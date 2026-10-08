// chat_status.go — 對外回報某 session「目前處理到哪個 Agent 階段」（供 client 每秒輪詢）。
//
//	GET /api/chat/status/:session_id → { stage, active }
//
// 由 gateway 的 ClientGateway.ChatStatus 經內部 HTTP 呼叫；client 不直接打（只連 gateway）。
package controllers

import (
	"net/http"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent/app/services"
)

var chatStatusTracker *services.ChatStatusTracker

// SetChatStatusTracker 由 main.go 注入（與 AgentServer 共用同一顆 tracker）。
func SetChatStatusTracker(t *services.ChatStatusTracker) { chatStatusTracker = t }

// ChatStatus GET /api/chat/status/:session_id
func ChatStatus(ctx *context.Context) {
	sessionID := ctx.Param("session_id")
	stage, active := chatStatusTracker.Get(sessionID)
	ctx.JSON(http.StatusOK, H{"success": true, "data": H{"stage": stage, "active": active}})
}
