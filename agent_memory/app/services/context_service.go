package services

import (
	"context"
	"errors"

	"agent_memory/app/models"
)

// ContextService 組裝 RAG context：PersonalMemory anchor + ProceduralMemory top-N
//                                    + EpisodicMemory top-N + LongTermMemory ANN top-N
//
// 設計重點：
//   - 任何子查詢失敗都不阻擋整體回應；缺一塊就回空 slice / nil
//   - LongTerm 為可選：Cassandra 未啟用或 ltm == nil 時直接跳過
//   - Caller 可預先計算 query_vector；service 自身不做 embedding
type ContextService struct {
	mem *MemoryService
	ltm *LongTermMemoryService // 允許為 nil
}

func NewContextService(mem *MemoryService, ltm *LongTermMemoryService) *ContextService {
	return &ContextService{mem: mem, ltm: ltm}
}

// Build 依 BuildContextRequest 拼出完整 RAG context
func (s *ContextService) Build(ctx context.Context, req models.BuildContextRequest) (*models.BuildContextResponse, error) {
	if req.AgentID == "" {
		return nil, errors.New("agent_id required")
	}
	if req.TopKProcedural == 0 {
		req.TopKProcedural = 3
	}
	if req.TopKEpisodic == 0 {
		req.TopKEpisodic = 2
	}
	if req.TopKLongTerm == 0 {
		req.TopKLongTerm = 5
	}

	resp := &models.BuildContextResponse{
		Procedural: []models.ProceduralMemory{},
		Episodic:   []models.EpisodicMemory{},
		LongTerm:   []models.LongTermMemoryHit{},
	}

	// PersonalMemory anchor（缺則 nil，不視為錯誤）
	if pm, err := s.mem.GetPersonalByAgent(ctx, req.AgentID); err == nil {
		resp.Personal = pm
	}

	// ProceduralMemory（recency 排序）
	if pms, err := s.mem.ListProceduralByAgent(ctx, req.AgentID, req.TopKProcedural); err == nil {
		resp.Procedural = pms
	}

	// EpisodicMemory（最近 N 筆）
	if ems, err := s.mem.ListEpisodicBySession(ctx, req.AgentID, req.SessionID, req.TopKEpisodic); err == nil {
		resp.Episodic = ems
	}

	// LongTermMemory ANN（需 query_vector + Cassandra 已啟用）
	if s.ltm != nil && len(req.QueryVector) == 1024 {
		hits, err := s.ltm.SearchANN(ctx, models.SearchLongTermMemoryRequest{
			AgentID:     req.AgentID,
			QueryVector: req.QueryVector,
			TopK:        req.TopKLongTerm,
			Category:    req.Category,
		})
		if err == nil {
			resp.LongTerm = hits
		}
	}

	return resp, nil
}
