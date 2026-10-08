// client_gateway.go — dev_pub_0.9 ClientGateway 實作：agent_client 全部流量的
// 單一入口，轉發到內部服務（agents / agent_graph gRPC + agent_embedding REST）。
//
// 認證在 services 的 APIKey interceptor 統一處理，這裡只做轉發與型別鏡像轉換。
package controllers

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"agent_gatway/app/models"
	"agent_gatway/app/services"
	"agent_gatway/internal/mtls"
	"agent_gatway/pb/gatewaypb"
	"agent_gatway/pb/graphpb"
)

// ClientGatewayServer 實作 gatewaypb.ClientGatewayServer。
type ClientGatewayServer struct {
	gatewaypb.UnimplementedClientGatewayServer

	version     string
	callTimeout time.Duration

	agentsConn *grpc.ClientConn // nil = internal.agents_grpc_addr 未設定
	graphConn  *grpc.ClientConn // nil = internal.graph_grpc_addr 未設定

	embeddingBase string // 空 = VerifySearch 停用
	agentsHTTPBase string // 空 = ChatStatus 回空狀態
	httpClient    *http.Client
}

// ClientGatewayOptions 由 commands/root.go 從 config 組出。
type ClientGatewayOptions struct {
	Version          string
	AgentsGRPCAddr   string
	GraphGRPCAddr    string
	EmbeddingBaseURL string
	AgentsHTTPBase   string
	ClientCertFile   string
	ClientKeyFile    string
	CAFile           string
	CallTimeoutSec   int
}

// NewClientGatewayServer 建立轉發器。對內連線用非阻塞 dial——下游沒起來不擋
// gateway 啟動，呼叫時自然回 Unavailable。
func NewClientGatewayServer(opt ClientGatewayOptions) (*ClientGatewayServer, error) {
	s := &ClientGatewayServer{
		version:        opt.Version,
		callTimeout:    time.Duration(opt.CallTimeoutSec) * time.Second,
		embeddingBase:  strings.TrimRight(opt.EmbeddingBaseURL, "/"),
		agentsHTTPBase: strings.TrimRight(opt.AgentsHTTPBase, "/"),
	}
	if s.callTimeout <= 0 {
		s.callTimeout = 120 * time.Second
	}

	creds, err := mtls.ClientCredentials(opt.ClientCertFile, opt.ClientKeyFile, opt.CAFile)
	if err != nil {
		return nil, fmt.Errorf("internal client tls: %w", err)
	}
	if opt.AgentsGRPCAddr != "" {
		conn, derr := grpc.NewClient(opt.AgentsGRPCAddr, grpc.WithTransportCredentials(creds))
		if derr != nil {
			return nil, fmt.Errorf("agents grpc client: %w", derr)
		}
		s.agentsConn = conn
	}
	if opt.GraphGRPCAddr != "" {
		conn, derr := grpc.NewClient(opt.GraphGRPCAddr, grpc.WithTransportCredentials(creds))
		if derr != nil {
			return nil, fmt.Errorf("graph grpc client: %w", derr)
		}
		s.graphConn = conn
	}

	// embedding 走內部 HTTPS（自簽/內部 CA）
	tr := &http.Transport{}
	if opt.CAFile != "" {
		pem, rerr := os.ReadFile(opt.CAFile)
		if rerr != nil {
			return nil, fmt.Errorf("read internal CA: %w", rerr)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no valid certs in %s", opt.CAFile)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	} else {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 — dev
	}
	s.httpClient = &http.Client{Timeout: 30 * time.Second, Transport: tr}

	return s, nil
}

// Close 收掉對內連線。
func (s *ClientGatewayServer) Close() {
	if s.agentsConn != nil {
		s.agentsConn.Close()
	}
	if s.graphConn != nil {
		s.graphConn.Close()
	}
}

func (s *ClientGatewayServer) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.callTimeout)
}

// ---- Ping ----

func (s *ClientGatewayServer) Ping(ctx context.Context, _ *gatewaypb.PingReq) (*gatewaypb.PingResp, error) {
	return &gatewaypb.PingResp{
		Version:    s.version,
		KeyName:    services.KeyNameFrom(ctx),
		ServerTime: time.Now().Format(time.RFC3339),
	}, nil
}

// ---- Chat ----

