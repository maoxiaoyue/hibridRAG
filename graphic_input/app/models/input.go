// Package models — graphic_input 的持久化模型與 REST DTO。
package models

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// InputLog 每一次人工輸入的紀錄（undo 參考；真正的圖譜 rollback 不做——
// AGE 沒有好用的事務語意，記 log 供人工修復）。
type InputLog struct {
	bun.BaseModel `bun:"table:graphic_input_log,alias:gil"`

	ID        string         `bun:"id,pk" json:"id"`
	Actor     string         `bun:"actor" json:"actor,omitempty"`
	Action    string         `bun:"action,notnull" json:"action"`   // upload_doc | upsert_entity | upsert_relation | batch_import
	Target    string         `bun:"target" json:"target,omitempty"` // obsidian | pg | both | -
	Payload   map[string]any `bun:"payload,type:jsonb" json:"payload,omitempty"`
	Result    string         `bun:"result,notnull" json:"result"` // ok | rejected_duplicate | error:<msg>
	CreatedAt time.Time      `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
}

// AutoMigrate 建 graphic_input_log 表。
func AutoMigrate(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().Model((*InputLog)(nil)).IfNotExists().Exec(ctx)
	return err
}

// ── REST DTO ────────────────────────────────────────────────────────────────

// EntityInput 結構化實體輸入。
type EntityInput struct {
	Name        string `json:"name"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
}

// SimilarCandidate 重複偵測回饋的相似實體。
type SimilarCandidate struct {
	Name        string `json:"name"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
}

// UpsertEntityRequest POST /api/input/entity 請求。
type UpsertEntityRequest struct {
	Entity EntityInput `json:"entity"`
	Force  bool        `json:"force,omitempty"` // 有相似候選時預設拒絕，force=true 強制新建
	Actor  string      `json:"actor,omitempty"`
}

// UpsertEntityResponse Written=false 代表被重複偵測擋下，看 Candidates。
type UpsertEntityResponse struct {
	Written         bool               `json:"written"`
	EmbeddingSynced bool               `json:"embedding_synced"`
	Candidates      []SimilarCandidate `json:"candidates,omitempty"`
}

// UpsertRelationRequest POST /api/input/relation 請求。
type UpsertRelationRequest struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type,omitempty"` // 空 → "related_to"
	Actor  string `json:"actor,omitempty"`
}

// CheckDuplicatesRequest POST /api/input/check 請求。
type CheckDuplicatesRequest struct {
	Name string `json:"name"`
}

// BatchRow 批次匯入的一列（entity / relation 擇一）。
type BatchRow struct {
	Kind     string                 `json:"kind"` // entity | relation
	Entity   *EntityInput           `json:"entity,omitempty"`
	Relation *UpsertRelationRequest `json:"relation,omitempty"`
}

// BatchImportRequest POST /api/input/batch 請求。CSV 與 Rows 擇一：
//   - CSVKind=entity 時 CSV 每列 `name,category,description`
//   - CSVKind=relation 時 CSV 每列 `source,type,target`
type BatchImportRequest struct {
	Actor   string     `json:"actor,omitempty"`
	Rows    []BatchRow `json:"rows,omitempty"`
	CSVKind string     `json:"csv_kind,omitempty"` // entity | relation
	CSV     string     `json:"csv,omitempty"`
}

// BatchRowResult 單列結果。
type BatchRowResult struct {
	Index  int    `json:"index"`
	Result string `json:"result"` // ok | rejected_duplicate | error:<msg>
}

// BatchImportResponse 批次結果。
type BatchImportResponse struct {
	OK     int              `json:"ok"`
	Failed int              `json:"failed"`
	Rows   []BatchRowResult `json:"rows"`
}

// DocResult 文件上傳的單檔結果。
type DocResult struct {
	File                string `json:"file"`
	ExtractedChars      int    `json:"extracted_chars"`
	ObsidianPath        string `json:"obsidian_path,omitempty"`
	PGEntities          int    `json:"pg_entities,omitempty"`
	PGRelationsMerged   int    `json:"pg_relations_merged,omitempty"`
	PGRelationsProposed int    `json:"pg_relations_proposed,omitempty"`
	PGEmbeddings        int    `json:"pg_embeddings,omitempty"`
	Error               string `json:"error,omitempty"`
}
