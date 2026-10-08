package models

import (
	"time"

	"github.com/uptrace/bun"
)

// IngestJobStatus 攝取任務狀態
type IngestJobStatus string

const (
	IngestStatusPending   IngestJobStatus = "pending"
	IngestStatusRunning   IngestJobStatus = "running"
	IngestStatusCompleted IngestJobStatus = "completed"
	IngestStatusFailed    IngestJobStatus = "failed"
)

// IngestJobKind 攝取來源類型
type IngestJobKind string

const (
	IngestKindSkill     IngestJobKind = "skill"
	IngestKindReference IngestJobKind = "reference"
)

// IngestJob 攝取任務（③ 知識攝取管線的單位）
type IngestJob struct {
	bun.BaseModel `bun:"table:ingest_jobs,alias:ij"`

	ID          string                 `bun:"id,pk" json:"id"`
	AgentID     string                 `bun:"agent_id,notnull" json:"agent_id"`
	Kind        IngestJobKind          `bun:"kind,notnull" json:"kind"`
	SourcePath  string                 `bun:"source_path,notnull" json:"source_path"`
	Status      IngestJobStatus        `bun:"status,notnull,default:'pending'" json:"status"`
	TotalChunks int                    `bun:"total_chunks,default:0" json:"total_chunks"`
	DoneChunks  int                    `bun:"done_chunks,default:0" json:"done_chunks"`
	Error       string                 `bun:"error,type:text" json:"error,omitempty"`
	Metadata    map[string]interface{} `bun:"metadata,type:jsonb" json:"metadata,omitempty"`
	CreatedAt   time.Time              `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt   time.Time              `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
	FinishedAt  *time.Time             `bun:"finished_at" json:"finished_at,omitempty"`
}

// ---------- Request / Response DTOs ----------

// IngestSkillRequest POST /api/ingest/skill
type IngestSkillRequest struct {
	AgentID    string `json:"agent_id" binding:"required"`
	SkillPath  string `json:"skill_path" binding:"required"` // SKILL.md 檔案路徑
	OverwriteIdentity bool `json:"overwrite_identity,omitempty"`
}

// IngestReferencesRequest POST /api/ingest/references
type IngestReferencesRequest struct {
	AgentID string   `json:"agent_id" binding:"required"`
	Paths   []string `json:"paths" binding:"required"` // references/*.md 路徑列表
	Tags    []string `json:"tags,omitempty"`
}

// IngestJobResponse 攝取任務回應
type IngestJobResponse struct {
	JobID  string          `json:"job_id"`
	Status IngestJobStatus `json:"status"`
}
