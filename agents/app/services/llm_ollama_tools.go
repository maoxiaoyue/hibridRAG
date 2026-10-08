// llm_ollama_tools.go — Ollama tool calling 支援（/api/chat with tools[]）。
//
// Ollama 自 v0.3 起在 /api/chat 支援 OpenAI 形式的 tools：
//
//	request: {
//	  model, messages, stream:false,
//	  tools: [{type:"function", function:{name, description, parameters: <JSONSchema>}}],
//	  options: ...
//	}
//	response: {
//	  message: {
//	    role: "assistant", content: "",
//	    tool_calls: [{function: {name: "...", arguments: {...object...}}}]
//	  },
//	  done_reason: "stop" | ...
//	}
//
// 與其他 provider 的差異：
//   - tool_calls 沒有 id（Ollama 未生成）→ 我們本地用 "call_<n>" 合成
//   - arguments 是 object（非 string）；不需要二次 JSON parse
//
// 這支實作 ToolCallerLLM 介面，並能被 services.RunToolLoop 使用。
package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Compile-time guarantee：OllamaClient 滿足 ToolCallerLLM。
var _ ToolCallerLLM = (*OllamaClient)(nil)

// ── Wire types ──────────────────────────────────────────────────────────────

type ollamaToolFn struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Parameters  ToolSchema `json:"parameters"` // 直接用我們的 ToolSchema（JSON Schema 子集）
}

type ollamaTool struct {
	Type     string       `json:"type"` // 固定 "function"
	Function ollamaToolFn `json:"function"`
}

// ollamaMsg 是 Ollama 端 message 結構（多了 tool_calls / 可帶 name）。
type ollamaMsg struct {
	Role      string             `json:"role"`
	Content   string             `json:"content"`
	Name      string             `json:"name,omitempty"`       // role=tool 時的 tool name
	ToolCalls []ollamaToolCall   `json:"tool_calls,omitempty"` // role=assistant 時
}

