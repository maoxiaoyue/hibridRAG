package controllers

import (
	"net/http"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_registry/internal/db"
)

func HealthCheck(ctx *context.Context) {
	components := H{}
	overall := "ok"

	if d := db.Get(); d != nil && d.IsConnected() {
		components["postgres"] = "ok"
	} else {
		components["postgres"] = "down"
		overall = "degraded"
	}

	status := http.StatusOK
	if overall == "degraded" {
		status = http.StatusServiceUnavailable
	}
	ctx.JSON(status, H{
		"status":     overall,
		"service":    "agent_registry",
		"components": components,
	})
}
