// graph_grpc.go — gRPC 版 GraphService，供 agents 服務的 graph_query 工具呼叫。
package controllers

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"agent_graph/app/models"
	"agent_graph/app/services"
	"agent_graph/pb/graphpb"
)

// GraphGRPCServer 實作 graphpb.GraphServiceServer。
type GraphGRPCServer struct {
	graphpb.UnimplementedGraphServiceServer
	facade *services.GraphFacade
	vault  *services.VaultFS   // nil/停用 → Vault* RPC 回 Unavailable
	temp   *services.VaultTemp // nil/停用 → AppendTemp 回 Unavailable
}

func NewGraphGRPCServer(facade *services.GraphFacade, vault *services.VaultFS, temp *services.VaultTemp) *GraphGRPCServer {
	return &GraphGRPCServer{facade: facade, vault: vault, temp: temp}
}

func grpcCode(err error) codes.Code {
	switch {
	case errors.Is(err, services.ErrGraphDisabled), errors.Is(err, services.ErrVectorDisabled):
		return codes.Unavailable
	case errors.Is(err, services.ErrNotFound):
		return codes.NotFound
	case errors.Is(err, services.ErrAlreadyReviewed):
		return codes.FailedPrecondition
	default:
		return codes.Internal
	}
}

func entityToProto(e models.EntityResult) *graphpb.Entity {
	return &graphpb.Entity{
		Name: e.Name, Category: e.Category, Description: e.Description,
		SourceFile: e.SourceFile, Score: e.Score, Degree: int32(e.Degree),
	}
}

func relationToProto(r models.RelationResult) *graphpb.Relation {
	return &graphpb.Relation{Source: r.Source, Target: r.Target, Type: r.Type}
}

func localContextToProto(r *models.LocalContextResponse) *graphpb.LocalContextResp {
	neighbors := make([]*graphpb.Entity, 0, len(r.Neighbors))
	for _, n := range r.Neighbors {
		neighbors = append(neighbors, entityToProto(n))
	}
	relations := make([]*graphpb.Relation, 0, len(r.Relations))
	for _, rel := range r.Relations {
		relations = append(relations, relationToProto(rel))
	}
	return &graphpb.LocalContextResp{
		Center: entityToProto(r.Center), Neighbors: neighbors, Relations: relations,
	}
}

// SearchEntities：query 空字串是合法輸入，等同 Kùzu 端 `CONTAINS ""`（比對全部
// 實體），client 端「進分頁先預載幾筆」的流程（見 pub_client/rag_admin/
// mobile_client 的 graph_view.go）就是靠這個語意運作——不可拒絕空 query。
func (s *GraphGRPCServer) SearchEntities(ctx context.Context, req *graphpb.SearchEntitiesReq) (*graphpb.SearchEntitiesResp, error) {
	entities, err := s.facade.SearchEntities(ctx, req.GetQuery(), int(req.GetLimit()))
	if err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}
	out := make([]*graphpb.Entity, 0, len(entities))
	for _, e := range entities {
		out = append(out, entityToProto(e))
	}
	return &graphpb.SearchEntitiesResp{Entities: out}, nil
}

func (s *GraphGRPCServer) GetLocalContext(ctx context.Context, req *graphpb.LocalContextReq) (*graphpb.LocalContextResp, error) {
	if req.GetEntityName() == "" {
		return nil, status.Error(codes.InvalidArgument, "entity_name is required")
	}
	resp, err := s.facade.GetLocalContext(ctx, req.GetEntityName(), int(req.GetHops()), int(req.GetLimit()))
	if err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}
	return localContextToProto(resp), nil
}

func (s *GraphGRPCServer) ExecuteCypher(ctx context.Context, req *graphpb.ExecuteCypherReq) (*graphpb.ExecuteCypherResp, error) {
	if req.GetQuery() == "" || len(req.GetColumns()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "query and columns are required")
	}
	var params map[string]any
	if req.GetParams() != nil {
		params = req.GetParams().AsMap()
	}
	rows, err := s.facade.ExecuteCypher(ctx, req.GetQuery(), params, req.GetColumns())
	if err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}
	out := make([]*structpb.Struct, 0, len(rows))
	for _, row := range rows {
		st, serr := structpb.NewStruct(row)
		if serr != nil {
			return nil, status.Error(codes.Internal, "encode row: "+serr.Error())
		}
		out = append(out, st)
	}
	return &graphpb.ExecuteCypherResp{Rows: out}, nil
}

