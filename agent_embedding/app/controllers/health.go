package controllers

import (
	"context"
	"net/http"
	"time"

	hypctx "github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_embedding/internal/cache"
	"agent_embedding/internal/cassandra"
	"agent_embedding/internal/postgres"
)

// HealthCheck reports component liveness for embedding backend, Cassandra, Redis.
func HealthCheck(ctx *hypctx.Context) {
	components := H{}
	overall := "ok"

	probeCtx, cancel := context.WithTimeout(ctx.Request.Context(), 4*time.Second)
	defer cancel()

	// Cassandra
	if err := cassandra.Ping(probeCtx); err != nil {
		components["cassandra"] = "down: " + err.Error()
		overall = "degraded"
	} else {
		components["cassandra"] = "ok"
	}

	// PostgreSQL (optional, for hybrid search)
	if postgres.Get() != nil {
		if err := postgres.Ping(probeCtx); err != nil {
			components["postgres"] = "down: " + err.Error()
			overall = "degraded"
		} else {
			components["postgres"] = "ok"
		}
	} else {
		components["postgres"] = "disabled"
	}

	// Redis (optional)
	if rdb := cache.Get(); rdb != nil {
		if err := rdb.Ping(probeCtx).Err(); err != nil {
			components["redis"] = "down: " + err.Error()
			overall = "degraded"
		} else {
			components["redis"] = "ok"
		}
	} else {
		components["redis"] = "disabled"
	}

	// Embedding backend (BGE-M3)
	if svc != nil {
		if dim, err := svc.Embedder().Ping(probeCtx); err != nil {
			components["embedding"] = "down: " + err.Error()
			overall = "degraded"
		} else {
			components["embedding"] = H{"status": "ok", "dimension": dim, "model": svc.Embedder().Model}
		}
	} else {
		components["embedding"] = "uninitialized"
		overall = "degraded"
	}

	status := http.StatusOK
	if overall != "ok" {
		status = http.StatusServiceUnavailable
	}
	ctx.JSON(status, H{
		"status":     overall,
		"service":    "agent_embedding",
		"components": components,
	})
}
