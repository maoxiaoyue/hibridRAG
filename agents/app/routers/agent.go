package routers

import (
	"agent/app/models"

	"github.com/maoxiaoyue/hypgo/pkg/schema"
)

// RegisterAgent 註冊 agent gRPC 路由的 schema metadata，
// 供 hyp context / contract testing / manifest 使用。
func RegisterAgent() {
	// Inbound: 上游 API 推訊息進入
	schema.Global().Register(schema.Route{
		Protocol: "grpc",
		Command:  "agent.AgentService/Inbound",
		Summary:  "上游 API 推送訊息進入本 agent；立即回 ack，內部非同步處理",
		Tags:     []string{"agent", "inbound"},
		Input:    models.InboundReq{},
		Output:   models.InboundAck{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "accepted"},
			400: {Description: "invalid request (empty messages / missing payload)"},
			502: {Description: "downstream unreachable"},
			504: {Description: "downstream timeout"},
		},
	})

	// Outbound: agent 推送訊息給下游的規格 (不是 server 暴露的 RPC，是 agent 作為 client 呼叫對端 Inbound 時的輸出規格
	schema.Global().Register(schema.Route{
		Protocol: "grpc",
		Command:  "agent.AgentService/Outbound",
		Summary:  "本 agent 推送訊息給下游 agent 的輸出規格 (client-side contract)",
		Tags:     []string{"agent", "outbound"},
		Input:    models.OutboundMsg{},
		Output:   models.InboundAck{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "downstream accepted"},
			502: {Description: "downstream unreachable"},
			504: {Description: "downstream timeout"},
		},
	})
}
