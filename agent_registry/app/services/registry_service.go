package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"agent_registry/app/models"
)

var ErrNotFound = errors.New("not found")

type RegistryService struct {
	db *bun.DB
}

func NewRegistryService(db *bun.DB) *RegistryService {
	return &RegistryService{db: db}
}

// Upsert registers or refreshes an agent's record (keyed by id)
func (s *RegistryService) Upsert(ctx context.Context, req models.RegisterAgentRequest) (*models.AgentRegistration, error) {
	if req.ID == "" || req.Name == "" || req.URL == "" {
		return nil, fmt.Errorf("id, name, and url are required")
	}

	now := time.Now()
	reg := &models.AgentRegistration{
		ID:           req.ID,
		Name:         req.Name,
		Description:  req.Description,
		URL:          req.URL,
		GRPCEndpoint: req.GRPCEndpoint,
		Version:      req.Version,
		Skills:       req.Skills,
		Capabilities: req.Capabilities,
		LastSeenAt:   now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if reg.Skills == nil {
		reg.Skills = []models.AgentSkill{}
	}

	_, err := s.db.NewInsert().Model(reg).
		On("CONFLICT (id) DO UPDATE").
		Set("name = EXCLUDED.name").
		Set("description = EXCLUDED.description").
		Set("url = EXCLUDED.url").
		Set("grpc_endpoint = EXCLUDED.grpc_endpoint").
		Set("version = EXCLUDED.version").
		Set("skills = EXCLUDED.skills").
		Set("capabilities = EXCLUDED.capabilities").
		Set("last_seen_at = EXCLUDED.last_seen_at").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("upsert registration: %w", err)
	}

	return s.GetByID(ctx, req.ID)
}

// GetByID fetches a single registration
func (s *RegistryService) GetByID(ctx context.Context, id string) (*models.AgentRegistration, error) {
	reg := new(models.AgentRegistration)
	err := s.db.NewSelect().Model(reg).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return reg, nil
}

// Search finds agents whose skills JSONB array contains a matching skill (by id or name).
//
// Skill ids are free-text and not namespaced per agent, so an unqualified substring
// search can accidentally match an unrelated (or stale/dead) registration whose skill
// id or name merely contains the search term. To keep routing deterministic, an exact,
// case-insensitive match on the skill id is always preferred: only when no registration
// exactly declares that skill id do we fall back to fuzzy substring matching on id/name.
func (s *RegistryService) Search(ctx context.Context, skill string) ([]models.AgentRegistration, error) {
	skill = strings.ToLower(strings.TrimSpace(skill))

	var regs []models.AgentRegistration
	if skill == "" {
		if err := s.db.NewSelect().Model(&regs).Order("last_seen_at DESC").Scan(ctx); err != nil {
			return nil, err
		}
		return regs, nil
	}

	exactQ := s.db.NewSelect().Model(&regs).Order("last_seen_at DESC").Where(`EXISTS (
		SELECT 1 FROM jsonb_array_elements(ar.skills) AS s
		WHERE lower(s->>'id') = ?
	)`, skill)
	if err := exactQ.Scan(ctx); err != nil {
		return nil, err
	}
	if len(regs) > 0 {
		return regs, nil
	}

	// Fallback: no agent declares this exact skill id — try a fuzzy match on id or name.
	var fuzzy []models.AgentRegistration
	fuzzyQ := s.db.NewSelect().Model(&fuzzy).Order("last_seen_at DESC").Where(`EXISTS (
		SELECT 1 FROM jsonb_array_elements(ar.skills) AS s
		WHERE lower(s->>'id') LIKE ? OR lower(s->>'name') LIKE ?
	)`, "%"+skill+"%", "%"+skill+"%")
	if err := fuzzyQ.Scan(ctx); err != nil {
		return nil, err
	}
	return fuzzy, nil
}

// Delete removes a registration
func (s *RegistryService) Delete(ctx context.Context, id string) error {
	res, err := s.db.NewDelete().Model((*models.AgentRegistration)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
