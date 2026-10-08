// chat.go — 同步對話端點 POST /api/chat 的請求 / 回應 DTO。
//
// 與 gRPC Inbound（fire-and-forget）不同，此端點為**同步**：client 送訊息、
// 直接拿到 agent 回覆，且支援「client 端工具回合制」——當 LLM 決定呼叫一個由
// client 在本地執行的工具時，回應 status=tool_calls，client 跑完工具後帶
// continuation_id + tool_results 再次呼叫，server 接續推理直到 status=final。
//
// 注意：本檔不可 import app/services（會造成 import cycle；services 反向依賴 models）。
// 因此 client 工具的 JSON Schema 以 json.RawMessage 透傳，由 controller 端轉成
// services.ToolSchema。
package models

import "encoding/json"

// ChatTurnMessage 單輪對話訊息（role: user / assistant / system）。
type ChatTurnMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ClientToolSpec client 宣告自己可在本地執行的一個工具。
// Schema 為 JSON Schema（object，對應 services.ToolSchema）。
type ClientToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

// ClientToolResult client 在本地執行工具後回送的結果。
type ClientToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

// ChatTurnRequest POST /api/chat 請求。
//
// 兩種模式：
//   - 首次：填 Messages（+ 可選 ClientTools）。
//   - 接續（client 跑完工具）：填 ContinuationID + ToolResults。
type ChatTurnRequest struct {
	AgentID        string             `json:"agent_id"`
	SessionID      string             `json:"session_id,omitempty"`
	Messages       []ChatTurnMessage  `json:"messages,omitempty"`
	ClientTools    []ClientToolSpec   `json:"client_tools,omitempty"`
	ContinuationID string             `json:"continuation_id,omitempty"`
	ToolResults    []ClientToolResult `json:"tool_results,omitempty"`
	Temperature    float32            `json:"temperature,omitempty"`
}

// ClientToolCall server 要求 client 在本地執行的一個工具呼叫。
type ClientToolCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// ChatUsage token 使用量（對應 services.TokenUsage，避免 import cycle 而獨立定義）。
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatTurnResponse POST /api/chat 回應。
//
//	Status == "final"      → Content 為終局回覆。
//	Status == "tool_calls" → ToolCalls 需由 client 本地執行，帶 ContinuationID 回送結果。
type ChatTurnResponse struct {
	Status         string           `json:"status"`
	Content        string           `json:"content,omitempty"`
	SessionID      string           `json:"session_id"`
	ContinuationID string           `json:"continuation_id,omitempty"`
	ToolCalls      []ClientToolCall `json:"tool_calls,omitempty"`
	Usage          *ChatUsage       `json:"usage,omitempty"`
}
