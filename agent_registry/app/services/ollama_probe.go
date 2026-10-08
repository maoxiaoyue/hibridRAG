// ollama_probe.go — 從 Ollama 探得目前可用的模型清單與能力。
//
// 兩支 API：
//   GET  /api/tags        列出已安裝模型（名稱 / 大小 / details）
//   POST /api/show        單一模型細節（capabilities / model_info 上下文長度）
//
// capabilities → modalities 對應：text 恆為真（含 embedding 也吃文字）；
// vision → image；audio → audio（Ollama 現無標準 audio 能力，預留）。
package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agent_registry/app/models"
)

// OllamaProber 針對單一 Ollama host 做探測。
type OllamaProber struct {
	host   string
	client *http.Client
}

// NewOllamaProber host 例 "http://192.168.50.104:11434"。
func NewOllamaProber(host string) *OllamaProber {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	return &OllamaProber{
		host:   host,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *OllamaProber) Host() string { return p.host }

type ollamaDetails struct {
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

type tagsResponse struct {
	Models []struct {
		Name    string        `json:"name"`
		Model   string        `json:"model"`
		Size    int64         `json:"size"`
		Details ollamaDetails `json:"details"`
	} `json:"models"`
}

type showResponse struct {
	Capabilities []string       `json:"capabilities"`
	Details      ollamaDetails  `json:"details"`
	ModelInfo    map[string]any `json:"model_info"`
}

// Probe 回傳 host 上所有模型的中繼資料（每顆會再打一次 /api/show 補 capabilities）。
func (p *OllamaProber) Probe(ctx context.Context) ([]models.LLMModel, error) {
	tags, err := p.listTags(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]models.LLMModel, 0, len(tags.Models))
	for _, m := range tags.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name == "" {
			continue
		}
		caps, families, ctxLen := p.show(ctx, name)
		// details 以 /api/show 為主，/api/tags 為輔
		det := m.Details
		if len(families) > 0 {
			det.Families = families
		}
		out = append(out, models.LLMModel{
			Name:          name,
			Host:          p.host,
			Modalities:    modalitiesFromCaps(caps, det.Families),
			Capabilities:  caps,
			Family:        det.Family,
			ParameterSize: det.ParameterSize,
			Quantization:  det.QuantizationLevel,
			SizeBytes:     m.Size,
			ContextLen:    ctxLen,
			LastSyncedAt:  now,
		})
	}
	return out, nil
}

func (p *OllamaProber) listTags(ctx context.Context) (*tagsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.host+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama /api/tags: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama /api/tags: status %d", resp.StatusCode)
	}
	var tr tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("decode /api/tags: %w", err)
	}
	return &tr, nil
}

// show 回傳單一模型的 capabilities / families / context length；失敗時回傳空值
// （探測性質，不因單顆模型失敗而中斷整批）。
func (p *OllamaProber) show(ctx context.Context, name string) (caps, families []string, ctxLen int) {
	body, _ := json.Marshal(map[string]string{"model": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.host+"/api/show", bytes.NewReader(body))
	if err != nil {
		return nil, nil, 0
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, 0
	}
	var sr showResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, nil, 0
	}
	return sr.Capabilities, sr.Details.Families, contextLenFromModelInfo(sr.ModelInfo)
}

// contextLenFromModelInfo model_info 的鍵名依 family 而異（例 "qwen3.context_length"），
// 取第一個以 ".context_length" 結尾的整數值。
func contextLenFromModelInfo(mi map[string]any) int {
	for k, v := range mi {
		if !strings.HasSuffix(k, ".context_length") {
			continue
		}
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case json.Number:
			i, _ := n.Int64()
			return int(i)
		}
	}
	return 0
}

// modalitiesFromCaps 由 capabilities（優先）與 families（fallback）推導可接受的輸入型態。
func modalitiesFromCaps(caps, families []string) models.Modalities {
	mod := models.Modalities{Text: true} // 文字輸入為所有 LLM 的基本能力
	for _, c := range caps {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "vision":
			mod.Image = true
		case "audio":
			mod.Audio = true
		}
	}
	// capabilities 缺漏時（較舊 Ollama）以 families 補推影像能力
	if !mod.Image {
		for _, f := range families {
			switch strings.ToLower(strings.TrimSpace(f)) {
			case "clip", "mllama", "llava", "qwen2vl", "qwen3vl":
				mod.Image = true
			}
		}
	}
	return mod
}
