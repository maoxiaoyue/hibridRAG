package services

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/uptrace/bun"

	"agent_memory/app/models"
)

// MemoryService 處理四類記憶（Personal / Procedural / Episodic / Tool）的 CRUD
type MemoryService struct {
	db *bun.DB
}

func NewMemoryService(db *bun.DB) *MemoryService {
	return &MemoryService{db: db}
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// ── Personal ─────────────────────────────────────────────────────────────────

func (s *MemoryService) CreatePersonal(ctx context.Context, req models.CreatePersonalMemoryRequest) (*models.PersonalMemory, error) {
	m := &models.PersonalMemory{
		ID:            newID("pm"),
		AgentID:       req.AgentID,
		Identity:      req.Identity,
		ExpressionDNA: req.ExpressionDNA,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if _, err := s.db.NewInsert().Model(m).Exec(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *MemoryService) GetPersonal(ctx context.Context, id string) (*models.PersonalMemory, error) {
	m := new(models.PersonalMemory)
	err := s.db.NewSelect().Model(m).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

func (s *MemoryService) UpdatePersonal(ctx context.Context, id string, req models.UpdatePersonalMemoryRequest) (*models.PersonalMemory, error) {
	m, err := s.GetPersonal(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Identity != nil {
		m.Identity = req.Identity
	}
	if req.ExpressionDNA != nil {
		m.ExpressionDNA = req.ExpressionDNA
	}
	m.UpdatedAt = time.Now()
	if _, err := s.db.NewUpdate().Model(m).WherePK().Exec(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// UpsertPersonal 以 agent_id 為鍵執行 upsert（ON CONFLICT DO UPDATE）
func (s *MemoryService) UpsertPersonal(ctx context.Context, req models.CreatePersonalMemoryRequest) (*models.PersonalMemory, error) {
	m := &models.PersonalMemory{
		ID:            newID("pm"),
		AgentID:       req.AgentID,
		Identity:      req.Identity,
		ExpressionDNA: req.ExpressionDNA,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	_, err := s.db.NewInsert().Model(m).
		On("CONFLICT (agent_id) DO UPDATE").
		Set("identity = EXCLUDED.identity").
		Set("expression_dna = EXCLUDED.expression_dna").
		Set("updated_at = CURRENT_TIMESTAMP").
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	actual := new(models.PersonalMemory)
	if err := s.db.NewSelect().Model(actual).Where("agent_id = ?", req.AgentID).Scan(ctx); err != nil {
		return nil, err
	}
	return actual, nil
}

// ── Procedural ────────────────────────────────────────────────────────────────

func (s *MemoryService) CreateProcedural(ctx context.Context, req models.CreateProceduralMemoryRequest) (*models.ProceduralMemory, error) {
	m := &models.ProceduralMemory{
		ID:         newID("prm"),
		AgentID:    req.AgentID,
		ModelName:  req.ModelName,
		Content:    req.Content,
		Heuristics: req.Heuristics,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if _, err := s.db.NewInsert().Model(m).Exec(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *MemoryService) GetProcedural(ctx context.Context, id string) (*models.ProceduralMemory, error) {
	m := new(models.ProceduralMemory)
	err := s.db.NewSelect().Model(m).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// UpsertProcedural 以 (agent_id, model_name) 為鍵執行 SELECT-then-upsert
func (s *MemoryService) UpsertProcedural(ctx context.Context, req models.CreateProceduralMemoryRequest) (*models.ProceduralMemory, error) {
	existing := new(models.ProceduralMemory)
	err := s.db.NewSelect().Model(existing).
		Where("agent_id = ? AND model_name = ?", req.AgentID, req.ModelName).
		Limit(1).Scan(ctx)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		m := &models.ProceduralMemory{
			ID:         newID("prm"),
			AgentID:    req.AgentID,
			ModelName:  req.ModelName,
			Content:    req.Content,
			Heuristics: req.Heuristics,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		if _, err := s.db.NewInsert().Model(m).Exec(ctx); err != nil {
			return nil, err
		}
		return m, nil
	case err != nil:
		return nil, err
	default:
		existing.Content = req.Content
		existing.Heuristics = req.Heuristics
		existing.UpdatedAt = time.Now()
		if _, err := s.db.NewUpdate().Model(existing).WherePK().
			Column("content", "heuristics", "updated_at").Exec(ctx); err != nil {
			return nil, err
		}
		return existing, nil
	}
}

// ── Episodic ──────────────────────────────────────────────────────────────────

func (s *MemoryService) CreateEpisodic(ctx context.Context, req models.CreateEpisodicMemoryRequest) (*models.EpisodicMemory, error) {
	m := &models.EpisodicMemory{
		ID:         newID("em"),
		AgentID:    req.AgentID,
		SessionID:  req.SessionID,
		Turns:      req.Turns,
		Summary:    req.Summary,
		OccurredAt: time.Now(),
		CreatedAt:  time.Now(),
	}
	if _, err := s.db.NewInsert().Model(m).Exec(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *MemoryService) GetEpisodic(ctx context.Context, id string) (*models.EpisodicMemory, error) {
	m := new(models.EpisodicMemory)
	err := s.db.NewSelect().Model(m).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// ── List / Lookup helpers（給 ContextService 使用）─────────────────────────

// GetPersonalByAgent 取得指定 agent 的 PersonalMemory（unique per agent_id）
func (s *MemoryService) GetPersonalByAgent(ctx context.Context, agentID string) (*models.PersonalMemory, error) {
	m := new(models.PersonalMemory)
	err := s.db.NewSelect().Model(m).Where("agent_id = ?", agentID).Limit(1).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// ListProceduralByAgent 取得 agent 的 ProceduralMemory（依 created_at DESC）
//
// MVP 階段以 recency 排序；Phase 2 把 content 寫入 Cassandra 後改走 ANN 排序。
func (s *MemoryService) ListProceduralByAgent(ctx context.Context, agentID string, limit int) ([]models.ProceduralMemory, error) {
	if limit <= 0 {
		limit = 100
	}
	var ms []models.ProceduralMemory
	err := s.db.NewSelect().Model(&ms).
		Where("agent_id = ?", agentID).
		Order("created_at DESC").
		Limit(limit).
		Scan(ctx)
	return ms, err
}

// ListEpisodicBySession 依 session 取最近 N 筆對話記錄；session_id 留空則回傳該 agent 全域最近
func (s *MemoryService) ListEpisodicBySession(ctx context.Context, agentID, sessionID string, limit int) ([]models.EpisodicMemory, error) {
	if limit <= 0 {
		limit = 10
	}
	var ms []models.EpisodicMemory
	q := s.db.NewSelect().Model(&ms).
		Where("agent_id = ?", agentID).
		Order("occurred_at DESC").
		Limit(limit)
	if sessionID != "" {
		q = q.Where("session_id = ?", sessionID)
	}
	err := q.Scan(ctx)
	return ms, err
}

// ── Tool ──────────────────────────────────────────────────────────────────────

func (s *MemoryService) CreateTool(ctx context.Context, req models.CreateToolMemoryRequest) (*models.ToolMemory, error) {
	m := &models.ToolMemory{
		ID:         newID("tm"),
		AgentID:    req.AgentID,
		ToolName:   req.ToolName,
		Signature:  req.Signature,
		UsageStats: req.UsageStats,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if _, err := s.db.NewInsert().Model(m).Exec(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *MemoryService) GetTool(ctx context.Context, id string) (*models.ToolMemory, error) {
	m := new(models.ToolMemory)
	err := s.db.NewSelect().Model(m).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}
