package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"agent_registry/app/models"
)

// ModelCatalogService 管理 llm_models 表：從 Ollama 同步、查詢。
type ModelCatalogService struct {
	db     *bun.DB
	prober *OllamaProber
}

func NewModelCatalogService(db *bun.DB, ollamaHost string) *ModelCatalogService {
	return &ModelCatalogService{db: db, prober: NewOllamaProber(ollamaHost)}
}

// Host 回傳目前探測的 Ollama host（供回應/日誌顯示）。
func (s *ModelCatalogService) Host() string { return s.prober.Host() }

// Sync 從 Ollama 探得目前模型清單並 upsert 進 llm_models，回傳同步後的完整清單。
func (s *ModelCatalogService) Sync(ctx context.Context) ([]models.LLMModel, error) {
	probed, err := s.prober.Probe(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for i := range probed {
		probed[i].CreatedAt = now
		probed[i].UpdatedAt = now
		if _, err := s.db.NewInsert().Model(&probed[i]).
			On("CONFLICT (name) DO UPDATE").
			Set("host = EXCLUDED.host").
			Set("modalities = EXCLUDED.modalities").
			Set("capabilities = EXCLUDED.capabilities").
			Set("family = EXCLUDED.family").
			Set("parameter_size = EXCLUDED.parameter_size").
			Set("quantization = EXCLUDED.quantization").
			Set("size_bytes = EXCLUDED.size_bytes").
			Set("context_len = EXCLUDED.context_len").
			Set("last_synced_at = EXCLUDED.last_synced_at").
			Set("updated_at = EXCLUDED.updated_at").
			Exec(ctx); err != nil {
			return nil, fmt.Errorf("upsert model %s: %w", probed[i].Name, err)
		}
	}
	return s.List(ctx)
}

// List 回傳所有已記錄模型（依名稱排序）。
func (s *ModelCatalogService) List(ctx context.Context) ([]models.LLMModel, error) {
	var ms []models.LLMModel
	if err := s.db.NewSelect().Model(&ms).Order("name ASC").Scan(ctx); err != nil {
		return nil, err
	}
	return ms, nil
}

// Get 依名稱查單一模型。
func (s *ModelCatalogService) Get(ctx context.Context, name string) (*models.LLMModel, error) {
	m := new(models.LLMModel)
	err := s.db.NewSelect().Model(m).Where("name = ?", name).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}
