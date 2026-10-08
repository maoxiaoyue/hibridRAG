package controllers

import (
	"errors"
	"net/http"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_memory/app/models"
	"agent_memory/app/services"
	"agent_memory/internal/db"
	"agent_memory/internal/logger"
)

func memSvc() *services.MemoryService {
	return services.NewMemoryService(db.DB())
}

// ── Personal ─────────────────────────────────────────────────────────────────

// CreatePersonalMemory POST /api/memory/personal
func CreatePersonalMemory(ctx *context.Context) {
	var req models.CreatePersonalMemoryRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	m, err := memSvc().CreatePersonal(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("create personal memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "create failed"})
		return
	}
	ctx.JSON(http.StatusCreated, H{"success": true, "data": m})
}

// GetPersonalMemory GET /api/memory/personal/:id
func GetPersonalMemory(ctx *context.Context) {
	id := ctx.Param("id")
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing id"})
		return
	}
	m, err := memSvc().GetPersonal(ctx.Request.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "not found"})
			return
		}
		logger.Error("get personal memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "get failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": m})
}

// UpdatePersonalMemory PUT /api/memory/personal/:id
func UpdatePersonalMemory(ctx *context.Context) {
	id := ctx.Param("id")
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing id"})
		return
	}
	var req models.UpdatePersonalMemoryRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	m, err := memSvc().UpdatePersonal(ctx.Request.Context(), id, req)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "not found"})
			return
		}
		logger.Error("update personal memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "update failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": m})
}

// ── Procedural ────────────────────────────────────────────────────────────────

// CreateProceduralMemory POST /api/memory/procedural
func CreateProceduralMemory(ctx *context.Context) {
	var req models.CreateProceduralMemoryRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	m, err := memSvc().CreateProcedural(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("create procedural memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "create failed"})
		return
	}
	ctx.JSON(http.StatusCreated, H{"success": true, "data": m})
}

// GetProceduralMemory GET /api/memory/procedural/:id
func GetProceduralMemory(ctx *context.Context) {
	id := ctx.Param("id")
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing id"})
		return
	}
	m, err := memSvc().GetProcedural(ctx.Request.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "not found"})
			return
		}
		logger.Error("get procedural memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "get failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": m})
}

// ── Episodic ──────────────────────────────────────────────────────────────────

// CreateEpisodicMemory POST /api/memory/episodic
func CreateEpisodicMemory(ctx *context.Context) {
	var req models.CreateEpisodicMemoryRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	m, err := memSvc().CreateEpisodic(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("create episodic memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "create failed"})
		return
	}
	ctx.JSON(http.StatusCreated, H{"success": true, "data": m})
}

// GetEpisodicMemory GET /api/memory/episodic/:id
func GetEpisodicMemory(ctx *context.Context) {
	id := ctx.Param("id")
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing id"})
		return
	}
	m, err := memSvc().GetEpisodic(ctx.Request.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "not found"})
			return
		}
		logger.Error("get episodic memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "get failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": m})
}

// ── Tool ──────────────────────────────────────────────────────────────────────

// CreateToolMemory POST /api/memory/tool
func CreateToolMemory(ctx *context.Context) {
	var req models.CreateToolMemoryRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	m, err := memSvc().CreateTool(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("create tool memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "create failed"})
		return
	}
	ctx.JSON(http.StatusCreated, H{"success": true, "data": m})
}

// GetToolMemory GET /api/memory/tool/:id
func GetToolMemory(ctx *context.Context) {
	id := ctx.Param("id")
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing id"})
		return
	}
	m, err := memSvc().GetTool(ctx.Request.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "not found"})
			return
		}
		logger.Error("get tool memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "get failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": m})
}
