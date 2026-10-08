// tool_context.go — 把 request 層的 persona agentID 帶進 Tool.Call 的 ctx。
//
// 背景：內建工具（memory_read / memory_write / rag_query）註冊時綁的 AgentID
// 是 config.yaml 的 agent.id（process 全域），但 persona 是 request 級的
// （gRPC Inbound / /api/chat 都以 payload agent_id 選 persona）——不注入的話，
// persona B 對話中 LLM 呼叫 memory_write 會寫進 agent.id 本體的記憶。
//
// 解析優先序（見 resolveToolAgentID）：
//  1. args.agent_id  — LLM 明確指定的跨 agent 覆寫（既有行為，保留）
//  2. ctx 注入值     — request 的 persona agentID（本檔新增）
//  3. tool 靜態欄位  — config agent.id（未注入時的 fallback，行為同改動前）
package services

import "context"

type toolAgentIDKey struct{}

// WithToolAgentID 把 request 的 persona agentID 放進 ctx。
// 呼叫端：agent_grpc.go process() 與 agent_chat.go runChatLoop()。
func WithToolAgentID(ctx context.Context, agentID string) context.Context {
	if agentID == "" {
		return ctx
	}
	return context.WithValue(ctx, toolAgentIDKey{}, agentID)
}

// ToolAgentIDFrom 取出 ctx 中的 request-scoped agentID；未注入時回空字串。
func ToolAgentIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(toolAgentIDKey{}).(string)
	return v
}

// resolveToolAgentID 依「args 覆寫 > ctx 注入 > 靜態 fallback」解析工具實際
// 操作的 agentID。內建工具的 Call 一律經此解析，不直接讀自己的 AgentID 欄位。
func resolveToolAgentID(ctx context.Context, args map[string]any, fallback string) string {
	if v, ok := args["agent_id"].(string); ok && v != "" {
		return v
	}
	if v := ToolAgentIDFrom(ctx); v != "" {
		return v
	}
	return fallback
}
