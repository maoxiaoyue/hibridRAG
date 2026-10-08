package services

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/uptrace/bun"

	"agent_critic/app/models"
)

// CriticService 評估答案品質並持久化評估紀錄
type CriticService struct {
	db     *bun.DB
	scorer *LexicalScorer
}

func NewCriticService(db *bun.DB, scorer *LexicalScorer) *CriticService {
	return &CriticService{db: db, scorer: scorer}
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// Evaluate 評分並寫入一筆評估紀錄
func (s *CriticService) Evaluate(ctx context.Context, req models.EvaluateRequest) (*models.CriticEvaluation, error) {
	score := s.scorer.Score(req.Question, req.Answer, req.Sources)

	m := &models.CriticEvaluation{
		ID:           newID("ce"),
		AgentID:      req.AgentID,
		SessionID:    req.SessionID,
		Revision:     req.Revision,
		Question:     req.Question,
		Answer:       req.Answer,
		Sources:      req.Sources,
		Completeness: score.Completeness,
		Grounding:    score.Grounding,
		Faithfulness: score.Faithfulness,
		Overall:      score.Overall,
		Passed:       score.Passed,
		Feedback:     score.Feedback,
		CreatedAt:    time.Now(),
	}
	if _, err := s.db.NewInsert().Model(m).Exec(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// Get 取得單筆評估紀錄
func (s *CriticService) Get(ctx context.Context, id string) (*models.CriticEvaluation, error) {
	m := new(models.CriticEvaluation)
	err := s.db.NewSelect().Model(m).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// ListBySession 依 session 取最近的評估紀錄（revision loop 追蹤用）
func (s *CriticService) ListBySession(ctx context.Context, sessionID string, limit int) ([]models.CriticEvaluation, error) {
	if limit <= 0 {
		limit = 20
	}
	var list []models.CriticEvaluation
	err := s.db.NewSelect().Model(&list).
		Where("session_id = ?", sessionID).
		Order("created_at DESC").
		Limit(limit).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return list, nil
}
