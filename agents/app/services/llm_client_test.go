package services_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent/app/services"
)

// ---- helpers ----

// captureRequest 啟動 httptest server，記錄請求 body 與 headers，回傳指定 response
func captureRequest(t *testing.T, status int, respBody string) (server *httptest.Server, captured *capturedReq) {
	t.Helper()
	captured = &capturedReq{Headers: http.Header{}}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Method = r.Method
		captured.URL = r.URL.String()
		captured.Headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		captured.Body = string(body)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(server.Close)
	return server, captured
}

type capturedReq struct {
	Method  string
	URL     string
	Headers http.Header
	Body    string
}

// ---- Ollama ----

func TestOllamaClient_Chat(t *testing.T) {
	respBody := `{
		"model": "llama3",
		"message": {"role": "assistant", "content": "Hello!"},
		"done": true,
		"done_reason": "stop",
		"prompt_eval_count": 12,
		"eval_count": 8
	}`
	srv, captured := captureRequest(t, 200, respBody)

	client := services.NewOllamaClient(services.OllamaConfig{
		URL:       srv.URL,
		Model:     "llama3",
		Timeout:   5,
		MaxTokens: 256,
	})

	resp, err := client.Chat(context.Background(), services.ChatRequest{
		Messages: []services.ChatMessage{
			{Role: services.RoleSystem, Content: "You are helpful."},
			{Role: services.RoleUser, Content: "Hi"},
		},
		Temperature: 0.7,
	})
	if err != nil {
		t.Fatalf("Chat err: %v", err)
	}

	// 回應內容
	if resp.Content != "Hello!" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q", resp.FinishReason)
	}
	if resp.Usage.PromptTokens != 12 || resp.Usage.CompletionTokens != 8 || resp.Usage.TotalTokens != 20 {
		t.Errorf("Usage = %+v", resp.Usage)
	}

	// 請求內容驗證
	if !strings.Contains(captured.URL, "/api/chat") {
		t.Errorf("URL = %s", captured.URL)
	}
	var sentReq map[string]any
	if err := json.Unmarshal([]byte(captured.Body), &sentReq); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	if sentReq["model"] != "llama3" {
		t.Errorf("model in req = %v", sentReq["model"])
	}
	if sentReq["stream"] != false {
		t.Errorf("stream should be false, got %v", sentReq["stream"])
	}
	opts, _ := sentReq["options"].(map[string]any)
	if opts["num_predict"] != float64(256) {
		t.Errorf("options.num_predict = %v", opts["num_predict"])
	}
}

func TestOllamaClient_Provider(t *testing.T) {
	c := services.NewOllamaClient(services.OllamaConfig{URL: "http://x"})
	if c.Provider() != "ollama" {
		t.Errorf("Provider = %s", c.Provider())
	}
}