type ollamaToolCall struct {
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type ollamaChatToolsReq struct {
	Model    string         `json:"model"`
	Messages []ollamaMsg    `json:"messages"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
	Tools    []ollamaTool   `json:"tools,omitempty"`
}

type ollamaChatToolsResp struct {
	Model           string    `json:"model"`
	Message         ollamaMsg `json:"message"`
	Done            bool      `json:"done"`
	DoneReason      string    `json:"done_reason,omitempty"`
	PromptEvalCount int       `json:"prompt_eval_count"`
	EvalCount       int       `json:"eval_count"`
}

// ── Implementation ──────────────────────────────────────────────────────────

// ChatWithTools 同 Chat，但帶 tools[]；回 ToolChatResponse（內含 ToolCalls）。
func (c *OllamaClient) ChatWithTools(
	ctx context.Context,
	req ChatRequest,
	tools []Tool,
) (*ToolChatResponse, error) {
	if c.cfg.URL == "" {
		return nil, fmt.Errorf("ollama: url not configured")
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("ollama: empty messages")
	}

	// 把通用 ChatMessage 轉成 ollamaMsg（單純 role/content；tool message 由 caller 預先放好）
	msgs := convertChatMessagesToOllama(req.Messages)

	// 把 Tool[] 轉成 Ollama tools[]
	otools := make([]ollamaTool, 0, len(tools))
	for _, t := range tools {
		otools = append(otools, ollamaTool{
			Type: "function",
			Function: ollamaToolFn{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		})
	}

	opts := map[string]any{}
	if req.Temperature > 0 {
		opts["temperature"] = req.Temperature
	}
	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = c.cfg.MaxTokens
	}
	if maxTok > 0 {
		opts["num_predict"] = maxTok
	}

	body, err := json.Marshal(ollamaChatToolsReq{
		Model:    c.cfg.Model,
		Messages: msgs,
		Stream:   false,
		Options:  opts,
		Tools:    otools,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal req: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.URL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var r ollamaChatToolsResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("ollama: decode resp: %w", err)
	}

	// 解析 tool_calls：Ollama 不給 id，自己合成 "call_<n>"
	var calls []ToolCall
	for i, tc := range r.Message.ToolCalls {
		calls = append(calls, ToolCall{
			ID:   fmt.Sprintf("call_%d", i+1),
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}

	finish := r.DoneReason
	if finish == "" {
		if len(calls) > 0 {
			finish = "tool_calls"
		} else if r.Done {
			finish = "stop"
		} else {
			finish = "incomplete"
		}
	}

	return &ToolChatResponse{
		ChatResponse: ChatResponse{
			Content:      r.Message.Content,
			FinishReason: finish,
			Model:        r.Model,
			Usage: TokenUsage{
				PromptTokens:     r.PromptEvalCount,
				CompletionTokens: r.EvalCount,
				TotalTokens:      r.PromptEvalCount + r.EvalCount,
			},
		},
		ToolCalls: calls,
	}, nil
}

// ── assistant tool_calls 的無損編碼 ───────────────────────────────────────────
//
// 通用 ChatMessage 只有 Role+Content，無法表達 assistant 的 tool_calls。
// 多輪 tool 對話若把「assistant 呼叫了哪些工具」用佔位文字（如 "[tool_calls x1]"）
// 塞進 Content，模型會忽略後續 tool result 甚至幻覺（實測 gemma4 會）。
//
// 因此把 tool_calls 以 marker + JSON 編碼進 assistant Content，
// 由 convertChatMessagesToOllama 還原成 Ollama 原生 assistant.tool_calls。
const assistantToolCallsPrefix = "[assistant_tool_calls]"

type encodedToolCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

// EncodeAssistantToolCalls 把一輪 assistant 的 tool_calls 編碼成可塞進 ChatMessage.Content
// 的字串；convertChatMessagesToOllama 會還原成原生 tool_calls。
func EncodeAssistantToolCalls(calls []ToolCall) string {
	enc := make([]encodedToolCall, 0, len(calls))
	for _, c := range calls {
		enc = append(enc, encodedToolCall{ID: c.ID, Name: c.Name, Args: c.Args})
	}
	b, err := json.Marshal(enc)
	if err != nil {
		return assistantToolCallsPrefix + "[]"
	}
	return assistantToolCallsPrefix + string(b)
}

// decodeAssistantToolCalls 還原 EncodeAssistantToolCalls 的內容。
func decodeAssistantToolCalls(content string) ([]ollamaToolCall, bool) {
	if !strings.HasPrefix(content, assistantToolCallsPrefix) {
		return nil, false
	}
	var enc []encodedToolCall
	if err := json.Unmarshal([]byte(content[len(assistantToolCallsPrefix):]), &enc); err != nil {
		return nil, false
	}
	out := make([]ollamaToolCall, 0, len(enc))
	for _, e := range enc {
		var tc ollamaToolCall
		tc.Function.Name = e.Name
		tc.Function.Arguments = e.Args
		out = append(out, tc)
	}
	return out, true
}

// convertChatMessagesToOllama 將通用 ChatMessage 轉為 Ollama 結構。
//
// 處理三種特殊形式：
//   - assistant + "[assistant_tool_calls]<json>" → 還原成原生 assistant.tool_calls（content 清空）
//   - RoleUser + "[tool_result name=X status=Y]\n<body>" → role=tool + name
//   - 其餘照搬 role/content
func convertChatMessagesToOllama(in []ChatMessage) []ollamaMsg {
	out := make([]ollamaMsg, 0, len(in))
	for _, m := range in {
		// assistant tool_calls 還原為原生結構（否則模型會忽略 tool result）。
		if m.Role == RoleAssistant {
			if tcs, ok := decodeAssistantToolCalls(m.Content); ok {
				out = append(out, ollamaMsg{Role: "assistant", Content: "", ToolCalls: tcs})
				continue
			}
		}

		role := string(m.Role)
		content := m.Content
		var name string

		// RunToolLoop / chat 用 RoleUser 包 "[tool_result name=X status=Y]\n<body>" 作為通用回注，
		// 對 Ollama 翻譯回 role=tool 比較貼合原生語意（也較不易混淆 user 語境）。
		if m.Role == RoleUser && strings.HasPrefix(content, "[tool_result name=") {
			if n, body, ok := parseToolResultPrefix(content); ok {
				role = "tool"
				name = n
				content = body
			}
		}
		out = append(out, ollamaMsg{
			Role:    role,
			Content: content,
			Name:    name,
		})
	}
	return out
}

// parseToolResultPrefix 解析 "[tool_result name=X status=Y]\n<body>"。
func parseToolResultPrefix(s string) (name string, body string, ok bool) {
	const head = "[tool_result name="
	if !strings.HasPrefix(s, head) {
		return "", "", false
	}
	rest := s[len(head):]
	closeIdx := strings.Index(rest, "]")
	if closeIdx < 0 {
		return "", "", false
	}
	header := rest[:closeIdx]
	body = rest[closeIdx+1:]
	body = strings.TrimPrefix(body, "\n")
	// header 形如 "X status=Y" 或 "X error=..."；取第一個 token 為 name
	if sp := strings.IndexAny(header, " "); sp >= 0 {
		name = header[:sp]
	} else {
		name = header
	}
	return name, body, true
}
