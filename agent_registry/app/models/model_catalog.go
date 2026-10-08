package models

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"time"
)

// Modalities 一顆模型可接受的「輸入內容型態」。文字為所有 LLM 的基本輸入；
// 影像來自 vision 能力；音訊為預留欄位（Ollama 現行 capabilities 尚無標準 audio，
// 未來若出現 audio 能力即可自動帶出）。
type Modalities struct {
	Text  bool `json:"text"`
	Image bool `json:"image"`
	Audio bool `json:"audio"`
}

// LLMModel 記錄一顆（由 Ollama 探得的）可用模型與其能力 / 輸入型態。
// 這是 agent_client 選 model 時的下拉來源；資料由 POST /registry/models/sync
// 從 Ollama /api/tags + /api/show 拉取後 upsert。
type LLMModel struct {
	bun.BaseModel `bun:"table:llm_models,alias:lm"`

	Name          string     `bun:"name,pk" json:"name"`                              // Ollama tag，例 "qwen3.5:9b"
	Host          string     `bun:"host,notnull" json:"host"`                         // 探得來源 Ollama base URL
	Modalities    Modalities `bun:"modalities,type:jsonb,notnull" json:"modalities"`  // text / image / audio
	Capabilities  []string   `bun:"capabilities,type:jsonb,notnull" json:"capabilities"` // Ollama 原始 capabilities（vision/tools/embedding/thinking...）
	Family        string     `bun:"family" json:"family,omitempty"`
	ParameterSize string     `bun:"parameter_size" json:"parameter_size,omitempty"`
	Quantization  string     `bun:"quantization" json:"quantization,omitempty"`
	SizeBytes     int64      `bun:"size_bytes" json:"size_bytes,omitempty"`
	ContextLen    int        `bun:"context_len" json:"context_len,omitempty"`
	LastSyncedAt  time.Time  `bun:"last_synced_at,nullzero,notnull,default:current_timestamp" json:"last_synced_at"`
	CreatedAt     time.Time  `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt     time.Time  `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

// ── Response DTOs ────────────────────────────────────────────────────────────

type ListModelsResponse struct {
	Models []LLMModel `json:"models"`
	Total  int        `json:"total"`
}

type SyncModelsResponse struct {
	Host   string     `json:"host"`
	Synced int        `json:"synced"`
	Models []LLMModel `json:"models"`
}

// MigrateModelCatalog 建立 llm_models 表（與 agent_registrations 分開，供 root.go 呼叫）。
func MigrateModelCatalog(db *bun.DB) error {
	ctx := context.Background()
	if _, err := db.NewCreateTable().Model((*LLMModel)(nil)).IfNotExists().Exec(ctx); err != nil {
		return fmt.Errorf("migrate llm_models: %w", err)
	}
	return nil
}
