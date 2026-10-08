package models

import (
	"time"

	"github.com/uptrace/bun"
)

// PersonalMemory 身份卡 + 表達 DNA（② 靜態記憶，直寫 PG）
type PersonalMemory struct {
	bun.BaseModel `bun:"table:personal_memory,alias:pm"`

	ID            string                 `bun:"id,pk" json:"id"`
	AgentID       string                 `bun:"agent_id,notnull" json:"agent_id"`
	Identity      map[string]interface{} `bun:"identity,type:jsonb,notnull" json:"identity"`
	ExpressionDNA map[string]interface{} `bun:"expression_dna,type:jsonb" json:"expression_dna,omitempty"`
	CreatedAt     time.Time              `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt     time.Time              `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

// ProceduralMemory 心智模型 + 決策啟發式
type ProceduralMemory struct {
	bun.BaseModel `bun:"table:procedural_memory,alias:prm"`

	ID          string                 `bun:"id,pk" json:"id"`
	AgentID     string                 `bun:"agent_id,notnull" json:"agent_id"`
	ModelName   string                 `bun:"model_name,notnull" json:"model_name"`
	Content     string                 `bun:"content,type:text,notnull" json:"content"`
	Heuristics  map[string]interface{} `bun:"heuristics,type:jsonb" json:"heuristics,omitempty"`
	EmbeddingID string                 `bun:"embedding_id" json:"embedding_id,omitempty"`
	CreatedAt   time.Time              `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt   time.Time              `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

// EpisodicMemory 對話記錄（單輪 or 整段）
type EpisodicMemory struct {
	bun.BaseModel `bun:"table:episodic_memory,alias:em"`

	ID        string                 `bun:"id,pk" json:"id"`
	AgentID   string                 `bun:"agent_id,notnull" json:"agent_id"`
	SessionID string                 `bun:"session_id,notnull" json:"session_id"`
	Turns     map[string]interface{} `bun:"turns,type:jsonb,notnull" json:"turns"`
	Summary   string                 `bun:"summary,type:text" json:"summary,omitempty"`
	OccurredAt time.Time             `bun:"occurred_at,nullzero,notnull,default:current_timestamp" json:"occurred_at"`
	CreatedAt time.Time              `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
}

// ToolMemory 工具記憶（簽名、使用統計、成功率）
type ToolMemory struct {
	bun.BaseModel `bun:"table:tool_memory,alias:tm"`

	ID         string                 `bun:"id,pk" json:"id"`
	AgentID    string                 `bun:"agent_id,notnull" json:"agent_id"`
	ToolName   string                 `bun:"tool_name,notnull" json:"tool_name"`
	Signature  map[string]interface{} `bun:"signature,type:jsonb,notnull" json:"signature"`
	UsageStats map[string]interface{} `bun:"usage_stats,type:jsonb" json:"usage_stats,omitempty"`
	CreatedAt  time.Time              `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt  time.Time              `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

// ---------- Request / Response DTOs ----------

type CreatePersonalMemoryRequest struct {
	AgentID       string                 `json:"agent_id" binding:"required"`
	Identity      map[string]interface{} `json:"identity" binding:"required"`
	ExpressionDNA map[string]interface{} `json:"expression_dna,omitempty"`
}

type UpdatePersonalMemoryRequest struct {
	Identity      map[string]interface{} `json:"identity,omitempty"`
	ExpressionDNA map[string]interface{} `json:"expression_dna,omitempty"`
}

type CreateProceduralMemoryRequest struct {
	AgentID    string                 `json:"agent_id" binding:"required"`
	ModelName  string                 `json:"model_name" binding:"required"`
	Content    string                 `json:"content" binding:"required"`
	Heuristics map[string]interface{} `json:"heuristics,omitempty"`
}

type CreateEpisodicMemoryRequest struct {
	AgentID   string                 `json:"agent_id" binding:"required"`
	SessionID string                 `json:"session_id" binding:"required"`
	Turns     map[string]interface{} `json:"turns" binding:"required"`
	Summary   string                 `json:"summary,omitempty"`
}

type CreateToolMemoryRequest struct {
	AgentID    string                 `json:"agent_id" binding:"required"`
	ToolName   string                 `json:"tool_name" binding:"required"`
	Signature  map[string]interface{} `json:"signature" binding:"required"`
	UsageStats map[string]interface{} `json:"usage_stats,omitempty"`
}

type MemoryResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data"`
}
