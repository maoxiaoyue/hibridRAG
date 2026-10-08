// modeladmin_client.go — 「Models」分頁的後端連線（內網 / dev_090 專用）。
//
// 兩個後端：
//   registryURL → agent_registry（:8500）：模型目錄（含 text/image/audio modalities）
//   agentsURL   → agents 內網 REST（:18080）：各 persona 的模型設定（讀/寫 ROLE.md）
//
// 目前 agentsURL 由 main.go 傳空字串（persona 設定端點只在本機測試有效，外網伺服器
// 碰不到 agents 內網 REST）→ HasAgents()=false，Models 分頁只顯示模型目錄。persona 方法
// 保留以便日後（例如改走 gateway）重新啟用。RegistryBaseURL 留空則不建「Models」分頁。
package services

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"rag_admin/app/models"
)

// ModelAdminClient 連線到 agent_registry（模型目錄）與 agents（persona 設定）。
type ModelAdminClient struct {
	registryURL string
	agentsURL   string
	http        *http.Client
}

// normalizeHTTPBaseURL 補上缺少的 scheme。這幾個內網服務都走 HTTP/2 + TLS，使用者
// 在 Settings 常只填 host:port（如 "192.168.50.104:8500"）；Go 的 url.Parse 沒有
// scheme 時會把 host 誤判成 scheme，組出請求路徑時炸成
// `parse "192.168.50.104:8500/registry/models": first path segment in URL cannot contain colon`。
// 已帶 scheme（"://"）的值原樣保留。
func normalizeHTTPBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" || strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "https://" + trimmed
}

// NewModelAdminClient 兩個 URL 皆空時回 nil（呼叫端據此隱藏分頁）。
func NewModelAdminClient(registryURL, agentsURL string, insecure bool) *ModelAdminClient {
	registryURL = normalizeHTTPBaseURL(registryURL)
	agentsURL = normalizeHTTPBaseURL(agentsURL)
	if registryURL == "" && agentsURL == "" {
		return nil
	}
	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 — 內網自簽
	}
	return &ModelAdminClient{
		registryURL: registryURL,
		agentsURL:   agentsURL,
		http:        &http.Client{Timeout: 60 * time.Second, Transport: tr},
	}
}

// HasRegistry / HasAgents 供 view 決定啟用哪些區塊。
func (c *ModelAdminClient) HasRegistry() bool { return c.registryURL != "" }
func (c *ModelAdminClient) HasAgents() bool   { return c.agentsURL != "" }

type envelope[T any] struct {
	Success bool   `json:"success"`
	Data    T      `json:"data"`
	Error   string `json:"error,omitempty"`
}

// do 發一個請求並解出信封的 data。body 為 nil 表示無 request body。
func do[T any](ctx context.Context, c *ModelAdminClient, method, url string, body any) (T, error) {
	var zero T
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return zero, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return zero, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return zero, fmt.Errorf("call %s: %w", url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var env envelope[T]
	if err := json.Unmarshal(raw, &env); err != nil {
		return zero, fmt.Errorf("decode %s response: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := env.Error
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return zero, fmt.Errorf("%s status %d: %s", url, resp.StatusCode, msg)
	}
	return env.Data, nil
}

// ListModels GET {registry}/registry/models
func (c *ModelAdminClient) ListModels(ctx context.Context) ([]models.LLMModel, error) {
	if c.registryURL == "" {
		return nil, fmt.Errorf("registry URL not configured")
	}
	data, err := do[models.ListModelsData](ctx, c, http.MethodGet, c.registryURL+"/registry/models", nil)
	if err != nil {
		return nil, err
	}
	return data.Models, nil
}

// SyncModels POST {registry}/registry/models/sync — 觸發從 Ollama 重新探測。
func (c *ModelAdminClient) SyncModels(ctx context.Context) ([]models.LLMModel, error) {
	if c.registryURL == "" {
		return nil, fmt.Errorf("registry URL not configured")
	}
	data, err := do[models.SyncModelsData](ctx, c, http.MethodPost, c.registryURL+"/registry/models/sync", nil)
	if err != nil {
		return nil, err
	}
	return data.Models, nil
}

// ListPersonas GET {agents}/api/personas
func (c *ModelAdminClient) ListPersonas(ctx context.Context) ([]models.PersonaInfo, error) {
	if c.agentsURL == "" {
		return nil, fmt.Errorf("model admin (agents) URL not configured")
	}
	data, err := do[models.ListPersonasData](ctx, c, http.MethodGet, c.agentsURL+"/api/personas", nil)
	if err != nil {
		return nil, err
	}
	return data.Personas, nil
}

// SetPersonaModel PUT {agents}/api/personas/:id/model — 設定 persona 推理模型。
func (c *ModelAdminClient) SetPersonaModel(ctx context.Context, agentID, model, role string) (models.PersonaInfo, error) {
	if c.agentsURL == "" {
		return models.PersonaInfo{}, fmt.Errorf("model admin (agents) URL not configured")
	}
	body := map[string]string{"model": model, "role": role}
	return do[models.PersonaInfo](ctx, c, http.MethodPut, c.agentsURL+"/api/personas/"+agentID+"/model", body)
}
