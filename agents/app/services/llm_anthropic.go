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

// AnthropicClient Anthropic /v1/messages 實作
//
// 注意：Anthropic API 把 system prompt 放在 top-level 的 "system" 欄位，
// 而非 messages 陣列；本 client 自動把 ChatMessage 中的 system role 抽出。
type AnthropicClient struct {
	cfg  APIConfig
	http *http.Client
}

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com"
	anthropicAPIVersion     = "2023-06-01"
)

// NewAnthropicClient 建立 Anthropic provider
func NewAnthropicClient(cfg APIConfig) *AnthropicClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = anthropicDefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &AnthropicClient{
		cfg:  cfg,
		http: httpClientWithTimeout(cfg.Timeout),
	}
}

func (c *AnthropicClient) Provider() string { return "anthropic" }

// ---- 送出請求 ----

type anthropicMsgReq struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	System      string        `json:"system,omitempty"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float32       `json:"temperature,omitempty"`
}

type anthropicMsgResp struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Role       string `json:"role"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat 同步 chat completion
func (c *AnthropicClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if c.cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic: api_key not configured")
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("anthropic: empty messages")
	}

	system, rest := extractSystemAndRest(req.Messages)
	if len(rest) == 0 {
		return nil, fmt.Errorf("anthropic: no user/assistant messages after system extraction")
	}

	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = c.cfg.MaxTokens
	}
	if maxTok == 0 {
		maxTok = 1024 // Anthropic 必填欄位，給合理預設
	}

	body, err := json.Marshal(anthropicMsgReq{
		Model:       c.cfg.Model,
		MaxTokens:   maxTok,
		System:      system,
		Messages:    rest,
		Temperature: req.Temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal req: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var r anthropicMsgResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("anthropic: decode resp: %w", err)
	}
	if r.Error != nil {
		return nil, fmt.Errorf("anthropic: %s: %s", r.Error.Type, r.Error.Message)
	}

	// 串接所有 text content（Anthropic 可能回多個 content block）
	var contentBuf strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			contentBuf.WriteString(c.Text)
		}
	}

	return &ChatResponse{
		Content:      contentBuf.String(),
		FinishReason: r.StopReason,
		Model:        r.Model,
		Usage: TokenUsage{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      r.Usage.InputTokens + r.Usage.OutputTokens,
		},
	}, nil
}
