package services

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"

	"agent_memory/app/models"
)

// SharedMemoryService 處理跨 agent 協調用的 KV pool（以 pool_id 隔離，非 agent_id）。
type SharedMemoryService struct {
	db *bun.DB
}

func NewSharedMemoryService(db *bun.DB) *SharedMemoryService {
	return &SharedMemoryService{db: db}
}

// Get 依 (pool_id, key) 取一筆；找不到回傳 ErrNotFound
func (s *SharedMemoryService) Get(ctx context.Context, poolID, key string) (*models.SharedMemory, error) {
	m := new(models.SharedMemory)
	err := s.db.NewSelect().Model(m).
		Where("pool_id = ? AND key = ?", poolID, key).
		Limit(1).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// Upsert 以 (pool_id, key) 為鍵執行 upsert（ON CONFLICT DO UPDATE）
func (s *SharedMemoryService) Upsert(ctx context.Context, req models.UpsertSharedMemoryRequest) (*models.SharedMemory, error) {
	m := &models.SharedMemory{
		ID:        newID("shm"),
		PoolID:    req.PoolID,
		Key:       req.Key,
		Value:     req.Value,
		WrittenBy: req.WrittenBy,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_, err := s.db.NewInsert().Model(m).
		On("CONFLICT (pool_id, key) DO UPDATE").
		Set("value = EXCLUDED.value").
		Set("written_by = EXCLUDED.written_by").
		Set("updated_at = CURRENT_TIMESTAMP").
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	actual := new(models.SharedMemory)
	if err := s.db.NewSelect().Model(actual).
		Where("pool_id = ? AND key = ?", req.PoolID, req.Key).Scan(ctx); err != nil {
		return nil, err
	}
	return actual, nil
}

// List 列出 pool 內所有 key（依 updated_at DESC）；limit <= 0 → 100
func (s *SharedMemoryService) List(ctx context.Context, poolID string, limit int) ([]models.SharedMemory, error) {
	if limit <= 0 {
		limit = 100
	}
	var ms []models.SharedMemory
	err := s.db.NewSelect().Model(&ms).
		Where("pool_id = ?", poolID).
		Order("updated_at DESC").
		Limit(limit).
		Scan(ctx)
	return ms, err
}
