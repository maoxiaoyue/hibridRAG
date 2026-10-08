package models

import "time"

// LongTermMemory — semantic memory：跨 session 持久化的語意記憶片段，
// 可被 SAI ANN 相似度搜尋。純 gRPC DTO（agent_memory 持有實際的 Cassandra 存取）。
type LongTermMemory struct {
	MemoryID   string            `json:"memory_id"`
	AgentID    string            `json:"agent_id"`
	Category   string            `json:"category"` // knowledge / experience / concept / fact / preference
	Source     string            `json:"source,omitempty"`
	Content    string            `json:"content"`
	Summary    string            `json:"summary,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Importance float32           `json:"importance"`
	CreatedAt  time.Time         `json:"created_at"`
}

// CreateLongTermMemoryRequest 寫入請求；Embedding 由呼叫端先算好（BGE-M3 1024 維）。
type CreateLongTermMemoryRequest struct {
	AgentID    string
	Category   string
	Source     string
	Content    string
	Summary    string
	Metadata   map[string]string
	Embedding  []float32
	Importance float32
}

// SearchLongTermMemoryRequest ANN 搜尋請求；QueryVector 由呼叫端先算好。
type SearchLongTermMemoryRequest struct {
	AgentID     string
	QueryVector []float32
	TopK        int
	Category    string
}

// LongTermMemoryHit 搜尋結果（不含 embedding）
type LongTermMemoryHit struct {
	MemoryID   string
	AgentID    string
	Category   string
	Source     string
	Content    string
	Summary    string
	Metadata   map[string]string
	Importance float32
}
