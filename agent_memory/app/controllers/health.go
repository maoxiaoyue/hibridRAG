package controllers

import (
	gocontext "context"
	"net/http"
	"time"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_memory/internal/cassandra"
	"agent_memory/internal/db"
)

// HealthCheck GET /health
//
// 回傳：
//   200 ok        — 全部後端正常
//   503 degraded  — 任一必要後端異常（PostgreSQL）
//
// Cassandra 異常時僅標記 down 但不影響整體狀態（長期記憶為可選功能）。
func HealthCheck(ctx *context.Context) {
	components := H{}
	overall := "ok"

	// PostgreSQL（必要）
	if d := db.Get(); d != nil && d.IsConnected() {
		components["postgres"] = "ok"
	} else {
		components["postgres"] = "down"
		overall = "degraded"
	}

	// Cassandra（可選）
	if c := cassandra.Get(); c != nil {
		cctx, cancel := gocontext.WithTimeout(ctx.Request.Context(), 2*time.Second)
		defer cancel()
		if err := c.Ping(cctx); err != nil {
			components["cassandra"] = "down: " + err.Error()
		} else {
			components["cassandra"] = "ok"
		}
	} else {
		components["cassandra"] = "disabled"
	}

	status := http.StatusOK
	if overall == "degraded" {
		status = http.StatusServiceUnavailable
	}
	ctx.JSON(status, H{
		"status":     overall,
		"service":    "agent_memory",
		"components": components,
	})
}