func (s *ClientGatewayServer) Chat(ctx context.Context, req *gatewaypb.ChatReq) (*gatewaypb.ChatResp, error) {
	if s.agentsConn == nil {
		return nil, status.Error(codes.Unavailable, "agents backend is not configured")
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()

	in := &models.ChatTurnReq{
		AgentId:        req.GetAgentId(),
		SessionId:      req.GetSessionId(),
		ContinuationId: req.GetContinuationId(),
		Temperature:    req.GetTemperature(),
	}
	for _, m := range req.GetMessages() {
		in.Messages = append(in.Messages, &models.Message{Role: m.GetRole(), Content: m.GetContent()})
	}
	for _, t := range req.GetClientTools() {
		in.ClientTools = append(in.ClientTools, &models.ChatToolSpec{
			Name: t.GetName(), Description: t.GetDescription(), SchemaJson: t.GetSchemaJson(),
		})
	}
	for _, r := range req.GetToolResults() {
		in.ToolResults = append(in.ToolResults, &models.ChatToolResult{
			ToolCallId: r.GetToolCallId(), Name: r.GetName(), Content: r.GetContent(), IsError: r.GetIsError(),
		})
	}

	resp, err := models.NewAgentServiceClient(s.agentsConn).Chat(cctx, in)
	if err != nil {
		return nil, err // 原樣透傳下游 gRPC status
	}

	out := &gatewaypb.ChatResp{
		Status:         resp.GetStatus(),
		Content:        resp.GetContent(),
		SessionId:      resp.GetSessionId(),
		ContinuationId: resp.GetContinuationId(),
	}
	for _, c := range resp.GetToolCalls() {
		out.ToolCalls = append(out.ToolCalls, &gatewaypb.ChatToolCall{
			Id: c.GetId(), Name: c.GetName(), ArgsJson: c.GetArgsJson(),
		})
	}
	if u := resp.GetUsage(); u != nil {
		out.Usage = &gatewaypb.TokenUsage{
			PromptTokens: u.GetPromptTokens(), CompletionTokens: u.GetCompletionTokens(), TotalTokens: u.GetTotalTokens(),
		}
	}
	return out, nil
}

// ---- ChatStatus（對話階段輪詢；內部 HTTP 打 agents /api/chat/status/:id）----

func (s *ClientGatewayServer) ChatStatus(ctx context.Context, req *gatewaypb.ChatStatusReq) (*gatewaypb.ChatStatusResp, error) {
	// agents HTTP base 未設定 → 回空狀態（client 據此顯示本地階段，不視為錯誤）。
	if s.agentsHTTPBase == "" || req.GetSessionId() == "" {
		return &gatewaypb.ChatStatusResp{}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	url := s.agentsHTTPBase + "/api/chat/status/" + neturl.PathEscape(req.GetSessionId())
	httpReq, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return &gatewaypb.ChatStatusResp{}, nil
	}
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return &gatewaypb.ChatStatusResp{}, nil // 輪詢失敗不擾動對話，回空狀態
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &gatewaypb.ChatStatusResp{}, nil
	}
	var env struct {
		Data struct {
			Stage  string `json:"stage"`
			Active bool   `json:"active"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return &gatewaypb.ChatStatusResp{}, nil
	}
	return &gatewaypb.ChatStatusResp{Stage: env.Data.Stage, Active: env.Data.Active}, nil
}

// ---- ChatImage（對話附加圖片辨識；內部 HTTP 打 agents /api/chat/image）----

func (s *ClientGatewayServer) ChatImage(ctx context.Context, req *gatewaypb.ChatImageReq) (*gatewaypb.ChatImageResp, error) {
	if s.agentsHTTPBase == "" {
		return nil, status.Error(codes.Unavailable, "agents http backend is not configured")
	}
	if len(req.GetImage()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "empty image")
	}
	// 視覺抽文字可能較久（VisionExtractor 預設 120s）——用專屬逾時的 client，沿用同一 TLS transport。
	cctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	url := s.agentsHTTPBase + "/api/chat/image?agent_id=" + neturl.QueryEscape(req.GetAgentId()) +
		"&filename=" + neturl.QueryEscape(req.GetFilename())
	httpReq, err := http.NewRequestWithContext(cctx, http.MethodPost, url, bytes.NewReader(req.GetImage()))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	httpReq.Header.Set("Content-Type", "application/octet-stream")

	client := &http.Client{Timeout: 120 * time.Second, Transport: s.httpClient.Transport}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "agents chat image: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, status.Errorf(codes.Internal, "agents chat image status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env struct {
		Data struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, status.Error(codes.Internal, "decode chat image response")
	}
	return &gatewaypb.ChatImageResp{Text: env.Data.Text}, nil
}

// ---- Persona 模型設定（內部 HTTP 打 agents /api/personas）----

// personaInfoJSON 對應 agents PersonaInfo 的 JSON。
type personaInfoJSON struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Model   string `json:"model"`
	RoleMD  string `json:"role_md"`
}

func toPBPersona(p personaInfoJSON) *gatewaypb.PersonaInfo {
	return &gatewaypb.PersonaInfo{
		AgentId: p.AgentID, Name: p.Name, Role: p.Role, Model: p.Model, RoleMd: p.RoleMD,
	}
}

func (s *ClientGatewayServer) ListPersonas(ctx context.Context, _ *gatewaypb.ListPersonasReq) (*gatewaypb.ListPersonasResp, error) {
	if s.agentsHTTPBase == "" {
		return nil, status.Error(codes.Unavailable, "agents http backend is not configured")
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, s.agentsHTTPBase+"/api/personas", nil)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "agents personas: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, status.Errorf(codes.Internal, "agents personas status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env struct {
		Data struct {
			Personas []personaInfoJSON `json:"personas"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, status.Error(codes.Internal, "decode personas response")
	}
	out := &gatewaypb.ListPersonasResp{}
	for _, p := range env.Data.Personas {
		out.Personas = append(out.Personas, toPBPersona(p))
	}
	return out, nil
}

func (s *ClientGatewayServer) SetPersonaModel(ctx context.Context, req *gatewaypb.SetPersonaModelReq) (*gatewaypb.PersonaInfo, error) {
	if s.agentsHTTPBase == "" {
		return nil, status.Error(codes.Unavailable, "agents http backend is not configured")
	}
	if req.GetAgentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id required")
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()

	body, _ := json.Marshal(map[string]string{"model": req.GetModel(), "role": req.GetRole()})
	url := s.agentsHTTPBase + "/api/personas/" + neturl.PathEscape(req.GetAgentId()) + "/model"
	httpReq, err := http.NewRequestWithContext(cctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "agents set persona: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, status.Errorf(codes.Internal, "agents set persona status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env struct {
		Data personaInfoJSON `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, status.Error(codes.Internal, "decode set persona response")
	}
	return toPBPersona(env.Data), nil
}

// ---- Persona SKILL.md 編輯（內部 HTTP 打 agents /api/personas/:id/skill）----

func (s *ClientGatewayServer) GetPersonaSkill(ctx context.Context, req *gatewaypb.GetPersonaSkillReq) (*gatewaypb.PersonaSkillResp, error) {
	if s.agentsHTTPBase == "" {
		return nil, status.Error(codes.Unavailable, "agents http backend is not configured")
	}
	if req.GetAgentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id required")
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()

	url := s.agentsHTTPBase + "/api/personas/" + neturl.PathEscape(req.GetAgentId()) + "/skill"
	httpReq, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "agents get persona skill: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, status.Errorf(codes.Internal, "agents get persona skill status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env struct {
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, status.Error(codes.Internal, "decode get persona skill response")
	}
	return &gatewaypb.PersonaSkillResp{AgentId: req.GetAgentId(), Content: env.Data.Content}, nil
}

func (s *ClientGatewayServer) SetPersonaSkill(ctx context.Context, req *gatewaypb.SetPersonaSkillReq) (*gatewaypb.PersonaSkillResp, error) {
	if s.agentsHTTPBase == "" {
		return nil, status.Error(codes.Unavailable, "agents http backend is not configured")
	}
	if req.GetAgentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id required")
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()

	body, _ := json.Marshal(map[string]string{"content": req.GetContent()})
	url := s.agentsHTTPBase + "/api/personas/" + neturl.PathEscape(req.GetAgentId()) + "/skill"
	httpReq, err := http.NewRequestWithContext(cctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "agents set persona skill: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, status.Errorf(codes.Internal, "agents set persona skill status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return &gatewaypb.PersonaSkillResp{AgentId: req.GetAgentId(), Content: req.GetContent()}, nil
}

// ---- UploadIngest（client stream → agents stream，邊收邊轉不整檔緩衝）----

func (s *ClientGatewayServer) UploadIngest(stream gatewaypb.ClientGateway_UploadIngestServer) error {
	if s.agentsConn == nil {
		return status.Error(codes.Unavailable, "agents backend is not configured")
	}
	cctx, cancel := s.callCtx(stream.Context())
	defer cancel()

	up, err := models.NewAgentServiceClient(s.agentsConn).UploadIngest(cctx)
	if err != nil {
		return err
	}
	for {
		chunk, rerr := stream.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return status.Errorf(codes.Internal, "recv from client: %v", rerr)
		}
		out := &models.UploadChunk{FileName: chunk.GetFileName(), Data: chunk.GetData()}
		if m := chunk.GetMeta(); m != nil {
			out.Meta = &models.UploadMeta{AgentId: m.GetAgentId(), Tags: m.GetTags()}
		}
		if serr := up.Send(out); serr != nil {
			return serr
		}
	}
	ack, err := up.CloseAndRecv()
	if err != nil {
		return err
	}
	return stream.SendAndClose(&gatewaypb.UploadAck{JobId: ack.GetJobId(), Status: ack.GetStatus()})
}

func (s *ClientGatewayServer) GetIngestJob(ctx context.Context, req *gatewaypb.IngestJobReq) (*gatewaypb.IngestJobResp, error) {
	if s.agentsConn == nil {
		return nil, status.Error(codes.Unavailable, "agents backend is not configured")
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()
	job, err := models.NewAgentServiceClient(s.agentsConn).GetIngestJob(cctx, &models.IngestJobReq{JobId: req.GetJobId()})
	if err != nil {
		return nil, err
	}
	return &gatewaypb.IngestJobResp{
		Id: job.GetId(), Status: job.GetStatus(),
		TotalChunks: job.GetTotalChunks(), DoneChunks: job.GetDoneChunks(), Error: job.GetError(),
	}, nil
}

// ---- Graph* ----

func (s *ClientGatewayServer) graphClient() (graphpb.GraphServiceClient, error) {
	if s.graphConn == nil {
		return nil, status.Error(codes.Unavailable, "graph backend is not configured")
	}
	return graphpb.NewGraphServiceClient(s.graphConn), nil
}

func gwEntity(e *graphpb.Entity) *gatewaypb.GraphEntity {
	if e == nil {
		return nil
	}
	return &gatewaypb.GraphEntity{
		Name: e.GetName(), Category: e.GetCategory(), Description: e.GetDescription(),
		SourceFile: e.GetSourceFile(), Score: e.GetScore(), Degree: e.GetDegree(),
	}
}

func gwContext(c *graphpb.LocalContextResp) *gatewaypb.GraphContextResp {
	out := &gatewaypb.GraphContextResp{Center: gwEntity(c.GetCenter())}
	for _, n := range c.GetNeighbors() {
		out.Neighbors = append(out.Neighbors, gwEntity(n))
	}
	for _, r := range c.GetRelations() {
		out.Relations = append(out.Relations, &gatewaypb.GraphRelation{
			Source: r.GetSource(), Target: r.GetTarget(), Type: r.GetType(),
		})
	}
	return out
}

func gwProposed(p *graphpb.ProposedRelationMsg) *gatewaypb.GraphProposedRelation {
	return &gatewaypb.GraphProposedRelation{
		Id: p.GetId(), SourceEntity: p.GetSourceEntity(), TargetEntity: p.GetTargetEntity(),
		RelationType: p.GetRelationType(), Confidence: p.GetConfidence(),
		SourceFile: p.GetSourceFile(), ExtractedBy: p.GetExtractedBy(),
		Status: p.GetStatus(), CreatedAt: p.GetCreatedAt(),
	}
}

func (s *ClientGatewayServer) GraphSearch(ctx context.Context, req *gatewaypb.GraphSearchReq) (*gatewaypb.GraphSearchResp, error) {
	gc, err := s.graphClient()
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()
	resp, err := gc.SearchEntities(cctx, &graphpb.SearchEntitiesReq{Query: req.GetQuery(), Limit: req.GetLimit()})
	if err != nil {
		return nil, err
	}
	out := &gatewaypb.GraphSearchResp{}
	for _, e := range resp.GetEntities() {
		out.Entities = append(out.Entities, gwEntity(e))
	}
	return out, nil
}

func (s *ClientGatewayServer) GraphContext(ctx context.Context, req *gatewaypb.GraphContextReq) (*gatewaypb.GraphContextResp, error) {
	gc, err := s.graphClient()
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()
	resp, err := gc.GetLocalContext(cctx, &graphpb.LocalContextReq{
		EntityName: req.GetEntityName(), Hops: req.GetHops(), Limit: req.GetLimit(),
	})
	if err != nil {
		return nil, err
	}
	return gwContext(resp), nil
}

func (s *ClientGatewayServer) GraphHybridSearch(ctx context.Context, req *gatewaypb.GraphHybridSearchReq) (*gatewaypb.GraphHybridSearchResp, error) {
	gc, err := s.graphClient()
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()
	resp, err := gc.HybridSearch(cctx, &graphpb.HybridSearchReq{
		Query: req.GetQuery(), TopK: req.GetTopK(), Hops: req.GetHops(),
	})
	if err != nil {
		return nil, err
	}
	out := &gatewaypb.GraphHybridSearchResp{}
	for _, e := range resp.GetSeeds() {
		out.Seeds = append(out.Seeds, gwEntity(e))
	}
	for _, c := range resp.GetContext() {
		out.Context = append(out.Context, gwContext(c))
	}
	return out, nil
}

func (s *ClientGatewayServer) GraphListProposed(ctx context.Context, req *gatewaypb.GraphListProposedReq) (*gatewaypb.GraphListProposedResp, error) {
	gc, err := s.graphClient()
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()
	resp, err := gc.ListProposed(cctx, &graphpb.ListProposedReq{Status: req.GetStatus(), Limit: req.GetLimit()})
	if err != nil {
		return nil, err
	}
	out := &gatewaypb.GraphListProposedResp{}
	for _, p := range resp.GetRelations() {
		out.Relations = append(out.Relations, gwProposed(p))
	}
	return out, nil
}

func (s *ClientGatewayServer) GraphApproveProposed(ctx context.Context, req *gatewaypb.GraphReviewProposedReq) (*gatewaypb.GraphProposedRelation, error) {
	gc, err := s.graphClient()
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()
	p, err := gc.ApproveProposed(cctx, &graphpb.ReviewProposedReq{Id: req.GetId()})
	if err != nil {
		return nil, err
	}
	return gwProposed(p), nil
}

func (s *ClientGatewayServer) GraphRejectProposed(ctx context.Context, req *gatewaypb.GraphReviewProposedReq) (*gatewaypb.GraphProposedRelation, error) {
	gc, err := s.graphClient()
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()
	p, err := gc.RejectProposed(cctx, &graphpb.ReviewProposedReq{Id: req.GetId()})
	if err != nil {
		return nil, err
	}
	return gwProposed(p), nil
}

// ---- Vault*（agent_graph GraphService.Vault*，Obsidian vault .md 下載）----

func (s *ClientGatewayServer) VaultManifest(ctx context.Context, _ *gatewaypb.VaultManifestReq) (*gatewaypb.VaultManifestResp, error) {
	gc, err := s.graphClient()
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.callCtx(ctx)
	defer cancel()
	resp, err := gc.VaultManifest(cctx, &graphpb.VaultManifestReq{})
	if err != nil {
		return nil, err
	}
	out := &gatewaypb.VaultManifestResp{}
	for _, f := range resp.GetFiles() {
		out.Files = append(out.Files, &gatewaypb.VaultFileInfo{
			Path: f.GetPath(), Size: f.GetSize(), Sha256: f.GetSha256(), ModifiedUnix: f.GetModifiedUnix(),
		})
	}
	return out, nil
}

// VaultFetch server-streaming 轉發：下游 Recv → 上游 Send，邊收邊轉不整檔緩衝
//（與 UploadIngest 相同原則，方向相反）。
func (s *ClientGatewayServer) VaultFetch(req *gatewaypb.VaultFetchReq, stream gatewaypb.ClientGateway_VaultFetchServer) error {
	gc, err := s.graphClient()
	if err != nil {
		return err
	}
	cctx, cancel := s.callCtx(stream.Context())
	defer cancel()
	down, err := gc.VaultFetch(cctx, &graphpb.VaultFetchReq{Paths: req.GetPaths()})
	if err != nil {
		return err
	}
	for {
		chunk, rerr := down.Recv()
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr // 原樣透傳下游 gRPC status（NotFound/InvalidArgument…）
		}
		if serr := stream.Send(&gatewaypb.VaultFileChunk{
			Path: chunk.GetPath(), Data: chunk.GetData(), Eof: chunk.GetEof(),
		}); serr != nil {
			return serr
		}
	}
}

// ---- VerifySearch（agent_embedding REST）----

func (s *ClientGatewayServer) VerifySearch(ctx context.Context, req *gatewaypb.VerifySearchReq) (*gatewaypb.VerifySearchResp, error) {
	if s.embeddingBase == "" {
		return nil, status.Error(codes.Unavailable, "embedding backend is not configured")
	}
	body, err := json.Marshal(map[string]any{
		"agent_id": req.GetAgentId(), "query": req.GetQuery(), "top_k": req.GetTopK(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal: %v", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.embeddingBase+"/api/search", bytes.NewReader(body))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "build request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "embedding call: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, status.Errorf(codes.Internal, "embedding status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, status.Errorf(codes.Internal, "decode: %v", err)
	}
	return &gatewaypb.VerifySearchResp{Total: int32(out.Total)}, nil
}
