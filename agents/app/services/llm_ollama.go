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

// OllamaClient Ollama /api/chat 實作
type OllamaClient struct {
	cfg  OllamaConfig
	http *http.Client
}

// NewOllamaClient 建立 Ollama provider
func NewOllamaClient(cfg OllamaConfig) *OllamaClient {
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	if cfg.Model == "" {
		cfg.Model = "llama3"
	}
	return &OllamaClient{
		cfg:  cfg,
		http: httpClientWithTimeout(cfg.Timeout),
	}
}

func (c *OllamaClient) Provider() string { return "ollama" }

// ---- 送出請求 ----

type ollamaChatReq struct {
	Model    string         `json:"model"`
	Messages []ChatMessage  `json:"messages"` // ChatRole 會自動 marshal 成字串
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

type ollamaChatResp struct {
	Model           string `json:"model"`
	Message         struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason,omitempty"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

// Chat 同步 chat completion
func (c *OllamaClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if c.cfg.URL == "" {
		return nil, fmt.Errorf("ollama: url not configured")
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("ollama: empty messages")
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

	body, err := json.Marshal(ollamaChatReq{
		Model:    c.cfg.Model,
		Messages: req.Messages,
		Stream:   false,
		Options:  opts,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal req: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL+"/api/chat", bytes.NewReader(body))
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

	var r ollamaChatResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("ollama: decode resp: %w", err)
	}

	finish := r.DoneReason
	if finish == "" {
		if r.Done {
			finish = "stop"
		} else {
			finish = "incomplete"
		}
	}

	return &ChatResponse{
		Content:      r.Message.Content,
		FinishReason: finish,
		Model:        r.Model,
		Usage: TokenUsage{
			PromptTokens:     r.PromptEvalCount,
			CompletionTokens: r.EvalCount,
			TotalTokens:      r.PromptEvalCount + r.EvalCount,
		},
	}, nil
}