func TestOllamaClient_HTTPError(t *testing.T) {
	srv, _ := captureRequest(t, 500, `internal error`)
	c := services.NewOllamaClient(services.OllamaConfig{URL: srv.URL, Model: "x"})
	_, err := c.Chat(context.Background(), services.ChatRequest{
		Messages: []services.ChatMessage{{Role: services.RoleUser, Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Errorf("expected status 500 error, got %v", err)
	}
}

// ---- Anthropic ----

func TestAnthropicClient_Chat(t *testing.T) {
	respBody := `{
		"id": "msg_123",
		"type": "message",
		"role": "assistant",
		"model": "claude-haiku-4-5",
		"stop_reason": "end_turn",
		"content": [{"type": "text", "text": "Hi there"}],
		"usage": {"input_tokens": 15, "output_tokens": 5}
	}`
	srv, captured := captureRequest(t, 200, respBody)

	client := services.NewAnthropicClient(services.APIConfig{
		Provider:  "anthropic",
		Model:     "claude-haiku-4-5",
		APIKey:    "sk-test",
		BaseURL:   srv.URL,
		Timeout:   5,
		MaxTokens: 200,
	})

	resp, err := client.Chat(context.Background(), services.ChatRequest{
		Messages: []services.ChatMessage{
			{Role: services.RoleSystem, Content: "You are an agent."},
			{Role: services.RoleUser, Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("Chat err: %v", err)
	}

	if resp.Content != "Hi there" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.FinishReason != "end_turn" {
		t.Errorf("FinishReason = %q", resp.FinishReason)
	}
	if resp.Usage.PromptTokens != 15 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 20 {
		t.Errorf("Usage = %+v", resp.Usage)
	}

	// Headers
	if captured.Headers.Get("x-api-key") != "sk-test" {
		t.Errorf("x-api-key header missing/wrong: %v", captured.Headers.Get("x-api-key"))
	}
	if captured.Headers.Get("anthropic-version") == "" {
		t.Errorf("anthropic-version header missing")
	}

	// System extraction：messages 中應該只剩 user
	var sentReq map[string]any
	if err := json.Unmarshal([]byte(captured.Body), &sentReq); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	if sentReq["system"] != "You are an agent." {
		t.Errorf("system field = %v", sentReq["system"])
	}
	msgs, _ := sentReq["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("messages should have 1 entry (user only), got %d", len(msgs))
	}
}

func TestAnthropicClient_NoAPIKey(t *testing.T) {
	c := services.NewAnthropicClient(services.APIConfig{Model: "claude"})
	_, err := c.Chat(context.Background(), services.ChatRequest{
		Messages: []services.ChatMessage{{Role: services.RoleUser, Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Errorf("expected api_key error, got %v", err)
	}
}

// ---- OpenAI ----

func TestOpenAIClient_Chat(t *testing.T) {
	respBody := `{
		"id": "chatcmpl-1",
		"model": "gpt-4o-mini",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "OK"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}
	}`
	srv, captured := captureRequest(t, 200, respBody)

	client := services.NewOpenAIClient(services.APIConfig{
		Provider: "openai",
		Model:    "gpt-4o-mini",
		APIKey:   "sk-test",
		BaseURL:  srv.URL,
	})

	resp, err := client.Chat(context.Background(), services.ChatRequest{
		Messages: []services.ChatMessage{{Role: services.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Chat err: %v", err)
	}
	if resp.Content != "OK" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q", resp.FinishReason)
	}
	if resp.Usage.TotalTokens != 12 {
		t.Errorf("TotalTokens = %d", resp.Usage.TotalTokens)
	}

	if !strings.HasPrefix(captured.Headers.Get("Authorization"), "Bearer sk-test") {
		t.Errorf("Authorization header wrong: %v", captured.Headers.Get("Authorization"))
	}
	if !strings.Contains(captured.URL, "/v1/chat/completions") {
		t.Errorf("URL = %s", captured.URL)
	}
}

func TestOpenAIClient_CustomNoAPIKey(t *testing.T) {
	// custom provider 可不必設 api_key（自架）
	srv, captured := captureRequest(t, 200, `{
		"choices": [{"message": {"content": "ok"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
	}`)
	c := services.NewOpenAIClient(services.APIConfig{
		Provider: "custom",
		Model:    "local",
		BaseURL:  srv.URL,
	})
	_, err := c.Chat(context.Background(), services.ChatRequest{
		Messages: []services.ChatMessage{{Role: services.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("custom should not require api_key: %v", err)
	}
	if captured.Headers.Get("Authorization") != "" {
		t.Errorf("custom without api_key should not set Authorization, got %v", captured.Headers.Get("Authorization"))
	}
}

// ---- Factory ----

func TestNewLLMClient_Ollama(t *testing.T) {
	c, err := services.NewLLMClient(services.LLMConfig{
		Mode:   "ollama",
		Ollama: &services.OllamaConfig{URL: "http://x", Model: "llama3"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if c.Provider() != "ollama" {
		t.Errorf("Provider = %s", c.Provider())
	}
}

func TestNewLLMClient_Anthropic(t *testing.T) {
	c, err := services.NewLLMClient(services.LLMConfig{
		Mode: "api",
		API:  &services.APIConfig{Provider: "anthropic", Model: "claude-haiku-4-5", APIKey: "sk-test"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if c.Provider() != "anthropic" {
		t.Errorf("Provider = %s", c.Provider())
	}
}

func TestNewLLMClient_OpenAI(t *testing.T) {
	c, err := services.NewLLMClient(services.LLMConfig{
		Mode: "api",
		API:  &services.APIConfig{Provider: "openai", Model: "gpt-4o-mini", APIKey: "sk-test"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if c.Provider() != "openai" {
		t.Errorf("Provider = %s", c.Provider())
	}
}

func TestNewLLMClient_CustomMissingBaseURL(t *testing.T) {
	_, err := services.NewLLMClient(services.LLMConfig{
		Mode: "api",
		API:  &services.APIConfig{Provider: "custom", Model: "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("expected base_url required error, got %v", err)
	}
}

func TestNewLLMClient_GeminiNotSupported(t *testing.T) {
	_, err := services.NewLLMClient(services.LLMConfig{
		Mode: "api",
		API:  &services.APIConfig{Provider: "gemini", Model: "gemini-2.0-flash"},
	})
	if err == nil || !strings.Contains(err.Error(), "gemini") {
		t.Errorf("expected gemini not supported error, got %v", err)
	}
}

func TestNewLLMClient_NoneMode(t *testing.T) {
	_, err := services.NewLLMClient(services.LLMConfig{Mode: "none"})
	if err == nil {
		t.Error("expected error for mode=none")
	}
}

// ---- Loader ----

func TestParseLLMConfig_Ollama(t *testing.T) {
	yaml := `
mode: ollama
ollama:
  url: "http://192.168.50.104:11434"
  model: "gemma4:e4b"
  timeout: 30
  max_tokens: 65536
`
	cfg, err := services.ParseLLMConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Mode != "ollama" || cfg.Ollama.Model != "gemma4:e4b" || cfg.Ollama.MaxTokens != 65536 {
		t.Errorf("parsed wrong: %+v", cfg)
	}
}

func TestParseLLMConfig_EnvExpansion(t *testing.T) {
	t.Setenv("MY_TEST_KEY", "sk-secret-123")
	yaml := `
mode: api
api:
  provider: anthropic
  model: claude-haiku-4-5
  api_key: ${MY_TEST_KEY}
`
	cfg, err := services.ParseLLMConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.API.APIKey != "sk-secret-123" {
		t.Errorf("APIKey = %q, want sk-secret-123", cfg.API.APIKey)
	}
}

func TestParseLLMConfig_EnvWithDefault(t *testing.T) {
	// env 未設 → 用 default
	os.Unsetenv("MAYBE_OLLAMA_URL")
	yaml := `
mode: ollama
ollama:
  url: "${MAYBE_OLLAMA_URL:-http://localhost:11434}"
  model: "${MAYBE_OLLAMA_MODEL:-llama3}"
`
	cfg, err := services.ParseLLMConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Ollama.URL != "http://localhost:11434" {
		t.Errorf("expected default URL, got %q", cfg.Ollama.URL)
	}
	if cfg.Ollama.Model != "llama3" {
		t.Errorf("expected default model, got %q", cfg.Ollama.Model)
	}

	// env 有設 → 蓋過 default
	t.Setenv("MAYBE_OLLAMA_URL", "http://override:9999")
	cfg2, _ := services.ParseLLMConfig([]byte(yaml))
	if cfg2.Ollama.URL != "http://override:9999" {
		t.Errorf("env should override default, got %q", cfg2.Ollama.URL)
	}
}

func TestParseLLMConfig_MissingEnvKept(t *testing.T) {
	yaml := `
mode: api
api:
  provider: openai
  model: gpt-4o-mini
  api_key: ${DEFINITELY_NOT_SET_KKKK}
`
	cfg, err := services.ParseLLMConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 找不到 env → 保留原字面
	if cfg.API.APIKey != "${DEFINITELY_NOT_SET_KKKK}" {
		t.Errorf("expected literal placeholder, got %q", cfg.API.APIKey)
	}
}

func TestParseLLMConfig_ValidationError(t *testing.T) {
	yaml := `
mode: api
api:
  provider: custom
  model: x
`
	_, err := services.ParseLLMConfig([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("expected base_url error, got %v", err)
	}
}

func TestLoadLLMConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "llm.yaml")
	yaml := "mode: ollama\nollama:\n  url: http://localhost:11434\n  model: llama3\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := services.LoadLLMConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Ollama.URL != "http://localhost:11434" {
		t.Errorf("got %+v", cfg)
	}
}
