// Package embed 提供 BGE-M3 embedding client
//
// 後端支援：
//   - Ollama（預設；/api/embed，只回 dense）
//   - HuggingFace TEI（/embed，可配 SPLADE pooling 輸出 sparse）
//
// BGE-M3 原生產出 dense[1024] + sparse{token:weight} + multi-vector（ColBERT 風格）。
// Ollama 目前只暴露 dense，sparse 需另起 TEI 或自建服務；本 client 先把介面備好，
// 若後端無 sparse，Result.Sparse 為 nil，呼叫端以「只有 dense」處理。
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Backend embedding 服務後端類型
type Backend string

const (
	BackendOllama Backend = "ollama"
	BackendTEI    Backend = "tei"
)

// DefaultModel Ollama 上的 BGE-M3 模型 tag
const DefaultModel = "bge-m3"

// Result 單筆文本 embedding 輸出
//
//   - Dense：稠密向量（BGE-M3 為 1024 維）
//   - Sparse：lexical 權重（token/token_id → weight）；後端不支援時為 nil
type Result struct {
	Dense  []float32
	Sparse map[string]float32
}

// Dim 回傳 dense 向量維度（0 表示尚未取得）
func (r Result) Dim() int { return len(r.Dense) }

// HasSparse 是否帶有稀疏向量
func (r Result) HasSparse() bool { return len(r.Sparse) > 0 }

// Client BGE-M3 embedding client
//
// 設計：
//   - 後端可切換（Ollama / TEI）；呼叫端只看 Embed() 介面
//   - 批次輸入→批次輸出，順序對齊
//   - HTTP timeout / base URL / model 皆可調
type Client struct {
	BaseURL string  // e.g. "http://192.168.50.201:11434"
	Model   string  // e.g. "bge-m3"
	Backend Backend // ollama / tei
	HTTP    *http.Client
}

// Option Client 構造選項
type Option func(*Client)

func WithModel(m string) Option      { return func(c *Client) { c.Model = m } }
func WithBackend(b Backend) Option   { return func(c *Client) { c.Backend = b } }
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.HTTP = h }
}
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if c.HTTP == nil {
			c.HTTP = &http.Client{}
		}
		c.HTTP.Timeout = d
	}
}

// New 建立一個 Ollama 後端的 BGE-M3 client（最常見用法）
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   DefaultModel,
		Backend: BackendOllama,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Embed 對多筆文本取 embedding；回傳切片與 inputs 對齊
// 空輸入 → 回 nil, nil
func (c *Client) Embed(ctx context.Context, inputs []string) ([]Result, error) {
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

// EmbedOne 取單一文本 embedding（方便 helper）
func (c *Client) EmbedOne(ctx context.Context, input string) (Result, error) {
	out, err := c.Embed(ctx, []string{input})
	if err != nil {
		return Result{}, err
	}
	if len(out) == 0 {
		return Result{}, fmt.Errorf("embed: empty result")
	}
	return out[0], nil
}

// ---- Ollama ----
//
// POST /api/embed
//   req:  {"model":"bge-m3","input":["a","b"]}
//   resp: {"model":"bge-m3","embeddings":[[...],[...]]}
//
// Ollama 目前無原生 sparse 輸出；Sparse 欄位為 nil

type ollamaReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaResp struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

func (c *Client) embedOllama(ctx context.Context, inputs []string) ([]Result, error) {
	body, err := json.Marshal(ollamaReq{Model: c.Model, Input: inputs})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal ollama req: %w", err)
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
		return nil, fmt.Errorf("embed: decode ollama resp: %w", err)
	}
	if len(r.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("embed: ollama returned %d vectors, want %d", len(r.Embeddings), len(inputs))
	}

	out := make([]Result, len(inputs))
	for i, v := range r.Embeddings {
		out[i] = Result{Dense: v}
	}
	return out, nil
}

// ---- TEI (HuggingFace Text Embeddings Inference) ----
//
// POST /embed
//   req:  {"inputs":["a","b"]}
//   resp: [[...],[...]]
//
// POST /embed_sparse （若部署時啟用 SPLADE pooling）
//   resp: [[{"index":int,"value":float}, ...], ...]
//
// 若部署只開 /embed，Sparse 會是 nil；要 sparse 請在伺服端啟用 pooling=splade

type teiEmbedReq struct {
	Inputs []string `json:"inputs"`
}

type teiSparseEntry struct {
	Index int     `json:"index"`
	Value float32 `json:"value"`
}

func (c *Client) embedTEI(ctx context.Context, inputs []string) ([]Result, error) {
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

	out := make([]Result, len(inputs))
	for i, v := range denseVecs {
		out[i] = Result{Dense: v}
	}

	// 選配：嘗試取 sparse；失敗（例如 404）就忽略，不影響 dense 結果
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

func (c *Client) teiCall(ctx context.Context, path string, inputs []string) ([]byte, error) {
	body, err := json.Marshal(teiEmbedReq{Inputs: inputs})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal tei req: %w", err)
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

// ---- health / diagnostics ----

// Ping 做一次小探測，確認後端可用並回 dense 維度
func (c *Client) Ping(ctx context.Context) (int, error) {
	r, err := c.EmbedOne(ctx, "ping")
	if err != nil {
		return 0, err
	}
	return r.Dim(), nil
}
