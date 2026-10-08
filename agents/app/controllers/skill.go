package controllers

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent/app/models"
	"agent/app/services"
	"agent/internal/logger"
)

// skill runtime 全域 loader / selector（由 main.go 注入）
var (
	skillMu       sync.RWMutex
	skillLoader   *services.SkillLoader
	skillSelector *services.SkillSelector
)

// SetSkillRuntime 由 main.go 注入實際 loader / selector
func SetSkillRuntime(loader *services.SkillLoader, selector *services.SkillSelector) {
	skillMu.Lock()
	defer skillMu.Unlock()
	skillLoader = loader
	skillSelector = selector
}

func getSkillLoader() *services.SkillLoader {
	skillMu.RLock()
	defer skillMu.RUnlock()
	return skillLoader
}

func getSkillSelector() *services.SkillSelector {
	skillMu.RLock()
	defer skillMu.RUnlock()
	return skillSelector
}

// LoadSkill POST /api/skill/load
func LoadSkill(ctx *context.Context) {
	var req models.LoadSkillRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.AgentID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "agent_id required"})
		return
	}

	loader := getSkillLoader()
	if loader == nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "skill loader not configured"})
		return
	}

	start := time.Now()
	pack, cached, reloaded, err := loader.Load(req.AgentID, req.Force)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "skill directory not found", "agent_id": req.AgentID})
			return
		}
		logger.Error("load skill: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "load failed", "details": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, H{
		"success": true,
		"data": models.LoadSkillResponse{
			Pack:      pack,
			Cached:    cached,
			Reloaded:  reloaded,
			ElapsedMs: time.Since(start).Milliseconds(),
		},
	})
}

// GetSkill GET /api/skill/:agent_id
func GetSkill(ctx *context.Context) {
	agentID := ctx.Param("agent_id")
	if agentID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "agent_id required"})
		return
	}

	loader := getSkillLoader()
	if loader == nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "skill loader not configured"})
		return
	}

	pack, err := loader.Get(agentID)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "skill not loaded", "agent_id": agentID})
			return
		}
		logger.Error("get skill: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "get failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": pack})
}

// SelectSkillReferences POST /api/skill/select
func SelectSkillReferences(ctx *context.Context) {
	var req models.SelectReferencesRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.AgentID == "" || req.Query == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "agent_id and query required"})
		return
	}

	selector := getSkillSelector()
	if selector == nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "skill selector not configured"})
		return
	}

	picked, err := selector.Select(req.AgentID, req.Query, req.MaxDocs, req.MaxTokens)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "agent not loaded", "agent_id": req.AgentID})
			return
		}
		logger.Error("select skill refs: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "select failed"})
		return
	}

	ctx.JSON(http.StatusOK, H{
		"success": true,
		"data":    models.SelectReferencesResponse{Picked: picked},
	})
}
