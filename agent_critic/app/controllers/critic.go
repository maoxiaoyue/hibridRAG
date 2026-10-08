package controllers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_critic/app/models"
	"agent_critic/app/services"
	"agent_critic/internal/logger"
)

var criticSvc *services.CriticService

// SetCriticService 由 main.go 注入已配置好的 CriticService
func SetCriticService(svc *services.CriticService) { criticSvc = svc }

// Evaluate POST /api/critic/evaluate
func Evaluate(ctx *context.Context) {
	var req models.EvaluateRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.AgentID == "" || req.Question == "" || req.Answer == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "agent_id, question, answer are required"})
		return
	}
	m, err := criticSvc.Evaluate(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("evaluate: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "evaluate failed"})
		return
	}
	ctx.JSON(http.StatusCreated, H{"success": true, "data": m})
}

// GetEvaluation GET /api/critic/evaluations/:id
func GetEvaluation(ctx *context.Context) {
	id := ctx.Param("id")
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing id"})
		return
	}
	m, err := criticSvc.Get(ctx.Request.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "not found"})
			return
		}
		logger.Error("get evaluation: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "get failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": m})
}

// ListEvaluations GET /api/critic/evaluations?session_id=&limit=
func ListEvaluations(ctx *context.Context) {
	sessionID := ctx.Query("session_id")
	if sessionID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing session_id"})
		return
	}
	limit, _ := strconv.Atoi(ctx.Query("limit"))
	list, err := criticSvc.ListBySession(ctx.Request.Context(), sessionID, limit)
	if err != nil {
		logger.Error("list evaluations: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "list failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": list})
}
