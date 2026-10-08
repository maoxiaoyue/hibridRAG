// Package controllers provides ...
// @ai:owner ai-generated
package controllers

import (
	stdctx "context"
	"net/http"
	"runtime"
	"time"

	"agent/internal/cache"
	"agent/internal/database"
	"github.com/maoxiaoyue/hypgo/pkg/context"
)

// HealthCheck 健康檢查
func HealthCheck(ctx *context.Context) {
	// 檢查數據庫
	dbStatus := "healthy"
	if db := database.GetDB(); db != nil {
		if err := db.Ping(); err != nil {
			dbStatus = "unhealthy"
		}
	} else {
		dbStatus = "not connected"
	}

	// 檢查 Redis
	redisStatus := "healthy"
	if client := cache.GetClient(); client != nil {
		if err := client.Ping(stdctx.Background()).Err(); err != nil {
			redisStatus = "unhealthy"
		}
	} else {
		redisStatus = "not connected"
	}

	// 系統信息
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	ctx.JSON(http.StatusOK, H{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
		"protocol":  ctx.Protocol(),
		"services": H{
			"database": dbStatus,
			"redis":    redisStatus,
		},
		"system": H{
			"goroutines":   runtime.NumGoroutine(),
			"memory_alloc": m.Alloc / 1024 / 1024, // MB
			"memory_sys":   m.Sys / 1024 / 1024,   // MB
			"gc_runs":      m.NumGC,
		},
		"features": H{
			"http3":     ctx.IsHTTP3(),
			"http2":     ctx.IsHTTP2(),
			"websocket": false,
		},
	})
}

// Metrics Prometheus 指標
func Metrics(ctx *context.Context) {
	// TODO: 實現 Prometheus 指標
	ctx.String(http.StatusOK, "# HELP api_requests_total Total number of API requests\n")
}
