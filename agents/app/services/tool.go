// Package services — tool.go: Tool Calling 框架核心介面。
//
// 設計目標：
//   - 對 LLM 端公開 OpenAI / Anthropic 通用的 tool schema（JSON Schema subset）
//   - 對實作端公開最小 Go interface（Name / Description / Schema / Call）
//   - Registry 統一管理（內建 tools + MCP-import tools 同層）
//   - 與 LLM provider 解耦：tool_loop.go 負責 LLM ↔ Tool 之間的訊息回注
//
// 對應 TODO.md #4 / TODO4 P1-1。
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// ── Schema ──────────────────────────────────────────────────────────────────

// ToolSchema 採 JSON Schema subset，相容 OpenAI tools[].function.parameters
// 與 Anthropic tools[].input_schema。
type ToolSchema struct {
	// Type 通常為 "object"
	Type string `json:"type"`
	// Properties 各參數定義；key=參數名，value 為 JSON Schema 子集
	Properties map[string]ToolProperty `json:"properties,omitempty"`
	// Required 必填參數名單
	Required []string `json:"required,omitempty"`
}

// ToolProperty 單一參數的型別 / 描述。
type ToolProperty struct {
	Type        string   `json:"type"`                  // string | number | integer | boolean | array | object
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
	Items       *ToolProperty `json:"items,omitempty"`  // array 元素型別
}

// NewObjectSchema 建立基本 object schema。
func NewObjectSchema() ToolSchema {
	return ToolSchema{Type: "object", Properties: map[string]ToolProperty{}}
}

// AddProperty 在 ToolSchema 上加一個欄位（流式 API）。
func (s ToolSchema) AddProperty(name string, p ToolProperty, required bool) ToolSchema {
	if s.Properties == nil {
		s.Properties = map[string]ToolProperty{}
	}
	s.Properties[name] = p
	if required {
		s.Required = append(s.Required, name)
	}
	return s
}

// ── Tool & Result ───────────────────────────────────────────────────────────

// Tool 是 agent 可呼叫的單一工具。
// 實作端通常是純函式包成 struct；MCP-imported tool 由 mcp_client.go 適配。
type Tool interface {
	Name() string
	Description() string
	Schema() ToolSchema
	// Call 執行工具；args 為 LLM tool_call 的 arguments（已 unmarshal）。
	// 回傳 ToolResult.Content 會以字串 / JSON 形式回注 LLM messages。
	Call(ctx context.Context, args map[string]any) (ToolResult, error)
}

// ToolResult 工具執行結果。Content 必填；其餘為觀測 / debug 用。
type ToolResult struct {
	// Content 給 LLM 看的回應內容（純文字或 JSON 字串均可）
	Content string
	// IsError true 表示工具邏輯失敗（非 transport 錯）；LLM 應能據此 reroute
	IsError bool
	// Meta 任意觀測欄位（latency_ms / source / ...），不會回注 LLM
	Meta map[string]any
}

// MarshalContent helper：把任意 value 序列化為 JSON 字串作為 Content。
func MarshalContent(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error":"marshal: %s"}`, err.Error())
	}
	return string(b)
}

// ── Registry ────────────────────────────────────────────────────────────────

// ToolRegistry 安全的 Tool 集合；允許多次 Register 但禁止同名覆蓋。
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewToolRegistry 建立空 registry。
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]Tool{}}
}

// Register 註冊 tool；同名已存在會回錯。
func (r *ToolRegistry) Register(t Tool) error {
	if t == nil {
		return fmt.Errorf("tool registry: nil tool")
	}
	name := t.Name()
	if name == "" {
		return fmt.Errorf("tool registry: empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool registry: %q already registered", name)
	}
	r.tools[name] = t
	return nil
}

// MustRegister 失敗即 panic（init 階段用）。
func (r *ToolRegistry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Get 取出 tool；找不到回 (nil, false)。
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List 回傳所有 tool（依 Name 字典序）。
func (r *ToolRegistry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// ── ToolCall：LLM 端傳回的呼叫請求結構 ───────────────────────────────────────

// ToolCall LLM 提出的單一 tool 呼叫；Args 已 unmarshal。
// 各 provider 的原生 tool_call/tool_use 在 LLM client 端轉成此型別。
type ToolCall struct {
	ID   string         // provider-specific call id（回注時要帶回）
	Name string         // tool 名稱
	Args map[string]any // 已 unmarshal 的參數
}

// ToolMessage 工具執行結果回注 LLM 的訊息（OpenAI role=tool / Anthropic tool_result）。
type ToolMessage struct {
	ToolCallID string // 對應 ToolCall.ID
	Name       string // tool 名稱（OpenAI 需要；Anthropic 看 ID）
	Content    string // ToolResult.Content
	IsError    bool
}
