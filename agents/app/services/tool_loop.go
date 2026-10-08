// tool_loop.go — LLM ↔ Tool 回注迴圈。
//
// 流程（OpenAI / Anthropic 通用形狀）：
//
//	while not done && iter < max:
//	    resp = llm.ChatWithTools(messages, tools)
//	    if resp.ToolCalls empty:
//	        return resp.Content
//	    for each call:
//	        result = registry.Get(call.Name).Call(ctx, call.Args)
//	        messages += assistant tool_calls + tool result
//	    iter++
//
// 抽象 ToolCallerLLM 介面（LLMClient 進階版）：
//   - Ollama / OpenAI / Anthropic provider 各自把 tools[] 轉為 native 形式
//   - 解析 tool_calls 也由 provider 端負責
//
// 注意：本檔不重複 LLM provider 的 wire format；只跑迴圈邏輯與訊息累加。
// 目前 LLMClient 介面（Chat）尚未支援 tools；待各 provider 實作 ChatWithTools 後即可接上。
package services

import (
	"context"
	"fmt"
)

// ToolCallerLLM 是 LLMClient 的 tool-aware 擴充。
// 各 provider client（Ollama/OpenAI/Anthropic）實作此介面後即可加入 tool loop。
type ToolCallerLLM interface {
	LLMClient
	// ChatWithTools 同 Chat，但能附帶 tools 並回傳 tool_calls（若 LLM 決定呼叫）。
	// Content 為空 + ToolCalls 非空 → 等待工具結果再次呼叫。
	// Content 非空 + ToolCalls 空 → 終局回應。
	ChatWithTools(ctx context.Context, req ChatRequest, tools []Tool) (*ToolChatResponse, error)
}

// ToolChatResponse ChatWithTools 的回應。
type ToolChatResponse struct {
	ChatResponse
	// ToolCalls LLM 要求呼叫的工具（可能多個並行）；空 = 終局回應
	ToolCalls []ToolCall
}

// ToolLoopOptions 控制工具迴圈行為。
type ToolLoopOptions struct {
	MaxIterations int // 0 → 8
	// ToolTimeoutSec 單一 tool call 的執行 timeout；0 → 30s
	ToolTimeoutSec int
}

// ToolTrace 觀測用：每輪的工具呼叫摘要。
type ToolTrace struct {
	Iter int
	Name string
	OK   bool
	Err  string
}

// RunToolLoop 在 llm 和 registry 之間跑「LLM → tool_call → 執行 → 結果回注 → 再 LLM」迴圈。
//
// messages 會被原地累積（assistant tool_calls + tool results）；caller 可 reuse 結果繼續多輪對話。
// 回傳：終局 assistant Content、最終 messages、本輪所有 ToolTrace。
func RunToolLoop(
	ctx context.Context,
	llm ToolCallerLLM,
	registry *ToolRegistry,
	messages []ChatMessage,
	tools []Tool,
	opts ToolLoopOptions,
) (finalContent string, finalMsgs []ChatMessage, traces []ToolTrace, err error) {
	if llm == nil {
		return "", messages, nil, fmt.Errorf("tool loop: llm required")
	}
	if registry == nil {
		registry = NewToolRegistry()
	}
	maxIter := opts.MaxIterations
	if maxIter <= 0 {
		maxIter = 8
	}

	for iter := 0; iter < maxIter; iter++ {
		resp, err := llm.ChatWithTools(ctx, ChatRequest{Messages: messages}, tools)
		if err != nil {
			return "", messages, traces, fmt.Errorf("llm chat iter=%d: %w", iter, err)
		}

		// 終局
		if len(resp.ToolCalls) == 0 {
			messages = append(messages, ChatMessage{Role: RoleAssistant, Content: resp.Content})
			return resp.Content, messages, traces, nil
		}

		// 把 assistant turn（含 tool_calls）先加進去。我們 ChatMessage 沒 tool_call 欄位，
		// 用 Content 留 placeholder（provider client 端拼 native message 時應記住 tool_calls）。
		assistantPlaceholder := resp.Content
		if assistantPlaceholder == "" {
			assistantPlaceholder = fmt.Sprintf("[tool_calls x%d]", len(resp.ToolCalls))
		}
		messages = append(messages, ChatMessage{Role: RoleAssistant, Content: assistantPlaceholder})

		// 執行每個 tool；結果以 RoleUser「[tool_result name=...]」附加（最通用、不依賴 provider 特殊角色）
		// provider-native 整合可在 ChatWithTools 內把這些再轉成 role=tool / tool_result content block。
		for _, call := range resp.ToolCalls {
			tool, ok := registry.Get(call.Name)
			if !ok {
				msg := fmt.Sprintf(`[tool_result name=%s error=unknown_tool]`, call.Name)
				messages = append(messages, ChatMessage{Role: RoleUser, Content: msg})
				traces = append(traces, ToolTrace{Iter: iter, Name: call.Name, OK: false, Err: "unknown_tool"})
				continue
			}
			toolCtx := ctx
			if opts.ToolTimeoutSec > 0 {
				var cancel context.CancelFunc
				toolCtx, cancel = withTimeoutSec(ctx, opts.ToolTimeoutSec)
				defer cancel()
			}
			res, terr := tool.Call(toolCtx, call.Args)
			if terr != nil {
				msg := fmt.Sprintf(`[tool_result name=%s error=%q]`, call.Name, terr.Error())
				messages = append(messages, ChatMessage{Role: RoleUser, Content: msg})
				traces = append(traces, ToolTrace{Iter: iter, Name: call.Name, OK: false, Err: terr.Error()})
				continue
			}
			tag := "ok"
			if res.IsError {
				tag = "error"
			}
			msg := fmt.Sprintf("[tool_result name=%s status=%s]\n%s", call.Name, tag, res.Content)
			messages = append(messages, ChatMessage{Role: RoleUser, Content: msg})
			traces = append(traces, ToolTrace{Iter: iter, Name: call.Name, OK: !res.IsError})
		}
	}

	return "", messages, traces, fmt.Errorf("tool loop: exceeded max iterations (%d)", maxIter)
}

// withTimeoutSec 包 ctx 加 second 級 timeout。
func withTimeoutSec(parent context.Context, sec int) (context.Context, context.CancelFunc) {
	return contextWithSeconds(parent, sec)
}