func (s *GraphGRPCServer) HybridSearch(ctx context.Context, req *graphpb.HybridSearchReq) (*graphpb.HybridSearchResp, error) {
	if req.GetQuery() == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}
	resp, err := s.facade.HybridSearch(ctx, req.GetQuery(), int(req.GetTopK()), int(req.GetHops()))
	if err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}
	seeds := make([]*graphpb.Entity, 0, len(resp.Seeds))
	for _, e := range resp.Seeds {
		seeds = append(seeds, entityToProto(e))
	}
	ctxs := make([]*graphpb.LocalContextResp, 0, len(resp.Context))
	for i := range resp.Context {
		ctxs = append(ctxs, localContextToProto(&resp.Context[i]))
	}
	return &graphpb.HybridSearchResp{Seeds: seeds, Context: ctxs}, nil
}

func (s *GraphGRPCServer) ProposeRelation(ctx context.Context, req *graphpb.ProposeRelationReq) (*graphpb.ProposeRelationResp, error) {
	if req.GetSourceEntity() == "" || req.GetTargetEntity() == "" {
		return nil, status.Error(codes.InvalidArgument, "source_entity and target_entity are required")
	}
	pr, err := s.facade.ProposeRelation(ctx, models.ProposeRelationRequest{
		SourceEntity: req.GetSourceEntity(), TargetEntity: req.GetTargetEntity(),
		RelationType: req.GetRelationType(), Confidence: req.GetConfidence(),
		SourceFile: req.GetSourceFile(), ExtractedBy: req.GetExtractedBy(),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &graphpb.ProposeRelationResp{Id: pr.ID, Status: pr.Status}, nil
}

// ---- dev_pub_0.9：proposed_relations 審核（供 gateway 轉發 client 流量）----

func proposedToProto(pr *models.ProposedRelation) *graphpb.ProposedRelationMsg {
	return &graphpb.ProposedRelationMsg{
		Id:           pr.ID,
		SourceEntity: pr.SourceEntity,
		TargetEntity: pr.TargetEntity,
		RelationType: pr.RelationType,
		Confidence:   pr.Confidence,
		SourceFile:   pr.SourceFile,
		ExtractedBy:  pr.ExtractedBy,
		Status:       pr.Status,
		CreatedAt:    pr.CreatedAt.Format(time.RFC3339),
	}
}

func (s *GraphGRPCServer) ListProposed(ctx context.Context, req *graphpb.ListProposedReq) (*graphpb.ListProposedResp, error) {
	list, err := s.facade.ListProposed(ctx, req.GetStatus(), int(req.GetLimit()))
	if err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}
	out := make([]*graphpb.ProposedRelationMsg, 0, len(list))
	for i := range list {
		out = append(out, proposedToProto(&list[i]))
	}
	return &graphpb.ListProposedResp{Relations: out}, nil
}

func (s *GraphGRPCServer) ApproveProposed(ctx context.Context, req *graphpb.ReviewProposedReq) (*graphpb.ProposedRelationMsg, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	pr, err := s.facade.ApproveProposed(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}
	return proposedToProto(pr), nil
}

func (s *GraphGRPCServer) RejectProposed(ctx context.Context, req *graphpb.ReviewProposedReq) (*graphpb.ProposedRelationMsg, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	pr, err := s.facade.RejectProposed(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}
	return proposedToProto(pr), nil
}

// ---- dev_090 graphic_input 配套：直接寫入 + 純文字攝取 ----

func (s *GraphGRPCServer) UpsertEntity(ctx context.Context, req *graphpb.UpsertEntityWriteReq) (*graphpb.UpsertEntityWriteResp, error) {
	e := req.GetEntity()
	if e == nil || e.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "entity.name is required")
	}
	resp, err := s.facade.UpsertEntityDirect(ctx, models.UpsertEntityRequest{
		Name: e.GetName(), Category: e.GetCategory(), Description: e.GetDescription(), SourceFile: e.GetSourceFile(),
	})
	if err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}
	return &graphpb.UpsertEntityWriteResp{Name: resp.Name, EmbeddingSynced: resp.EmbeddingSynced}, nil
}

