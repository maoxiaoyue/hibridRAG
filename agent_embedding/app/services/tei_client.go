// Package services contains the embedding-service business logic.
//
// tei_client.go — BGE-M3 embedding client supporting Ollama and HuggingFace TEI
// backends. Adds Redis-cached single-text embedding via the EmbedCached method.
package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Backend embedding service backend type.
type Backend string

const (
	BackendOllama Backend = "ollama"
	BackendTEI    Backend = "tei"
)

// EmbedResult one text → dense (and optionally sparse) embedding.
type EmbedResult struct {
	Dense  []float32
	Sparse map[string]float32
}

// Dim returns the dense vector dimension (0 if unset).
func (r EmbedResult) Dim() int { return len(r.Dense) }

// HasSparse reports whether the sparse map is populated.
func (r EmbedResult) HasSparse() bool { return len(r.Sparse) > 0 }

// EmbeddingClient BGE-M3 client.
type EmbeddingClient struct {
	BaseURL  string
	Model    string
	Backend  Backend
	HTTP     *http.Client
	Cache    *redis.Client
	CacheTTL time.Duration
}

// NewEmbeddingClient constructs a client. Pass cache=nil to disable caching.
func NewEmbeddingClient(baseURL, model string, backend Backend, timeout time.Duration, cache *redis.Client, cacheTTL time.Duration) *EmbeddingClient {
	if backend == "" {
		backend = BackendOllama
	}
	if model == "" {
		model = "bge-m3"
	}
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &EmbeddingClient{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Model:    model,
		Backend:  backend,
		HTTP:     &http.Client{Timeout: timeout},
		Cache:    cache,
		CacheTTL: cacheTTL,
	}
}

// Embed batches a list of texts → results in input order.
// Empty input returns nil, nil.
func (c *EmbeddingClient) Embed(ctx context.Context, inputs []string) ([]EmbedResult, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	switch c.Backend {
	case BackendTEI:
		return c.embedTEI(ctx, inputs)
	case BackendOllama:
		return c.embedOllama(ctx, inputs)
	default:
		return nil, fmt.Errorf("embed: unknown backend %q", c.Backend)
	}
}

// EmbedOne convenience helper for a single string.
func (c *EmbeddingClient) EmbedOne(ctx context.Context, input string) (EmbedResult, error) {
	out, err := c.Embed(ctx, []string{input})
	if err != nil {
		return EmbedResult{}, err
	}
	if len(out) == 0 {
		return EmbedResult{}, fmt.Errorf("embed: empty result")
	}
	return out[0], nil
}

// EmbedCached single-text embedding with Redis cache.
// Cache key: embed:<model>:<sha256(text)>
func (c *EmbeddingClient) EmbedCached(ctx context.Context, input string) (EmbedResult, bool, error) {
	if c.Cache != nil {
		if v, hit, err := c.cacheGet(ctx, input); err == nil && hit {
			return v, true, nil
		}
	}
	r, err := c.EmbedOne(ctx, input)
	if err != nil {
		return EmbedResult{}, false, err
	}
	if c.Cache != nil {
		_ = c.cachePut(ctx, input, r)
	}
	return r, false, nil
}

// Ping probes the backend with a tiny request and returns the dense dimension.
func (c *EmbeddingClient) Ping(ctx context.Context) (int, error) {
	r, err := c.EmbedOne(ctx, "ping")
	if err != nil {
		return 0, err
	}
	return r.Dim(), nil
}

// ── Ollama backend ──────────────────────────────────────────────────────────
//   POST /api/embed   {"model":"bge-m3","input":["a","b"]}
//   resp:             {"model":"bge-m3","embeddings":[[...],[...]]}

type ollamaReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaResp struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

func (c *EmbeddingClient) embedOllama(ctx context.Context, inputs []string) ([]EmbedResult, error) {
	body, err := json.Marshal(ollamaReq{Model: c.Model, Input: inputs})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: ollama call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embed: ollama status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var r ollamaResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("embed: decode: %w", err)
	}
	if len(r.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("embed: ollama returned %d vectors, want %d", len(r.Embeddings), len(inputs))
	}

	out := make([]EmbedResult, len(inputs))
	for i, v := range r.Embeddings {
		out[i] = EmbedResult{Dense: v}
	}
	return out, nil
}

// ── TEI backend ─────────────────────────────────────────────────────────────
//   POST /embed {"inputs":["a","b"]} → [[...], [...]]
//   POST /embed_sparse (optional) → [[{"index":int,"value":float}, ...], ...]

type teiReq struct {
	Inputs []string `json:"inputs"`
}

type teiSparseEntry struct {
	Index int     `json:"index"`
	Value float32 `json:"value"`
}

func (c *EmbeddingClient) embedTEI(ctx context.Context, inputs []string) ([]EmbedResult, error) {
	dense, err := c.teiCall(ctx, "/embed", inputs)
	if err != nil {
		return nil, err
	}
	var denseVecs [][]float32
	if err := json.Unmarshal(dense, &denseVecs); err != nil {
		return nil, fmt.Errorf("embed: decode tei dense: %w", err)
	}
	if len(denseVecs) != len(inputs) {
		return nil, fmt.Errorf("embed: tei dense returned %d, want %d", len(denseVecs), len(inputs))
	}
	out := make([]EmbedResult, len(inputs))
	for i, v := range denseVecs {
		out[i] = EmbedResult{Dense: v}
	}
	// Best-effort sparse fetch (silently ignored if unavailable).
	if sparse, err := c.teiCall(ctx, "/embed_sparse", inputs); err == nil {
		var sparseVecs [][]teiSparseEntry
		if err := json.Unmarshal(sparse, &sparseVecs); err == nil && len(sparseVecs) == len(inputs) {
			for i, entries := range sparseVecs {
				m := make(map[string]float32, len(entries))
				for _, e := range entries {
					m[fmt.Sprintf("%d", e.Index)] = e.Value
				}
				out[i].Sparse = m
			}
		}
	}
	return out, nil
}

func (c *EmbeddingClient) teiCall(ctx context.Context, path string, inputs []string) ([]byte, error) {
	body, err := json.Marshal(teiReq{Inputs: inputs})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: tei call %s: %w", path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: tei %s status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// ── Redis cache helpers ─────────────────────────────────────────────────────

func (c *EmbeddingClient) cacheKey(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "embed:" + c.Model + ":" + hex.EncodeToString(sum[:])
}

func (c *EmbeddingClient) cacheGet(ctx context.Context, text string) (EmbedResult, bool, error) {
	raw, err := c.Cache.Get(ctx, c.cacheKey(text)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return EmbedResult{}, false, nil
		}
		return EmbedResult{}, false, err
	}
	var r EmbedResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return EmbedResult{}, false, err
	}
	return r, true, nil
}

func (c *EmbeddingClient) cachePut(ctx context.Context, text string, r EmbedResult) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return c.Cache.Set(ctx, c.cacheKey(text), raw, c.CacheTTL).Err()
}
