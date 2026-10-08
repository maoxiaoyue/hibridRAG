package models

import "time"

// SkillPack agent 的技能封包，對應 skills/{agent_id}/ 目錄下的 SKILL.md + references/
// 由 services/skill_loader 在 agent runtime 載入並快取，Inbound 推理前組入 system prompt
type SkillPack struct {
	AgentID    string                `json:"agent_id"`
	Body       string                `json:"body"`       // SKILL.md 全文（純 markdown，無 front-matter）
	References map[string]*Reference `json:"references"` // key = 相對路徑
	LoadedAt   time.Time             `json:"loaded_at"`
	SourceDir  string                `json:"source_dir"` // skills/{agent_id}

	// 快取失效依據
	SkillMTime    time.Time `json:"skill_mtime"`
	SkillChecksum string    `json:"skill_checksum"` // SKILL.md sha256
	RefsChecksum  string    `json:"refs_checksum"`  // 聚合 hash：sorted("path:checksum\n"...) 再 sha256
}

// Reference references/ 目錄下的單一子檔
type Reference struct {
	Path     string    `json:"path"`     // 相對 skills/{agent_id}/ 的路徑，例如 references/foo.md
	Title    string    `json:"title"`    // 首個 H1，退化為檔名
	Body     string    `json:"body"`
	MTime    time.Time `json:"mtime"`
	Checksum string    `json:"checksum"` // sha256

	// 載入時預抽的動態挑選輔助欄位
	Tokens   int      `json:"tokens"`   // 粗估 token 數（供預算控制）
	Keywords []string `json:"keywords"` // 標題 + H2/H3 + 粗體字抽出
}

// LoadSkillRequest POST /api/skill/load
type LoadSkillRequest struct {
	AgentID string `json:"agent_id"`
	Force   bool   `json:"force"` // true 時忽略 mtime/checksum，強制重讀
}

// LoadSkillResponse
type LoadSkillResponse struct {
	Pack      *SkillPack `json:"pack"`
	Cached    bool       `json:"cached"`    // true = 完全命中 cache 未重讀
	Reloaded  []string   `json:"reloaded"`  // 因 mtime/checksum 變更而重讀的檔案相對路徑
	ElapsedMs int64      `json:"elapsed_ms"`
}

// GetSkillRequest GET /api/skill/{agent_id}
type GetSkillRequest struct {
	AgentID string `json:"agent_id"`
}

// SelectReferencesRequest POST /api/skill/select
// Inbound 推理前呼叫，從 references/ 動態挑選與 query 相關的片段
type SelectReferencesRequest struct {
	AgentID   string `json:"agent_id"`
	Query     string `json:"query"`      // 通常是最近一則 user message
	MaxTokens int    `json:"max_tokens"` // 總 token 預算
	MaxDocs   int    `json:"max_docs"`   // 最多挑幾份
}

// SelectReferencesResponse
type SelectReferencesResponse struct {
	Picked []PickedRef `json:"picked"`
}

// PickedRef 單筆挑選結果
type PickedRef struct {
	Path  string  `json:"path"`
	Title string  `json:"title"`
	Score float64 `json:"score"`
	Body  string  `json:"body"`
}
