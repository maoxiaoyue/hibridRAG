package models

import "time"

// LongTermMemory 對應 Cassandra long_term_memory 表
//
// 跨 session 持久化的語意記憶片段，可被 SAI ANN 相似度搜尋。
// 來源：EpisodicMemory consolidation / ingest pipeline / 使用者明確保存。
type LongTermMemory struct {
	MemoryID     string            `json:"memory_id"`
	AgentID      string            `json:"agent_id"`
	Category     string            `json:"category"` // knowledge / experience / concept / fact / preference
	Source       string            `json:"source,omitempty"`
	Content      string            `json:"content"`
	Summary      string            `json:"summary,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Embedding    []float32         `json:"embedding,omitempty"` // BGE-M3 dense, 1024 dims
	Importance   float32           `json:"importance"`
	CreatedAt    time.Time         `json:"created_at"`
	LastAccessed time.Time         `json:"last_accessed,omitempty"`
	AccessCount  int               `json:"access_count"`
}

// CreateLongTermMemoryRequest 寫入請求
type CreateLongTermMemoryRequest struct {
	AgentID    string            `json:"agent_id"`
	Category   string            `json:"category"`
	Source     string            `json:"source,omitempty"`
	Content    string            `json:"content"`
	Summary    string            `json:"summary,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Embedding  []float32         `json:"embedding"` // 必填，1024 dims
	Importance float32           `json:"importance,omitempty"`
}

// SearchLongTermMemoryRequest ANN 搜尋請求
type SearchLongTermMemoryRequest struct {
	AgentID     string    `json:"agent_id"`
	QueryVector []float32 `json:"query_vector"`       // 必填，1024 dims
	TopK        int       `json:"top_k,omitempty"`    // 預設 5
	Category    string    `json:"category,omitempty"` // 可選過濾
}

// LongTermMemoryHit 搜尋結果（不含 embedding，省流量）
type LongTermMemoryHit struct {
	MemoryID   string            `json:"memory_id"`
	AgentID    string            `json:"agent_id"`
	Category   string            `json:"category"`
	Source     string            `json:"source,omitempty"`
	Content    string            `json:"content"`
	Summary    string            `json:"summary,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Importance float32           `json:"importance"`
}

// ── BuildContext 用 ─────────────────────────────────────────────────────────

// BuildContextRequest 組裝 RAG context 的請求
type BuildContextRequest struct {
	AgentID        string    `json:"agent_id"`
	Query          string    `json:"query,omitempty"`        // 文字 query（保留，將來由 service 自動 embed）
	QueryVector    []float32 `json:"query_vector,omitempty"` // 預先計算的 BGE-M3 dense（1024）
	SessionID      string    `json:"session_id,omitempty"`
	Category       string    `json:"category,omitempty"` // long_term filter
	TopKProcedural int       `json:"top_k_procedural,omitempty"`
	TopKEpisodic   int       `json:"top_k_episodic,omitempty"`
	TopKLongTerm   int       `json:"top_k_long_term,omitempty"`
}

// BuildContextResponse 組裝後的 RAG context
type BuildContextResponse struct {
	Personal   *PersonalMemory     `json:"personal,omitempty"`
	Procedural []ProceduralMemory  `json:"procedural"`
	Episodic   []EpisodicMemory    `json:"episodic"`
	LongTerm   []LongTermMemoryHit `json:"long_term"`
}
