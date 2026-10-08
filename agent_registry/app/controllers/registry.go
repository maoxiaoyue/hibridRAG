package controllers

import (
	"errors"
	"net/http"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_registry/app/models"
	"agent_registry/app/services"
	"agent_registry/internal/db"
	"agent_registry/internal/logger"
)

func regSvc() *services.RegistryService {
	return services.NewRegistryService(db.DB())
}

// Register POST /registry/register
func Register(ctx *context.Context) {
	var req models.RegisterAgentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}

	reg, err := regSvc().Upsert(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("register agent: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": reg})
}

// GetAgent GET /registry/agents/:id
func GetAgent(ctx *context.Context) {
	id := ctx.Param("id")
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing id"})
		return
	}

	reg, err := regSvc().GetByID(ctx.Request.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "agent not found"})
			return
		}
		logger.Error("get agent: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "get failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": reg})
}

// SearchAgents GET /registry/search?skill=
func SearchAgents(ctx *context.Context) {
	skill := ctx.Query("skill")

	regs, err := regSvc().Search(ctx.Request.Context(), skill)
	if err != nil {
		logger.Error("search agents: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "search failed"})
		return
	}
	if skill != "" && distinctAgentCount(regs) > 1 {
		logger.Warning("ambiguous skill search %q matched %d distinct agents; routing to most recently seen: %s",
			skill, distinctAgentCount(regs), regs[0].ID)
	}
	ctx.JSON(http.StatusOK, H{
		"success": true,
		"data": models.SearchAgentsResponse{
			Agents: regs,
			Total:  len(regs),
		},
	})
}

// distinctAgentCount counts distinct agent ids among search results.
func distinctAgentCount(regs []models.AgentRegistration) int {
	seen := make(map[string]struct{}, len(regs))
	for _, r := range regs {
		seen[r.ID] = struct{}{}
	}
	return len(seen)
}

// Deregister DELETE /registry/agents/:id
func Deregister(ctx *context.Context) {
	id := ctx.Param("id")
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing id"})
		return
	}

	if err := regSvc().Delete(ctx.Request.Context(), id); err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "agent not found"})
			return
		}
		logger.Error("deregister agent: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "delete failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true})
}