func (s *GraphGRPCServer) UpsertRelation(ctx context.Context, req *graphpb.UpsertRelationWriteReq) (*graphpb.UpsertRelationWriteResp, error) {
	r := req.GetRelation()
	if r == nil || r.GetSource() == "" || r.GetTarget() == "" {
		return nil, status.Error(codes.InvalidArgument, "relation.source and relation.target are required")
	}
	if err := s.facade.UpsertRelationDirect(ctx, models.UpsertRelationRequest{
		Source: r.GetSource(), Target: r.GetTarget(), Type: r.GetType(),
	}); err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}
	return &graphpb.UpsertRelationWriteResp{Ok: true}, nil
}

// ---- 知識沉澱暫存：AppendTemp 寫 vault/temp/ ----

func (s *GraphGRPCServer) AppendTemp(ctx context.Context, req *graphpb.AppendTempReq) (*graphpb.AppendTempResp, error) {
	if !s.temp.Enabled() {
		return nil, status.Error(codes.Unavailable, "vault temp is not configured on this server")
	}
	if req.GetContent() == "" {
		return nil, status.Error(codes.InvalidArgument, "content is required")
	}
	prefix := req.GetPrefix()
	if prefix == "" {
		prefix = time.Now().Format("20060102")
	}
	title := req.GetTitle()
	if title == "" {
		title = "note"
	}
	path, err := s.temp.Write(title, req.GetContent(), req.GetSource(), prefix)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &graphpb.AppendTempResp{Path: path}, nil
}

// ---- vault 檔案下載（pub_client「Sync Vault」，經 gateway 轉發）----

// vaultChunkBytes VaultFetch 每則 chunk 的上限，與 UploadIngest 的 64KB 慣例一致。
const vaultChunkBytes = 64 << 10

func (s *GraphGRPCServer) VaultManifest(ctx context.Context, _ *graphpb.VaultManifestReq) (*graphpb.VaultManifestResp, error) {
	if !s.vault.Enabled() {
		return nil, status.Error(codes.Unavailable, "vault is not configured on this server")
	}
	files, err := s.vault.Manifest(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := make([]*graphpb.VaultFileInfo, 0, len(files))
	for _, f := range files {
		out = append(out, &graphpb.VaultFileInfo{
			Path: f.Path, Size: f.Size, Sha256: f.SHA256, ModifiedUnix: f.ModifiedUnix,
		})
	}
	return &graphpb.VaultManifestResp{Files: out}, nil
}

func (s *GraphGRPCServer) VaultFetch(req *graphpb.VaultFetchReq, stream graphpb.GraphService_VaultFetchServer) error {
	if !s.vault.Enabled() {
		return status.Error(codes.Unavailable, "vault is not configured on this server")
	}
	if len(req.GetPaths()) == 0 {
		return status.Error(codes.InvalidArgument, "paths is required")
	}
	buf := make([]byte, vaultChunkBytes)
	for _, rel := range req.GetPaths() {
		f, err := s.vault.Open(rel)
		if err != nil {
			if os.IsNotExist(err) {
				return status.Errorf(codes.NotFound, "vault file %s: not found", rel)
			}
			// 路徑逃逸/非 .md 等驗證錯誤——明確拒絕整個請求
			return status.Errorf(codes.InvalidArgument, "vault file %s: %v", rel, err)
		}
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				if serr := stream.Send(&graphpb.VaultFileChunk{Path: rel, Data: buf[:n]}); serr != nil {
					f.Close()
					return serr
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				f.Close()
				return status.Errorf(codes.Internal, "read %s: %v", rel, rerr)
			}
		}
		f.Close()
		if err := stream.Send(&graphpb.VaultFileChunk{Path: rel, Eof: true}); err != nil {
			return err
		}
	}
	return nil
}

func (s *GraphGRPCServer) IngestText(ctx context.Context, req *graphpb.IngestTextReq) (*graphpb.IngestTextResp, error) {
	if req.GetText() == "" {
		return nil, status.Error(codes.InvalidArgument, "text is required")
	}
	resp, err := s.facade.IngestText(ctx, models.IngestTextRequest{
		Source: req.GetSource(), Text: req.GetText(), Actor: req.GetActor(),
	})
	if err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}
	return &graphpb.IngestTextResp{
		Entities:          int32(resp.Entities),
		RelationsMerged:   int32(resp.RelationsMerged),
		RelationsProposed: int32(resp.RelationsProposed),
		Embeddings:        int32(resp.Embeddings),
	}, nil
}
