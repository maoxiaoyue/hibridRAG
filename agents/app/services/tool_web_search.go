// tool_web_search.go — Web search tool with pluggable provider.
//
// 不綁定特定供應商；提供 WebSearchProvider 介面，內建一個 generic HTTP impl
// （HTTPSearchProvider）能對接 Brave Search / SerpAPI / Tavily 等 REST API
// — 只要它們吐 {results:[{title,url,snippet}]} 形式（或自訂 mapper）。
//
// 開箱即用：給定 endpoint + api_key + result mapping，立即可掛上 ToolRegistry。
// 未設定時 WebSearchTool.Call 直接回 disabled，方便 dev 模式下不需 key。
package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WebSearchResult 單一搜尋結果。
type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// WebSearchProvider 將 query → []WebSearchResult。
type WebSearchProvider interface {
	Search(ctx context.Context, query string, topK int) ([]WebSearchResult, error)
}

// ── WebSearchTool ───────────────────────────────────────────────────────────

// WebSearchTool 包裝 WebSearchProvider 成 Tool；provider==nil 時回 disabled。
type WebSearchTool struct {
	Provider WebSearchProvider
}

func (t *WebSearchTool) Name() string { return "web_search" }
func (t *WebSearchTool) Description() string {
	return "Search the public web. Returns title, URL, snippet. Use for fresh facts not in knowledge base."
}
func (t *WebSearchTool) Schema() ToolSchema {
	return NewObjectSchema().
		AddProperty("query", ToolProperty{Type: "string", Description: "search query"}, true).
		AddProperty("top_k", ToolProperty{Type: "integer", Description: "max results; default 5", Default: 5}, false)
}
func (t *WebSearchTool) Call(ctx context.Context, args map[string]any) (ToolResult, error) {
	if t.Provider == nil {
		return ToolResult{IsError: true, Content: `{"error":"web_search disabled: no provider configured"}`}, nil
	}
	query, _ := args["query"].(string)
	if query == "" {
		return ToolResult{IsError: true, Content: `{"error":"query required"}`}, nil
	}
	topK := 5
	if v, ok := args["top_k"].(float64); ok && v > 0 {
		topK = int(v)
	}
	out, err := t.Provider.Search(ctx, query, topK)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
	}
	return ToolResult{Content: MarshalContent(map[string]any{
		"query":   query,
		"results": out,
		"count":   len(out),
	})}, nil
}

// ── HTTPSearchProvider（generic GET / POST + JSONPath-lite extract） ───────

// HTTPSearchConfig 描述如何呼叫一個 search REST API。
//
// 例：Brave Search
//   Endpoint: "https://api.search.brave.com/res/v1/web/search"
//   Method:   "GET"
//   QueryKey: "q"
//   Headers:  {"X-Subscription-Token": "${BRAVE_KEY}"}
//   ResultsPath: "web.results"  // 點分隔
//   TitleKey/URLKey/SnippetKey: "title" / "url" / "description"
type HTTPSearchConfig struct {
	Endpoint    string            `yaml:"endpoint"`
	Method      string            `yaml:"method"`        // GET | POST
	QueryKey    string            `yaml:"query_key"`     // GET → query param；POST → JSON body key
	Headers     map[string]string `yaml:"headers"`
	ResultsPath string            `yaml:"results_path"`  // 點分隔（e.g. "web.results"）
	TitleKey    string            `yaml:"title_key"`     // 預設 "title"
	URLKey      string            `yaml:"url_key"`       // 預設 "url"
	SnippetKey  string            `yaml:"snippet_key"`   // 預設 "snippet"
	TimeoutSec  int               `yaml:"timeout_sec"`   // 0 → 10
}

// HTTPSearchProvider 用 HTTPSearchConfig 跑出 WebSearchProvider 行為。
type HTTPSearchProvider struct {
	cfg HTTPSearchConfig
	hc  *http.Client
}

// NewHTTPSearchProvider 建立。timeout<=0 → 10s。
func NewHTTPSearchProvider(cfg HTTPSearchConfig) *HTTPSearchProvider {
	if cfg.Method == "" {
		cfg.Method = "GET"
	}
	if cfg.QueryKey == "" {
		cfg.QueryKey = "q"
	}
	if cfg.TitleKey == "" {
		cfg.TitleKey = "title"
	}
	if cfg.URLKey == "" {
		cfg.URLKey = "url"
	}
	if cfg.SnippetKey == "" {
		cfg.SnippetKey = "snippet"
	}
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPSearchProvider{cfg: cfg, hc: &http.Client{Timeout: timeout}}
}

// Search 執行 HTTP 呼叫 → JSON 解析 → ResultsPath 抽出 → 映射欄位。
func (p *HTTPSearchProvider) Search(ctx context.Context, query string, topK int) ([]WebSearchResult, error) {
	if p.cfg.Endpoint == "" {
		return nil, fmt.Errorf("http search: endpoint not configured")
	}
	var (
		req *http.Request
		err error
	)
	switch p.cfg.Method {
	case "POST":
		body, _ := json.Marshal(map[string]any{p.cfg.QueryKey: query, "count": topK})
		req, err = http.NewRequestWithContext(ctx, "POST", p.cfg.Endpoint, bytes.NewReader(body))
		if req != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	default: // GET
		url := fmt.Sprintf("%s?%s=%s&count=%d", p.cfg.Endpoint, p.cfg.QueryKey, urlEncode(query), topK)
		req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
	}
	if err != nil {
		return nil, err
	}
	for k, v := range p.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("http search: HTTP %d: %s", resp.StatusCode, string(b))
	}
	var root map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		return nil, fmt.Errorf("http search: decode: %w", err)
	}
	arr := digSlice(root, p.cfg.ResultsPath)
	out := make([]WebSearchResult, 0, len(arr))
	for i, raw := range arr {
		if i >= topK {
			break
		}
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, WebSearchResult{
			Title:   strField(m, p.cfg.TitleKey),
			URL:     strField(m, p.cfg.URLKey),
			Snippet: strField(m, p.cfg.SnippetKey),
		})
	}
	return out, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func strField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// digSlice 從 root 依 "a.b.c" 取出 []any；找不到回 nil。
func digSlice(root map[string]any, path string) []any {
	cur := any(root)
	if path == "" {
		if arr, ok := cur.([]any); ok {
			return arr
		}
		return nil
	}
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '.' {
			key := path[start:i]
			m, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = m[key]
			start = i + 1
		}
	}
	if arr, ok := cur.([]any); ok {
		return arr
	}
	return nil
}

// urlEncode 最小可用的 URL query 編碼（避免新增 import 循環）。
func urlEncode(s string) string {
	const hex = "0123456789ABCDEF"
	buf := make([]byte, 0, len(s)*3)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ':
			buf = append(buf, '+')
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~':
			buf = append(buf, c)
		default:
			buf = append(buf, '%', hex[c>>4], hex[c&15])
		}
	}
	return string(buf)
}
