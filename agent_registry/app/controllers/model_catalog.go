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

// modelCatalogHost 由 setupRoutes 在啟動時以 config 的 ollama.host 設定。
var modelCatalogHost string

// SetModelCatalogHost 由 commands.setupRoutes 呼叫，注入 Ollama host。
func SetModelCatalogHost(h string) { modelCatalogHost = h }

func modelSvc() *services.ModelCatalogService {
	return services.NewModelCatalogService(db.DB(), modelCatalogHost)
}

// SyncModels POST /registry/models/sync — 從 Ollama 拉取並 upsert。
func SyncModels(ctx *context.Context) {
	svc := modelSvc()
	list, err := svc.Sync(ctx.Request.Context())
	if err != nil {
		logger.Error("sync models from %s: %v", svc.Host(), err)
		ctx.AbortWithStatusJSON(http.StatusBadGateway, H{"error": "ollama sync failed", "details": err.Error(), "host": svc.Host()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": models.SyncModelsResponse{
		Host:   svc.Host(),
		Synced: len(list),
		Models: list,
	}})
}

// ListModels GET /registry/models — 列出已記錄模型。
func ListModels(ctx *context.Context) {
	list, err := modelSvc().List(ctx.Request.Context())
	if err != nil {
		logger.Error("list models: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "list failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": models.ListModelsResponse{
		Models: list,
		Total:  len(list),
	}})
}

// GetModel GET /registry/models/:name — 查單一模型。
func GetModel(ctx *context.Context) {
	name := ctx.Param("name")
	if name == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing name"})
		return
	}
	m, err := modelSvc().Get(ctx.Request.Context(), name)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "model not found"})
			return
		}
		logger.Error("get model: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "get failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": m})
}
