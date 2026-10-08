package models

// 對應後端（graphic_input 服務，預設 :8505；內網 dev_090 專用）：
//   POST /api/input/upload    （multipart：files[] + targets + tags + actor）
//   POST /api/input/entity    （GIUpsertEntityRequest / GIUpsertEntityResponse）
//   POST /api/input/relation  （GIUpsertRelationRequest）
//   POST /api/input/check     （GICheckRequest / GISimilarCandidate[]）
//   POST /api/input/batch     （GIBatchRequest / GIBatchResponse）
//   GET  /api/input/log       （GIInputLog[]）

// GIEntityInput 結構化實體輸入。
type GIEntityInput struct {
	Name        string `json:"name"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
}

// GISimilarCandidate 重複偵測回饋的相似實體。
type GISimilarCandidate struct {
	Name        string `json:"name"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
}

// GIUpsertEntityRequest 建立/更新實體。
type GIUpsertEntityRequest struct {
	Entity GIEntityInput `json:"entity"`
	Force  bool          `json:"force,omitempty"`
	Actor  string        `json:"actor,omitempty"`
}

// GIUpsertEntityResponse Written=false = 被重複偵測擋下（看 Candidates）。
type GIUpsertEntityResponse struct {
	Written         bool                 `json:"written"`
	EmbeddingSynced bool                 `json:"embedding_synced"`
	Candidates      []GISimilarCandidate `json:"candidates,omitempty"`
}

// GIUpsertRelationRequest 建立關係。
type GIUpsertRelationRequest struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type,omitempty"`
	Actor  string `json:"actor,omitempty"`
}

// GICheckRequest 重複偵測預查。
type GICheckRequest struct {
	Name string `json:"name"`
}

// GIBatchRequest 批次匯入（CSV）。
type GIBatchRequest struct {
	Actor   string `json:"actor,omitempty"`
	CSVKind string `json:"csv_kind"` // entity | relation
	CSV     string `json:"csv"`
}

// GIBatchRowResult 單列結果。
type GIBatchRowResult struct {
	Index  int    `json:"index"`
	Result string `json:"result"`
}

// GIBatchResponse 批次結果。
type GIBatchResponse struct {
	OK     int                `json:"ok"`
	Failed int                `json:"failed"`
	Rows   []GIBatchRowResult `json:"rows"`
}

// GIDocResult 文件上傳單檔結果。
type GIDocResult struct {
	File                string `json:"file"`
	ExtractedChars      int    `json:"extracted_chars"`
	ObsidianPath        string `json:"obsidian_path,omitempty"`
	PGEntities          int    `json:"pg_entities,omitempty"`
	PGRelationsMerged   int    `json:"pg_relations_merged,omitempty"`
	PGRelationsProposed int    `json:"pg_relations_proposed,omitempty"`
	PGEmbeddings        int    `json:"pg_embeddings,omitempty"`
	Error               string `json:"error,omitempty"`
}

// GIInputLog 輸入歷史一筆。
type GIInputLog struct {
	ID        string         `json:"id"`
	Actor     string         `json:"actor,omitempty"`
	Action    string         `json:"action"`
	Target    string         `json:"target,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	Result    string         `json:"result"`
	CreatedAt string         `json:"created_at"`
}
