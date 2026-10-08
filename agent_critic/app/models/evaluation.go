package models

import (
	"time"

	"github.com/uptrace/bun"
)

// CriticEvaluation 一次答案品質評估的完整紀錄。
// 保留 question/answer/sources 原文，供 revision loop 追蹤與日後訓練 LLM judge 使用。
type CriticEvaluation struct {
	bun.BaseModel `bun:"table:critic_evaluations,alias:ce"`

	ID           string    `bun:"id,pk" json:"id"`
	AgentID      string    `bun:"agent_id,notnull" json:"agent_id"`
	SessionID    string    `bun:"session_id" json:"session_id,omitempty"`
	Revision     int       `bun:"revision,notnull,default:0" json:"revision"`
	Question     string    `bun:"question,type:text,notnull" json:"question"`
	Answer       string    `bun:"answer,type:text,notnull" json:"answer"`
	Sources      []string  `bun:"sources,type:jsonb" json:"sources,omitempty"`
	Completeness float64   `bun:"completeness,notnull" json:"completeness"`
	Grounding    float64   `bun:"grounding,notnull" json:"grounding"`
	Faithfulness float64   `bun:"faithfulness,notnull" json:"faithfulness"`
	Overall      float64   `bun:"overall,notnull" json:"overall"`
	Passed       bool      `bun:"passed,notnull" json:"passed"`
	Feedback     []string  `bun:"feedback,type:jsonb" json:"feedback,omitempty"`
	CreatedAt    time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
}

// EvaluateRequest POST /api/critic/evaluate 的輸入
type EvaluateRequest struct {
	AgentID   string   `json:"agent_id"`
	SessionID string   `json:"session_id"`
	Question  string   `json:"question"`
	Answer    string   `json:"answer"`
	Sources   []string `json:"sources"`
	Revision  int      `json:"revision"`
}
