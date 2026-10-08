package models

import (
	"time"

	"github.com/uptrace/bun"
)

// SyncState 追蹤 Obsidian vault 每個檔案上次同步的內容雜湊，避免未變更的
// 檔案重複跑一次 LLM 抽取 + 圖譜寫入。
type SyncState struct {
	bun.BaseModel `bun:"table:graph_sync_state,alias:gss"`

	SourceFile   string    `bun:"source_file,pk" json:"source_file"`
	ContentHash  string    `bun:"content_hash,notnull" json:"content_hash"`
	LastSyncedAt time.Time `bun:"last_synced_at,nullzero,notnull,default:current_timestamp" json:"last_synced_at"`
}

// ProposedRelation 低信心的實體關係候選（LLM 抽取或未能精確消歧的 WikiLink），
// 不直接寫入圖譜，等待人工在 Obsidian vault 或審核介面確認後才 MERGE。
// 對應設計文件的 propose_new_relation。
type ProposedRelation struct {
	bun.BaseModel `bun:"table:proposed_relations,alias:pr"`

	ID           string    `bun:"id,pk" json:"id"`
	SourceEntity string    `bun:"source_entity,notnull" json:"source_entity"`
	TargetEntity string    `bun:"target_entity,notnull" json:"target_entity"`
	RelationType string    `bun:"relation_type,notnull" json:"relation_type"`
	Confidence   float64   `bun:"confidence,notnull" json:"confidence"`
	SourceFile   string    `bun:"source_file" json:"source_file,omitempty"`
	ExtractedBy  string    `bun:"extracted_by" json:"extracted_by,omitempty"` // 例 "ollama:qwen3.5:9b" 或 "wikilink"
	Status       string    `bun:"status,notnull,default:'pending'" json:"status"`
	CreatedAt    time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
}

// ── Request / Response DTOs ──────────────────────────────────────────────────

type SearchEntitiesRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type EntityResult struct {
	Name        string            `json:"name"`
	Category    string            `json:"category,omitempty"`
	Description string            `json:"description,omitempty"`
	SourceFile  string            `json:"source_file,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
	Score       float64           `json:"score,omitempty"`  // 向量相似度時填入
	Degree      int               `json:"degree,omitempty"` // 全圖關係數（facade 查詢時回填；client 用來決定節點大小/關鍵節點）
}

type RelationResult struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

type LocalContextRequest struct {
	EntityName string `json:"entity_name"`
	Hops       int    `json:"hops"`
	Limit      int    `json:"limit"`
}

type LocalContextResponse struct {
	Center    EntityResult     `json:"center"`
	Neighbors []EntityResult   `json:"neighbors"`
	Relations []RelationResult `json:"relations"`
}

type ExecuteCypherRequest struct {
	Query   string         `json:"query"`
	Params  map[string]any `json:"params,omitempty"`
	Columns []string       `json:"columns"` // RETURN 子句的欄位名，對應 agtype 型別解析
}

type ExecuteCypherResponse struct {
	Rows []map[string]any `json:"rows"`
}

type HybridSearchRequest struct {
	Query string `json:"query"` // 自然語言查詢，內部先算 embedding
	TopK  int    `json:"top_k"`
	Hops  int    `json:"hops"`
}

type HybridSearchResponse struct {
	Seeds   []EntityResult         `json:"seeds"`   // 向量相似度命中的種子實體
	Context []LocalContextResponse `json:"context"` // 每個種子的圖遍歷結果
}

type ProposeRelationRequest struct {
	SourceEntity string  `json:"source_entity"`
	TargetEntity string  `json:"target_entity"`
	RelationType string  `json:"relation_type"`
	Confidence   float64 `json:"confidence"`
	SourceFile   string  `json:"source_file,omitempty"`
	ExtractedBy  string  `json:"extracted_by,omitempty"`
}

// ListProposedRequest 查詢待審關係清單；Status 空字串 = "pending"。
type ListProposedRequest struct {
	Status string `json:"status,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// ── dev_090 graphic_input 配套：直接寫入 + 純文字攝取 ────────────────────────

// UpsertEntityRequest 直接建立/更新一個實體（graphic_input 人工輸入用；
// 不暴露給 agents 的 graph_query 工具、gateway 也不轉發）。
type UpsertEntityRequest struct {
	Name        string `json:"name"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
	SourceFile  string `json:"source_file,omitempty"` // 來源標記（如 "graphic_input:<actor>"）
}

// UpsertEntityResponse EmbeddingSynced=false 代表圖譜寫入成功但 embedding
// 同步失敗/停用——hybrid_search 會找不到這個實體，呼叫端可提示使用者。
type UpsertEntityResponse struct {
	Name            string `json:"name"`
	EmbeddingSynced bool   `json:"embedding_synced"`
}

// UpsertRelationRequest 直接建立一條關係（MERGE，端點不存在時自動補 stub）。
type UpsertRelationRequest struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// IngestTextRequest 純文字攝取：LLM 抽取實體/關係（confidence gate 護欄與
// vault 同步相同）+ 實體 embedding。graphic_input 的「PG 版本」文件輸入路徑。
type IngestTextRequest struct {
	Source string `json:"source"`          // 來源描述（原始檔名等），寫進 source_file 與 proposed_relations
	Text   string `json:"text"`            // 已抽好的純文字（PDF/OCR 由呼叫端處理）
	Actor  string `json:"actor,omitempty"` // 輸入者標記
}

// IngestTextResponse 抽取統計。
type IngestTextResponse struct {
	Entities          int `json:"entities"`           // upsert 的實體數
	RelationsMerged   int `json:"relations_merged"`   // 高信心直接 MERGE 的關係數
	RelationsProposed int `json:"relations_proposed"` // 低信心進待審的關係數
	Embeddings        int `json:"embeddings"`         // 成功同步 embedding 的實體數
}

// ReviewProposedRequest 核准/拒絕一筆待審關係。
type ReviewProposedRequest struct {
	ID string `json:"id"`
}

// ── vault 檔案下載（pub_client Sync Vault）────────────────────────────────────

// VaultFileInfo vault 內一個 .md 檔的清單項；client 端用 sha256 判斷要不要下載。
type VaultFileInfo struct {
	Path         string `json:"path"` // vault 相對路徑（forward slash）
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	ModifiedUnix int64  `json:"modified_unix"`
}
