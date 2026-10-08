package controllers

import (
	"net/http"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_memory/app/models"
	"agent_memory/app/services"
	"agent_memory/internal/db"
	"agent_memory/internal/logger"
)

// BuildContext POST /api/memory/context
//
// 一次組裝完整 RAG context（替代 agent 端自行拼裝）：
//   - PersonalMemory anchor (1 筆)
//   - ProceduralMemory top-N（預設 3，依 created_at DESC）
//   - EpisodicMemory top-N（預設 2，依 occurred_at DESC，可指定 session_id）
//   - LongTermMemory ANN top-N（預設 5；需 query_vector 1024 dims）
//
// 任一子查詢失敗都不阻斷整體回應；缺什麼欄位就是空陣列 / nil。
func BuildContext(ctx *context.Context) {
	var req models.BuildContextRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.AgentID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing agent_id"})
		return
	}

	mem := services.NewMemoryService(db.DB())
	svc := services.NewContextService(mem, GetLongTermMemoryService())

	resp, err := svc.Build(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("build context: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "build failed", "details": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": resp})
}
