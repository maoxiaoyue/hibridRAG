// Package services — LLM Client（chat completion 統一介面）
//
// 對應架構圖 ⚡ LLM Generate 步驟。支援的 provider：
//
//	Ollama    本地或內網 Ollama 服務（/api/chat）
//	Anthropic Claude 系列（/v1/messages）
//	OpenAI    含 OpenAI 官方與 OpenAI-compatible 自架（/v1/chat/completions）
//
// 設定檔：config/llm.yaml（mode + provider 區塊）
// 載入方式：
//
//	cfg, err := services.LoadLLMConfig("config/llm.yaml")
//	client, err := services.NewLLMClient(*cfg)
//	resp, err := client.Chat(ctx, services.ChatRequest{Messages: ...})
//
// MVP 範圍：non-streaming chat。streaming 留給 StreamingLLMClient（Phase 2）。
package services

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// ---- 訊息 / 請求 / 回應類型 ----

// ChatRole 訊息角色（OpenAI / Anthropic / Ollama 共用）
type ChatRole string

const (
	RoleSystem    ChatRole = "system"
	RoleUser      ChatRole = "user"
	RoleAssistant ChatRole = "assistant"
)

// ChatMessage 一輪對話訊息
type ChatMessage struct {
	Role    ChatRole `json:"role"`
	Content string   `json:"content"`
}

// ChatRequest chat completion 請求
type ChatRequest struct {
	Messages    []ChatMessage // 含 system / user / assistant 多輪
	Temperature float32       // 0.0~2.0；0 = deterministic
	MaxTokens   int           // 0 = 用 provider 預設
	// Stream 預留欄位，MVP 不實作；非 nil StreamingLLMClient 才會處理
	Stream bool
}

// TokenUsage 單次 inference 的 token 計數
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse chat completion 回應
type ChatResponse struct {
	Content      string     // assistant 訊息內容
	FinishReason string     // "stop" | "length" | "tool_calls" | provider-specific
	Model        string     // 實際使用的 model（provider 回填）
	Usage        TokenUsage // token 使用量
}

// ---- 介面 ----

// LLMClient chat completion 統一介面
type LLMClient interface {
	// Chat 同步 chat completion；ctx 取消則中斷請求
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// Provider 回傳 provider 識別字串（"ollama" / "anthropic" / "openai" / "custom"）
	Provider() string
}

// ---- Config 結構 ----

// LLMConfig 對應 config/llm.yaml
type LLMConfig struct {
	Mode   string        `yaml:"mode"` // none | ollama | api | rag
	Ollama *OllamaConfig `yaml:"ollama,omitempty"`
	API    *APIConfig    `yaml:"api,omitempty"`
}

// OllamaConfig mode=ollama 時的設定
//
// Model 與 Role 二擇一：
//   - Model 非空 → 直接使用
//   - Model 空、Role 非空 → 由 ModelRegistry 依用途解析
//   - 兩者皆空 → 套用 NewOllamaClient 的硬編碼 fallback（"llama3"）
type OllamaConfig struct {
	URL       string `yaml:"url"`
	Model     string `yaml:"model"`
	Role      string `yaml:"role"`       // 模型用途（chat / chat_quality / tool_calling …）；對應 ModelRole
	Timeout   int    `yaml:"timeout"`    // 秒；0 → 60
	MaxTokens int    `yaml:"max_tokens"` // 0 → 不傳（用 server 預設）
}

// APIConfig mode=api 時的設定
type APIConfig struct {
	Provider  string `yaml:"provider"`            // openai | anthropic | gemini | custom
	Model     string `yaml:"model"`               // e.g. claude-haiku-4-5
	APIKey    string `yaml:"api_key"`             // 支援 ${ENV_VAR}
	BaseURL   string `yaml:"base_url,omitempty"`  // custom provider 必填，其他選填覆寫
	Timeout   int    `yaml:"timeout"`             // 秒；0 → 60
	MaxTokens int    `yaml:"max_tokens"`          // 0 → 不傳
}

// ---- Factory ----

// NewLLMClient 依 LLMConfig.Mode 建立對應 provider 的 client
func NewLLMClient(cfg LLMConfig) (LLMClient, error) {
	switch cfg.Mode {
	case "ollama":
		if cfg.Ollama == nil {
			return nil, fmt.Errorf("llm: mode=ollama but ollama section missing")
		}
		return NewOllamaClient(*cfg.Ollama), nil

	case "api":
		if cfg.API == nil {
			return nil, fmt.Errorf("llm: mode=api but api section missing")
		}
		return newAPIClient(*cfg.API)

	case "none", "":
		return nil, fmt.Errorf("llm: mode=%q — no client configured", cfg.Mode)

	case "rag":
		return nil, fmt.Errorf("llm: mode=rag not supported by NewLLMClient (use RAG pipeline)")

	default:
		return nil, fmt.Errorf("llm: unsupported mode %q", cfg.Mode)
	}
}

// newAPIClient 依 APIConfig.Provider 派發
func newAPIClient(cfg APIConfig) (LLMClient, error) {
	switch cfg.Provider {
	case "anthropic":
		return NewAnthropicClient(cfg), nil
	case "openai":
		return NewOpenAIClient(cfg), nil
	case "custom":
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("llm: provider=custom requires base_url")
		}
		return NewOpenAIClient(cfg), nil // custom 走 OpenAI-compatible 協定
	case "gemini":
		return nil, fmt.Errorf("llm: provider=gemini not yet supported (use ADK gemini.NewModel directly)")
	default:
		return nil, fmt.Errorf("llm: unknown provider %q", cfg.Provider)
	}
}

// ---- 內部共用 helper ----

// httpClientWithTimeout 建立帶 timeout 的 http.Client；timeoutSec<=0 → 60s
func httpClientWithTimeout(timeoutSec int) *http.Client {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	return &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
}

// extractSystemAndRest 把 ChatMessage 切成 system 字串 + 其餘訊息
// （Anthropic API 把 system 放在 top-level 而非 messages 陣列）
func extractSystemAndRest(msgs []ChatMessage) (system string, rest []ChatMessage) {
	for _, m := range msgs {
		if m.Role == RoleSystem {
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
			continue
		}
		rest = append(rest, m)
	}
	return system, rest
}
