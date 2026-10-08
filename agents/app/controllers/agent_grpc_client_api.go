// agent_grpc_client_api.go — dev_pub_0.9：供 agent_gateway 轉發 client 流量的
// gRPC 介面（Chat / UploadIngest / GetIngestJob）。
//
// 全部是既有邏輯的薄包裝：
//   - Chat         → ChatSync（agent_chat.go，REST /api/chat 同一條路徑）
//   - UploadIngest → 落地檔案 + extractToText + EnqueueReferences（ingest_upload.go 同一條流程）
//   - GetIngestJob → IngestService.GetJob
package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"agent/app/models"
)

// maxUploadStreamBytes 單次 UploadIngest stream 的總量上限（與 REST multipart 同值）。
const maxUploadStreamBytes = maxUploadBytes

// Chat gRPC 版同步對話——直接委派給 ChatSync，pb ↔ DTO 雙向轉換。
func (s *AgentServer) Chat(ctx context.Context, req *models.ChatTurnReq) (*models.ChatTurnResp, error) {
	dto := models.ChatTurnRequest{
		AgentID:        req.GetAgentId(),
		SessionID:      req.GetSessionId(),
		ContinuationID: req.GetContinuationId(),
		Temperature:    req.GetTemperature(),
	}
	for _, m := range req.GetMessages() {
		dto.Messages = append(dto.Messages, models.ChatTurnMessage{Role: m.GetRole(), Content: m.GetContent()})
	}
	for _, t := range req.GetClientTools() {
		dto.ClientTools = append(dto.ClientTools, models.ClientToolSpec{
			Name:        t.GetName(),
			Description: t.GetDescription(),
			Schema:      json.RawMessage(t.GetSchemaJson()),
		})
	}
	for _, r := range req.GetToolResults() {
		dto.ToolResults = append(dto.ToolResults, models.ClientToolResult{
			ToolCallID: r.GetToolCallId(),
			Name:       r.GetName(),
			Content:    r.GetContent(),
			IsError:    r.GetIsError(),
		})
	}

	resp, httpStatus, err := s.ChatSync(ctx, dto)
	if err != nil {
		return nil, status.Error(httpToGRPCCode(httpStatus), err.Error())
	}

	out := &models.ChatTurnResp{
		Status:         resp.Status,
		Content:        resp.Content,
		SessionId:      resp.SessionID,
		ContinuationId: resp.ContinuationID,
	}
	for _, c := range resp.ToolCalls {
		argsJSON, _ := json.Marshal(c.Args)
		out.ToolCalls = append(out.ToolCalls, &models.ChatToolCall{
			Id: c.ID, Name: c.Name, ArgsJson: string(argsJSON),
		})
	}
	if resp.Usage != nil {
		out.Usage = &models.TokenUsage{
			PromptTokens:     int32(resp.Usage.PromptTokens),
			CompletionTokens: int32(resp.Usage.CompletionTokens),
			TotalTokens:      int32(resp.Usage.TotalTokens),
		}
	}
	return out, nil
}

// UploadIngest client-streaming 上傳：首則訊息帶 meta（agent_id/tags），
// 後續訊息帶 file_name + data chunk（同名續寫）。收完後走 REST 上傳同一條
// extractToText → EnqueueReferences 流程。
func (s *AgentServer) UploadIngest(stream models.AgentService_UploadIngestServer) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "recv meta: %v", err)
	}
	meta := first.GetMeta()
	if meta == nil || strings.TrimSpace(meta.GetAgentId()) == "" {
		return status.Error(codes.InvalidArgument, "first message must carry meta with agent_id")
	}
	agentID := strings.TrimSpace(meta.GetAgentId())
	tags := meta.GetTags()

	dir := uploadsDirFor(agentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return status.Errorf(codes.Internal, "mkdir upload dir: %v", err)
	}

	// 收 chunk：同一 file_name 續寫同一個檔案；落地檔名加隨機前綴防覆蓋。
	open := map[string]*os.File{} // file_name → 落地檔
	paths := map[string]string{}  // file_name → 落地路徑
	var total int64
	defer func() {
		for _, f := range open {
			f.Close()
		}
	}()

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return status.Errorf(codes.Internal, "recv chunk: %v", err)
		}
		name := chunk.GetFileName()
		if name == "" {
			continue // meta-only 或空訊息，略過
		}
		total += int64(len(chunk.GetData()))
		if total > maxUploadStreamBytes {
			return status.Errorf(codes.ResourceExhausted, "upload exceeds %d bytes", int64(maxUploadStreamBytes))
		}
		f, ok := open[name]
		if !ok {
			dst := filepath.Join(dir, randPrefix()+"-"+sanitizeName(name))
			f, err = os.Create(dst)
			if err != nil {
				return status.Errorf(codes.Internal, "create %s: %v", dst, err)
			}
			open[name] = f
			paths[name] = dst
		}
		if _, err := f.Write(chunk.GetData()); err != nil {
			return status.Errorf(codes.Internal, "write %s: %v", name, err)
		}
	}
	for name, f := range open {
		if err := f.Close(); err != nil {
			return status.Errorf(codes.Internal, "close %s: %v", name, err)
		}
	}
	delete(open, "")
	if len(paths) == 0 {
		return status.Error(codes.InvalidArgument, "no file chunks received")
	}

	// 抽文字 + 攝取（同 REST 流程；單檔失敗跳過，全失敗才回錯）
	var ingestible []string
	var skipped []string
	for origName, rawPath := range paths {
		textPath, err := extractToText(stream.Context(), rawPath, origName)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", origName, err))
			continue
		}
		ingestible = append(ingestible, textPath)
	}
	if len(ingestible) == 0 {
		return status.Errorf(codes.InvalidArgument, "no ingestible files: %s", strings.Join(skipped, "; "))
	}

	job, err := getIngestService().EnqueueReferences(models.IngestReferencesRequest{
		AgentID: agentID,
		Paths:   ingestible,
		Tags:    tags,
	})
	if err != nil {
		return status.Errorf(codes.Internal, "enqueue: %v", err)
	}
	// 清空 open map 讓 defer 不重複 Close
	for k := range open {
		delete(open, k)
	}
	return stream.SendAndClose(&models.UploadAck{JobId: job.ID, Status: string(job.Status)})
}

// GetIngestJob gRPC 版攝取任務查詢。
func (s *AgentServer) GetIngestJob(ctx context.Context, req *models.IngestJobReq) (*models.IngestJobResp, error) {
	if req.GetJobId() == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id required")
	}
	job, err := getIngestService().GetJob(req.GetJobId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "job %s: %v", req.GetJobId(), err)
	}
	return &models.IngestJobResp{
		Id:          job.ID,
		Status:      string(job.Status),
		TotalChunks: int32(job.TotalChunks),
		DoneChunks:  int32(job.DoneChunks),
		Error:       job.Error,
	}, nil
}

// httpToGRPCCode 把 ChatSync 的 HTTP 狀態碼映成 gRPC code。
func httpToGRPCCode(httpStatus int) codes.Code {
	switch httpStatus {
	case 400:
		return codes.InvalidArgument
	case 404:
		return codes.NotFound
	case 503:
		return codes.Unavailable
	default:
		return codes.Internal
	}
}
