// monitor.go — Ollama/GPU 使用狀況查詢端點。
//
//	GET /api/monitor/gpu            最近一次快照（背景每 10 分鐘、推理前都會更新）
//	GET /api/monitor/gpu?refresh=1  強制立即重新取樣
package controllers

import (
	"net/http"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent/app/services"
)

var gpuMonitor *services.GPUMonitor

// SetGPUMonitor 由 main.go 注入；nil = 監控未啟用（端點回 503）。
func SetGPUMonitor(m *services.GPUMonitor) { gpuMonitor = m }

// GPUStatus GET /api/monitor/gpu
func GPUStatus(ctx *context.Context) {
	if gpuMonitor == nil {
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable,
			H{"error": "gpu monitor not enabled (monitoring.gpu in config.yaml)"})
		return
	}
	snap := gpuMonitor.Last()
	if ctx.Query("refresh") == "1" || snap == nil {
		snap = gpuMonitor.Sample(ctx.Request.Context())
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": snap})
}
