// memory_grpc.go — gRPC 版 MemoryService，供 agents 服務的 ContextBuilder /
// EpisodicWriter / memory_read·memory_write 工具 / IngestRunner 呼叫，取代
// agents 自己另外重複實作、直接打同一個 Postgres 的舊做法。
//
// 涵蓋 personal/procedural/episodic（PG）+ long_term（Cassandra，semantic）+
// shared（PG，跨 agent KV pool）；其餘 CRUD（GetPersonal by id、Tool memory 等）
// 維持只走 HTTP REST（見 proto/memory.proto 註解）。
package controllers

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"agent_memory/app/models"
	"agent_memory/app/services"
	"agent_memory/pb/memorypb"
)

// MemoryGRPCServer 實作 memorypb.MemoryServiceServer。
type MemoryGRPCServer struct {
	memorypb.UnimplementedMemoryServiceServer
	svc    *services.MemoryService
	ltm    *services.LongTermMemoryService // nil-safe：Cassandra 未連上時停用 semantic RPC
	shared *services.SharedMemoryService
}

func NewMemoryGRPCServer(svc *services.MemoryService, ltm *services.LongTermMemoryService, shared *services.SharedMemoryService) *MemoryGRPCServer {
	return &MemoryGRPCServer{svc: svc, ltm: ltm, shared: shared}
}

func (s *MemoryGRPCServer) GetPersonalByAgent(ctx context.Context, req *memorypb.GetPersonalByAgentReq) (*memorypb.PersonalMemory, error) {
	m, err := s.svc.GetPersonalByAgent(ctx, req.GetAgentId())
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "personal memory not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return personalToProto(m)
}

