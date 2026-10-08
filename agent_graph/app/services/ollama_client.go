// ollama_client.go — direct Ollama HTTP client (no agent_embedding hop),
// matching the agents/ SemanticMemory convention: this service only needs
// "embed a string" and "extract structured entities/relations from a string",
// not agent_embedding's full chunking/RRF hybrid-search machinery.
package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"agent_graph/app/config"
)

// ── Embedding ────────────────────────────────────────────────────────────────

// EmbedClient talks to Ollama's /api/embed for BGE-M3 dense vectors.
type EmbedClient struct {
	baseURL string
	model   string
	http    *http.Client
}

func NewEmbedClient(cfg config.OllamaEmbeddingConfig) *EmbedClient {
	return &EmbedClient{
		baseURL: strings.TrimRight(cfg.URL, "/"),
		model:   cfg.Model,
		http:    &http.Client{Timeout: cfg.Timeout()},
	}
}

type ollamaEmbedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResp struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed returns dense vectors aligned with inputs (Ollama /api/embed: one
// call, batched input, order-preserving output).
func (c *EmbedClient) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(ollamaEmbedReq{Model: c.model, Input: inputs})
	if err != nil {
		return nil, fmt.Errorf("marshal embed req: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embed status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var r ollamaEmbedResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decode embed resp: %w", err)
	}
	if len(r.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("embed returned %d vectors, want %d", len(r.Embeddings), len(inputs))
	}
	return r.Embeddings, nil
}

// EmbedOne is a convenience wrapper for a single text.
func (c *EmbedClient) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	out, err := c.Embed(ctx, []string{input})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// ── Chat (entity/relation extraction) ───────────────────────────────────────

// ChatClient talks to Ollama's /api/chat for the one task this service needs
// an LLM for: pulling structured entities/relations out of markdown prose
// that WikiLinks alone don't capture.
type ChatClient struct {
	baseURL     string
	model       string
	temperature float32
	http        *http.Client
}

func NewChatClient(cfg config.OllamaChatConfig) *ChatClient {
	return &ChatClient{
		baseURL:     strings.TrimRight(cfg.URL, "/"),
		model:       cfg.Model,
		temperature: cfg.Temperature,
		http:        &http.Client{Timeout: cfg.Timeout()},
	}
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatReq struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Format   string              `json:"format,omitempty"` // "json" — best-effort structured output
	Options  map[string]any      `json:"options,omitempty"`
}

type ollamaChatResp struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

// ExtractedEntity / ExtractedRelation are the shape the extraction prompt
// asks the model to emit; Confidence in [0,1] drives the propose-vs-merge
// decision in the sync worker (see internal/sync).
type ExtractedEntity struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

type ExtractedRelation struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Type       string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

type ExtractionResult struct {
	Entities  []ExtractedEntity   `json:"entities"`
	Relations []ExtractedRelation `json:"relations"`
}

const extractionSystemPrompt = `You extract a knowledge graph from a note. Given the note's title and body:
1. List the entities (people, concepts, projects, tools) it substantively discusses — not the title itself.
2. List relations between those entities and the note's title entity.
Respond with ONLY a JSON object of this exact shape, no prose:
{"entities":[{"name":"...","category":"...","description":"one sentence"}],"relations":[{"source":"...","target":"...","type":"relates_to","confidence":0.0}]}
confidence is your certainty this relation is real and correctly named, from 0 (guess) to 1 (explicitly stated in the text).`

// ExtractEntitiesRelations asks the model to pull structured entities/
// relations out of one note's body. Returns a zero-value result (not an
// error) if the model's output isn't valid JSON — a malformed extraction
// should degrade to "no LLM-derived edges this sync", not fail the sync.
func (c *ChatClient) ExtractEntitiesRelations(ctx context.Context, title, body string) (ExtractionResult, error) {
	var zero ExtractionResult
	if c.baseURL == "" || c.model == "" {
		return zero, fmt.Errorf("ollama chat: not configured")
	}

	userMsg := fmt.Sprintf("Title: %s\n\nBody:\n%s", title, truncateRunes(body, 4000))
	reqBody, err := json.Marshal(ollamaChatReq{
		Model: c.model,
		Messages: []ollamaChatMessage{
			{Role: "system", Content: extractionSystemPrompt},
			{Role: "user", Content: userMsg},
		},
		Stream:  false,
		Format:  "json",
		Options: map[string]any{"temperature": c.temperature},
	})
	if err != nil {
		return zero, fmt.Errorf("marshal chat req: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(reqBody))
	if err != nil {
		return zero, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return zero, fmt.Errorf("chat call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return zero, fmt.Errorf("chat status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var r ollamaChatResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return zero, fmt.Errorf("decode chat resp: %w", err)
	}

	var result ExtractionResult
	if err := json.Unmarshal([]byte(r.Message.Content), &result); err != nil {
		return zero, nil // malformed model output — best-effort, not fatal
	}
	return result, nil
}

// ── Classification（knowledge-digest 管線用）─────────────────────────────────

const classifySystemPrompt = `You file a note into a single knowledge category.
Given the note's title and body, choose the ONE category that best fits.
Prefer an existing category from the provided list when one clearly applies;
only invent a new short category (1-3 words, lowercase, hyphen-separated) when none fit.
Respond with ONLY a JSON object of this exact shape, no prose:
{"category":"..."}`

// ClassifyText 為一段文字挑一個分類標籤（knowledge-digest 管線把 temp/ 筆記
// 搬進 vault/<category>/ 時用）。existing 是目前已有的分類清單（引導模型盡量歸到
// 既有類別而非一直開新類）。回傳 kebab-case 分類；模型輸出無效時回 "misc"（不報錯，
// best-effort——分類失敗不該讓整條管線卡住）。
func (c *ChatClient) ClassifyText(ctx context.Context, title, body string, existing []string) (string, error) {
	if c.baseURL == "" || c.model == "" {
		return "", fmt.Errorf("ollama chat: not configured")
	}
	userMsg := fmt.Sprintf("Existing categories: %s\n\nTitle: %s\n\nBody:\n%s",
		strings.Join(existing, ", "), title, truncateRunes(body, 4000))
	reqBody, err := json.Marshal(ollamaChatReq{
		Model: c.model,
		Messages: []ollamaChatMessage{
			{Role: "system", Content: classifySystemPrompt},
			{Role: "user", Content: userMsg},
		},
		Stream:  false,
		Format:  "json",
		Options: map[string]any{"temperature": c.temperature},
	})
	if err != nil {
		return "", fmt.Errorf("marshal classify req: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("classify call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("classify status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var r ollamaChatResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("decode classify resp: %w", err)
	}
	var out struct {
		Category string `json:"category"`
	}
	if err := json.Unmarshal([]byte(r.Message.Content), &out); err != nil {
		return "misc", nil // 模型輸出非 JSON → 歸到 misc（best-effort）
	}
	cat := SanitizeCategory(out.Category)
	if cat == "" {
		cat = "misc"
	}
	return cat, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
