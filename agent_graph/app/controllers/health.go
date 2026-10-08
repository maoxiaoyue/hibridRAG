package controllers

import (
	"net/http"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_graph/internal/db"
)

// HealthCheck GET /health
//
// 回傳：
//
//	200 ok        — PostgreSQL 正常
//	503 degraded  — PostgreSQL 異常
//
// 圖譜（Kùzu）/向量 index 是否可用不影響整體狀態（見 components.graph/vector），
// 圖譜功能是可選能力，跟 agent_memory 的 Cassandra 一樣「可選、不影響健康度」。
func HealthCheck(ctx *context.Context) {
	components := H{}
	overall := "ok"

	if d := db.Get(); d != nil && d.IsConnected() {
		components["postgres"] = "ok"
	} else {
		components["postgres"] = "down"
		overall = "degraded"
	}

	if graphSvc != nil && graphSvc.Graph != nil && graphSvc.Graph.Enabled() {
		components["graph"] = "ok"
	} else {
		components["graph"] = "disabled"
	}
	if graphSvc != nil && graphSvc.Vectors != nil && graphSvc.Vectors.Enabled() {
		components["vector"] = "ok"
	} else {
		components["vector"] = "disabled"
	}

	status := http.StatusOK
	if overall == "degraded" {
		status = http.StatusServiceUnavailable
	}
	ctx.JSON(status, H{
		"status":     overall,
		"service":    "agent_graph",
		"components": components,
	})
}