func (s *MemoryGRPCServer) UpsertPersonal(ctx context.Context, req *memorypb.UpsertPersonalReq) (*memorypb.PersonalMemory, error) {
	m, err := s.svc.UpsertPersonal(ctx, models.CreatePersonalMemoryRequest{
		AgentID:       req.GetAgentId(),
		Identity:      req.GetIdentity().AsMap(),
		ExpressionDNA: req.GetExpressionDna().AsMap(),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return personalToProto(m)
}

func (s *MemoryGRPCServer) ListProceduralByAgent(ctx context.Context, req *memorypb.ListProceduralByAgentReq) (*memorypb.ListProceduralByAgentResp, error) {
	list, err := s.svc.ListProceduralByAgent(ctx, req.GetAgentId(), int(req.GetLimit()))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	items := make([]*memorypb.ProceduralMemory, 0, len(list))
	for i := range list {
		pm, err := proceduralToProto(&list[i])
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		items = append(items, pm)
	}
	return &memorypb.ListProceduralByAgentResp{Items: items}, nil
}

func (s *MemoryGRPCServer) UpsertProcedural(ctx context.Context, req *memorypb.UpsertProceduralReq) (*memorypb.ProceduralMemory, error) {
	m, err := s.svc.UpsertProcedural(ctx, models.CreateProceduralMemoryRequest{
		AgentID:    req.GetAgentId(),
		ModelName:  req.GetModelName(),
		Content:    req.GetContent(),
		Heuristics: req.GetHeuristics().AsMap(),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return proceduralToProto(m)
}

func (s *MemoryGRPCServer) ListEpisodicByAgent(ctx context.Context, req *memorypb.ListEpisodicByAgentReq) (*memorypb.ListEpisodicByAgentResp, error) {
	// session_id 留空 → agent 全域最近 N 筆（對應 agents 端 ListEpisodicByAgent 的語意）。
	list, err := s.svc.ListEpisodicBySession(ctx, req.GetAgentId(), "", int(req.GetLimit()))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	items := make([]*memorypb.EpisodicMemory, 0, len(list))
	for i := range list {
		em, err := episodicToProto(&list[i])
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		items = append(items, em)
	}
	return &memorypb.ListEpisodicByAgentResp{Items: items}, nil
}

func (s *MemoryGRPCServer) CreateEpisodic(ctx context.Context, req *memorypb.CreateEpisodicReq) (*memorypb.EpisodicMemory, error) {
	m, err := s.svc.CreateEpisodic(ctx, models.CreateEpisodicMemoryRequest{
		AgentID:   req.GetAgentId(),
		SessionID: req.GetSessionId(),
		Turns:     req.GetTurns().AsMap(),
		Summary:   req.GetSummary(),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return episodicToProto(m)
}

// ── Semantic memory (LongTermMemory) ─────────────────────────────────────────

func (s *MemoryGRPCServer) CreateLongTerm(ctx context.Context, req *memorypb.CreateLongTermReq) (*memorypb.LongTermMemory, error) {
	if s.ltm == nil {
		return nil, status.Error(codes.Unavailable, "long-term memory disabled (Cassandra not connected)")
	}
	m, err := s.ltm.Insert(ctx, models.CreateLongTermMemoryRequest{
		AgentID:    req.GetAgentId(),
		Category:   req.GetCategory(),
		Source:     req.GetSource(),
		Content:    req.GetContent(),
		Summary:    req.GetSummary(),
		Metadata:   req.GetMetadata(),
		Embedding:  req.GetEmbedding(),
		Importance: req.GetImportance(),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return longTermToProto(&models.LongTermMemoryHit{
		MemoryID: m.MemoryID, AgentID: m.AgentID, Category: m.Category, Source: m.Source,
		Content: m.Content, Summary: m.Summary, Metadata: m.Metadata, Importance: m.Importance,
	}, m.CreatedAt), nil
}

func (s *MemoryGRPCServer) SearchLongTerm(ctx context.Context, req *memorypb.SearchLongTermReq) (*memorypb.SearchLongTermResp, error) {
	if s.ltm == nil {
		return nil, status.Error(codes.Unavailable, "long-term memory disabled (Cassandra not connected)")
	}
	hits, err := s.ltm.SearchANN(ctx, models.SearchLongTermMemoryRequest{
		AgentID:     req.GetAgentId(),
		QueryVector: req.GetQueryVector(),
		TopK:        int(req.GetTopK()),
		Category:    req.GetCategory(),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	items := make([]*memorypb.LongTermMemory, 0, len(hits))
	for i := range hits {
		items = append(items, longTermToProto(&hits[i], time.Time{}))
	}
	return &memorypb.SearchLongTermResp{Items: items}, nil
}

// ── Shared memory（跨 agent KV pool）───────────────────────────────────────────

func (s *MemoryGRPCServer) GetShared(ctx context.Context, req *memorypb.GetSharedReq) (*memorypb.SharedMemory, error) {
	m, err := s.shared.Get(ctx, req.GetPoolId(), req.GetKey())
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "shared memory not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return sharedToProto(m)
}

func (s *MemoryGRPCServer) UpsertShared(ctx context.Context, req *memorypb.UpsertSharedReq) (*memorypb.SharedMemory, error) {
	m, err := s.shared.Upsert(ctx, models.UpsertSharedMemoryRequest{
		PoolID:    req.GetPoolId(),
		Key:       req.GetKey(),
		Value:     req.GetValue().AsMap(),
		WrittenBy: req.GetWrittenBy(),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return sharedToProto(m)
}

func (s *MemoryGRPCServer) ListShared(ctx context.Context, req *memorypb.ListSharedReq) (*memorypb.ListSharedResp, error) {
	list, err := s.shared.List(ctx, req.GetPoolId(), int(req.GetLimit()))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	items := make([]*memorypb.SharedMemory, 0, len(list))
	for i := range list {
		sm, err := sharedToProto(&list[i])
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		items = append(items, sm)
	}
	return &memorypb.ListSharedResp{Items: items}, nil
}

// ── model <-> proto 轉換 ──────────────────────────────────────────────────────

func personalToProto(m *models.PersonalMemory) (*memorypb.PersonalMemory, error) {
	identity, err := structOrEmpty(m.Identity)
	if err != nil {
		return nil, err
	}
	exprDNA, err := structOrEmpty(m.ExpressionDNA)
	if err != nil {
		return nil, err
	}
	return &memorypb.PersonalMemory{
		Id:            m.ID,
		AgentId:       m.AgentID,
		Identity:      identity,
		ExpressionDna: exprDNA,
		CreatedAt:     m.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:     m.UpdatedAt.Format(time.RFC3339Nano),
	}, nil
}

func proceduralToProto(m *models.ProceduralMemory) (*memorypb.ProceduralMemory, error) {
	heuristics, err := structOrEmpty(m.Heuristics)
	if err != nil {
		return nil, err
	}
	return &memorypb.ProceduralMemory{
		Id:         m.ID,
		AgentId:    m.AgentID,
		ModelName:  m.ModelName,
		Content:    m.Content,
		Heuristics: heuristics,
		CreatedAt:  m.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:  m.UpdatedAt.Format(time.RFC3339Nano),
	}, nil
}

func episodicToProto(m *models.EpisodicMemory) (*memorypb.EpisodicMemory, error) {
	turns, err := structOrEmpty(m.Turns)
	if err != nil {
		return nil, err
	}
	return &memorypb.EpisodicMemory{
		Id:         m.ID,
		AgentId:    m.AgentID,
		SessionId:  m.SessionID,
		Turns:      turns,
		Summary:    m.Summary,
		OccurredAt: m.OccurredAt.Format(time.RFC3339Nano),
		CreatedAt:  m.CreatedAt.Format(time.RFC3339Nano),
	}, nil
}


func structOrEmpty(m map[string]interface{}) (*structpb.Struct, error) {
	if m == nil {
		m = map[string]interface{}{}
	}
	return structpb.NewStruct(m)
}

// longTermToProto 轉換 LongTermMemoryHit（Insert/SearchANN 都不回傳 embedding，省流量）。
// createdAt 為零值時（SearchANN 命中不帶時間戳）省略該欄位。
func longTermToProto(h *models.LongTermMemoryHit, createdAt time.Time) *memorypb.LongTermMemory {
	out := &memorypb.LongTermMemory{
		MemoryId:   h.MemoryID,
		AgentId:    h.AgentID,
		Category:   h.Category,
		Source:     h.Source,
		Content:    h.Content,
		Summary:    h.Summary,
		Metadata:   h.Metadata,
		Importance: h.Importance,
	}
	if !createdAt.IsZero() {
		out.CreatedAt = createdAt.Format(time.RFC3339Nano)
	}
	return out
}

func sharedToProto(m *models.SharedMemory) (*memorypb.SharedMemory, error) {
	value, err := structOrEmpty(m.Value)
	if err != nil {
		return nil, err
	}
	return &memorypb.SharedMemory{
		PoolId:    m.PoolID,
		Key:       m.Key,
		Value:     value,
		WrittenBy: m.WrittenBy,
		CreatedAt: m.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: m.UpdatedAt.Format(time.RFC3339Nano),
	}, nil
}
