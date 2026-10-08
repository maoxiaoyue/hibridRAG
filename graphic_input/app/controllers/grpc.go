// grpc.go — GraphicInputService 的 gRPC 實作（供內網 CLI/腳本；client 分頁走 REST）。
package controllers

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"graphic_input/app/models"
	"graphic_input/app/services"
	"graphic_input/pb/graphic_inputpb"
)

// GRPCServer 實作 graphic_inputpb.GraphicInputServiceServer。
type GRPCServer struct {
	graphic_inputpb.UnimplementedGraphicInputServiceServer
	svc *services.InputService
}

// NewGRPCServer 建構。
func NewGRPCServer(svc *services.InputService) *GRPCServer { return &GRPCServer{svc: svc} }

// UploadDocuments client-streaming：首則 meta，後續 file chunk（同名續寫）。
func (s *GRPCServer) UploadDocuments(stream graphic_inputpb.GraphicInputService_UploadDocumentsServer) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "recv meta: %v", err)
	}
	meta := first.GetMeta()
	if meta == nil {
		return status.Error(codes.InvalidArgument, "first message must carry meta")
	}

	// 收 chunk（順序保存，維持上傳順序）
	order := []string{}
	buf := map[string][]byte{}
	appendChunk := func(chunk *graphic_inputpb.DocChunk) {
		name := chunk.GetFileName()
		if name == "" {
			return
		}
		if _, ok := buf[name]; !ok {
			order = append(order, name)
		}
		buf[name] = append(buf[name], chunk.GetData()...)
	}
	appendChunk(first)
	for {
		chunk, rerr := stream.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return status.Errorf(codes.Internal, "recv chunk: %v", rerr)
		}
		appendChunk(chunk)
	}

	files := make([]services.DocFile, 0, len(order))
	for _, name := range order {
		files = append(files, services.DocFile{Name: name, Data: buf[name]})
	}
	results, err := s.svc.UploadDocuments(stream.Context(), meta.GetTargets(), meta.GetActor(), meta.GetTags(), files)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	out := &graphic_inputpb.UploadResult{}
	for _, r := range results {
		out.Results = append(out.Results, &graphic_inputpb.DocResult{
			File:                r.File,
			ExtractedChars:      int32(r.ExtractedChars),
			ObsidianPath:        r.ObsidianPath,
			PgEntities:          int32(r.PGEntities),
			PgRelationsMerged:   int32(r.PGRelationsMerged),
			PgRelationsProposed: int32(r.PGRelationsProposed),
			PgEmbeddings:        int32(r.PGEmbeddings),
			Error:               r.Error,
		})
	}
	return stream.SendAndClose(out)
}

// UpsertEntity 結構化實體輸入。
func (s *GRPCServer) UpsertEntity(ctx context.Context, req *graphic_inputpb.UpsertEntityReq) (*graphic_inputpb.UpsertEntityResp, error) {
	e := req.GetEntity()
	if e == nil || e.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "entity.name is required")
	}
	resp, err := s.svc.UpsertEntity(ctx, models.UpsertEntityRequest{
		Entity: models.EntityInput{Name: e.GetName(), Category: e.GetCategory(), Description: e.GetDescription()},
		Force:  req.GetForce(),
		Actor:  req.GetActor(),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &graphic_inputpb.UpsertEntityResp{Written: resp.Written, EmbeddingSynced: resp.EmbeddingSynced}
	for _, c := range resp.Candidates {
		out.Candidates = append(out.Candidates, &graphic_inputpb.SimilarCandidate{
			Name: c.Name, Category: c.Category, Description: c.Description,
		})
	}
	return out, nil
}

// UpsertRelation 結構化關係輸入。
func (s *GRPCServer) UpsertRelation(ctx context.Context, req *graphic_inputpb.UpsertRelationReq) (*graphic_inputpb.UpsertRelationResp, error) {
	if req.GetSource() == "" || req.GetTarget() == "" {
		return nil, status.Error(codes.InvalidArgument, "source and target are required")
	}
	if err := s.svc.UpsertRelation(ctx, models.UpsertRelationRequest{
		Source: req.GetSource(), Target: req.GetTarget(), Type: req.GetType(), Actor: req.GetActor(),
	}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &graphic_inputpb.UpsertRelationResp{Ok: true}, nil
}

// CheckDuplicates 重複偵測預查。
func (s *GRPCServer) CheckDuplicates(ctx context.Context, req *graphic_inputpb.CheckDuplicatesReq) (*graphic_inputpb.CheckDuplicatesResp, error) {
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	candidates, err := s.svc.CheckDuplicates(ctx, req.GetName())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &graphic_inputpb.CheckDuplicatesResp{}
	for _, c := range candidates {
		out.Candidates = append(out.Candidates, &graphic_inputpb.SimilarCandidate{
			Name: c.Name, Category: c.Category, Description: c.Description,
		})
	}
	return out, nil
}

// BatchImport client-streaming 批次匯入。
func (s *GRPCServer) BatchImport(stream graphic_inputpb.GraphicInputService_BatchImportServer) error {
	var rows []models.BatchRow
	actor := ""
	for {
		row, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return status.Errorf(codes.Internal, "recv row: %v", err)
		}
		if actor == "" && row.GetActor() != "" {
			actor = row.GetActor()
		}
		r := models.BatchRow{Kind: row.GetKind()}
		if e := row.GetEntity(); e != nil {
			r.Entity = &models.EntityInput{Name: e.GetName(), Category: e.GetCategory(), Description: e.GetDescription()}
		}
		if rel := row.GetRelation(); rel != nil {
			r.Relation = &models.UpsertRelationRequest{
				Source: rel.GetSource(), Target: rel.GetTarget(), Type: rel.GetType(),
			}
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return status.Error(codes.InvalidArgument, "no rows")
	}
	resp := s.svc.BatchImport(stream.Context(), actor, rows)
	out := &graphic_inputpb.BatchResult{Ok: int32(resp.OK), Failed: int32(resp.Failed)}
	for _, r := range resp.Rows {
		out.Rows = append(out.Rows, &graphic_inputpb.BatchRowResult{Index: int32(r.Index), Result: r.Result})
	}
	return stream.SendAndClose(out)
}

// ListInputLog 輸入歷史。
func (s *GRPCServer) ListInputLog(ctx context.Context, req *graphic_inputpb.ListInputLogReq) (*graphic_inputpb.ListInputLogResp, error) {
	entries, err := s.svc.ListLog(ctx, int(req.GetLimit()))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &graphic_inputpb.ListInputLogResp{}
	for _, e := range entries {
		payload, _ := json.Marshal(e.Payload)
		out.Entries = append(out.Entries, &graphic_inputpb.InputLogEntry{
			Id: e.ID, Actor: e.Actor, Action: e.Action, Target: e.Target,
			PayloadJson: string(payload), Result: e.Result,
			CreatedAt: e.CreatedAt.Format(time.RFC3339),
		})
	}
	return out, nil
}
