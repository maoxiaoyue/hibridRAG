package controllers

import (
	"errors"
	"net/http"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_memory/app/models"
	"agent_memory/app/services"
	"agent_memory/internal/logger"
)

// ltmService 由 main.go 在 Cassandra 連線成功後注入；nil 表示功能停用
var ltmService *services.LongTermMemoryService

// SetLongTermMemoryService 注入 service 實例
func SetLongTermMemoryService(s *services.LongTermMemoryService) {
	ltmService = s
}

// GetLongTermMemoryService 給其他 controller（如 context.go）共用
func GetLongTermMemoryService() *services.LongTermMemoryService {
	return ltmService
}

// 共用守門：確保 Cassandra 已啟用
func requireLTM(ctx *context.Context) (*services.LongTermMemoryService, bool) {
	if ltmService == nil {
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable,
			H{"error": "long-term memory disabled (cassandra not connected)"})
		return nil, false
	}
	return ltmService, true
}

// CreateLongTermMemory POST /api/memory/long_term
func CreateLongTermMemory(ctx *context.Context) {
	s, ok := requireLTM(ctx)
	if !ok {
		return
	}
	var req models.CreateLongTermMemoryRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	m, err := s.Insert(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("create long_term_memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "create failed", "details": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, H{"success": true, "data": m})
}

// GetLongTermMemory GET /api/memory/long_term/:id
func GetLongTermMemory(ctx *context.Context) {
	s, ok := requireLTM(ctx)
	if !ok {
		return
	}
	id := ctx.Param("id")
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing id"})
		return
	}
	m, err := s.Get(ctx.Request.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "not found"})
			return
		}
		logger.Error("get long_term_memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "get failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": m})
}

// SearchLongTermMemory POST /api/memory/long_term/search
func SearchLongTermMemory(ctx *context.Context) {
	s, ok := requireLTM(ctx)
	if !ok {
		return
	}
	var req models.SearchLongTermMemoryRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	hits, err := s.SearchANN(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("search long_term_memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "search failed", "details": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": hits, "count": len(hits)})
}

// DeleteLongTermMemory DELETE /api/memory/long_term/:id
func DeleteLongTermMemory(ctx *context.Context) {
	s, ok := requireLTM(ctx)
	if !ok {
		return
	}
	id := ctx.Param("id")
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing id"})
		return
	}
	if err := s.Delete(ctx.Request.Context(), id); err != nil {
		logger.Error("delete long_term_memory: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "delete failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true})
}
