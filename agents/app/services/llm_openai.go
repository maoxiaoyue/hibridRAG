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

// OpenAIClient OpenAI /v1/chat/completions 實作
//
// 同時支援 OpenAI-compatible 自架伺服器（vLLM / LM Studio / LocalAI 等）：
// 把 BaseURL 指向自架端點即可，協定一致。
type OpenAIClient struct {
	cfg      APIConfig
	provider string // "openai" | "custom"
	http     *http.Client
}

const openAIDefaultBaseURL = "https://api.openai.com"

// NewOpenAIClient 建立 OpenAI（含 OpenAI-compatible）provider
func NewOpenAIClient(cfg APIConfig) *OpenAIClient {
	provider := cfg.Provider
	if provider == "" {
		provider = "openai"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = openAIDefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &OpenAIClient{
		cfg:      cfg,
		provider: provider,
		http:     httpClientWithTimeout(cfg.Timeout),
	}
}

func (c *OpenAIClient) Provider() string { return c.provider }

// ---- 送出請求 ----

type openAIChatReq struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float32       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
}

type openAIChatResp struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat 同步 chat completion
func (c *OpenAIClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// custom provider 可不必設 api_key（自架可能不要），但 openai 必填
	if c.provider == "openai" && c.cfg.APIKey == "" {
		return nil, fmt.Errorf("openai: api_key not configured")
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("openai: empty messages")
	}

	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = c.cfg.MaxTokens
	}

	body, err := json.Marshal(openAIChatReq{
		Model:       c.cfg.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		MaxTokens:   maxTok,
		Stream:      false,
	})
	if err != nil {
		return nil, fmt.Errorf("openai: marshal req: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: call: %w", c.provider, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: status %d: %s", c.provider, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var r openAIChatResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("%s: decode resp: %w", c.provider, err)
	}
	if r.Error != nil {
		return nil, fmt.Errorf("%s: %s: %s", c.provider, r.Error.Type, r.Error.Message)
	}
	if len(r.Choices) == 0 {
		return nil, fmt.Errorf("%s: empty choices", c.provider)
	}

	choice := r.Choices[0]
	return &ChatResponse{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		Model:        r.Model,
		Usage: TokenUsage{
			PromptTokens:     r.Usage.PromptTokens,
			CompletionTokens: r.Usage.CompletionTokens,
			TotalTokens:      r.Usage.TotalTokens,
		},
	}, nil
}
