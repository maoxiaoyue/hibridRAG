package models

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// AgentSkill A2A skill descriptor stored inside AgentRegistration.Skills JSONB
type AgentSkill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AgentCapabilities capability flags from the agent-card
type AgentCapabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"push_notifications"`
}

// AgentRegistration 代表一個已注冊的 A2A agent
type AgentRegistration struct {
	bun.BaseModel `bun:"table:agent_registrations,alias:ar"`

	ID           string            `bun:"id,pk" json:"id"`
	Name         string            `bun:"name,notnull" json:"name"`
	Description  string            `bun:"description" json:"description,omitempty"`
	URL          string            `bun:"url,notnull" json:"url"`
	GRPCEndpoint string            `bun:"grpc_endpoint" json:"grpc_endpoint,omitempty"` // host:port，空表示不支援 gRPC
	Version      string            `bun:"version" json:"version,omitempty"`
	Skills       []AgentSkill      `bun:"skills,type:jsonb,notnull" json:"skills"`
	Capabilities AgentCapabilities `bun:"capabilities,type:jsonb,notnull" json:"capabilities"`
	LastSeenAt   time.Time         `bun:"last_seen_at,nullzero,notnull,default:current_timestamp" json:"last_seen_at"`
	CreatedAt    time.Time         `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt    time.Time         `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

// ── Request / Response DTOs ──────────────────────────────────────────────────

type RegisterAgentRequest struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	URL          string            `json:"url"`
	GRPCEndpoint string            `json:"grpc_endpoint,omitempty"`
	Version      string            `json:"version,omitempty"`
	Skills       []AgentSkill      `json:"skills"`
	Capabilities AgentCapabilities `json:"capabilities"`
}

type SearchAgentsResponse struct {
	Agents []AgentRegistration `json:"agents"`
	Total  int                 `json:"total"`
}

// AutoMigrate creates the agent_registrations table and GIN index
func AutoMigrate(db *bun.DB) error {
	ctx := context.Background()
	if _, err := db.NewCreateTable().Model((*AgentRegistration)(nil)).IfNotExists().Exec(ctx); err != nil {
		return fmt.Errorf("migrate agent_registrations: %w", err)
	}
	// GIN index for skill search on JSONB
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_agent_registrations_skills
		ON agent_registrations USING GIN (skills)
	`); err != nil {
		return fmt.Errorf("create skills GIN index: %w", err)
	}
	return nil
}
