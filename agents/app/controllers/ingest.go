// Package controllers provides ...
// @ai:owner ai-generated
package controllers

import (
	"errors"
	"net/http"
	"sync"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent/app/models"
	"agent/app/services"
	"agent/internal/database"
	"agent/internal/logger"
)

// ingestRunner 全域 runner（由上層注入）。若為 nil，任務建立後仍保持 pending。
var (
	ingestRunnerMu sync.RWMutex
	ingestRunner   services.IngestRunner

	ingestBusMu      sync.RWMutex
	ingestBus        services.EventBusPublisher
	ingestBusStreams services.IngestStreams
)

// SetIngestRunner 由 main.go 注入實際執行器（例如串接 BGE-M3 TEI + Cassandra + PG）
func SetIngestRunner(r services.IngestRunner) {
	ingestRunnerMu.Lock()
	defer ingestRunnerMu.Unlock()
	ingestRunner = r
}

// SetIngestEventBus 由 main.go 注入 Event Bus publisher 與 stream 名稱
func SetIngestEventBus(bus services.EventBusPublisher, streams services.IngestStreams) {
	ingestBusMu.Lock()
	defer ingestBusMu.Unlock()
	ingestBus = bus
	ingestBusStreams = streams
}

func getIngestService() *services.IngestService {
	ingestRunnerMu.RLock()
	r := ingestRunner
	ingestRunnerMu.RUnlock()

	svc := services.NewIngestService(database.GetDB(), r)

	ingestBusMu.RLock()
	bus, streams := ingestBus, ingestBusStreams
	ingestBusMu.RUnlock()
	if bus != nil {
		svc = svc.WithEventBus(bus, streams)
	}
	return svc
}

// IngestSkill POST /api/ingest/skill
func IngestSkill(ctx *context.Context) {
	var req models.IngestSkillRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	job, err := getIngestService().EnqueueSkill(req)
	if err != nil {
		logger.Error("enqueue skill ingest: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "enqueue failed"})
		return
	}
	ctx.JSON(http.StatusAccepted, H{
		"success": true,
		"data":    models.IngestJobResponse{JobID: job.ID, Status: job.Status},
	})
}

// IngestReferences POST /api/ingest/references
func IngestReferences(ctx *context.Context) {
	var req models.IngestReferencesRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if len(req.Paths) == 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "paths required"})
		return
	}
	job, err := getIngestService().EnqueueReferences(req)
	if err != nil {
		logger.Error("enqueue references ingest: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "enqueue failed"})
		return
	}
	ctx.JSON(http.StatusAccepted, H{
		"success": true,
		"data":    models.IngestJobResponse{JobID: job.ID, Status: job.Status},
	})
}

// GetIngestJob GET /api/ingest/jobs/:id
func GetIngestJob(ctx *context.Context) {
	id := ctx.Param("id")
	if id == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "missing id"})
		return
	}
	job, err := getIngestService().GetJob(id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": "job not found"})
			return
		}
		logger.Error("get ingest job: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "get failed"})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": job})
}
