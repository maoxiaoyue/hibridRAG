// vision_extractor.go — 用視覺 LLM（Ollama /api/chat 的 images 欄位）把圖片抽成文字。
//
// 後端原本沒有 PDF/圖片解析能力（ingest 只吃文字）。上傳端點用本元件把圖片轉成
// 可攝取的純文字（OCR / 描述），再走既有 chunk → embed → Cassandra 管線。
//
// Ollama /api/chat 支援在 message 上帶 base64 圖片：
//
//	{ model, stream:false, messages:[{role:"user", content:"<prompt>", images:["<base64>"]}] }
package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultVisionPrompt 預設抽文字提示。
const DefaultVisionPrompt = "請擷取此圖片中的所有文字（OCR），逐字輸出；若圖片沒有文字，請以一段話客觀描述圖片內容。只輸出結果，不要加說明。"

// VisionExtractor 透過 Ollama 視覺模型把圖片轉文字。
type VisionExtractor struct {
	url    string
	model  string
	prompt string
	http   *http.Client
}

// NewVisionExtractor 建立 extractor。timeoutSec<=0 → 120s；prompt 空 → 內建預設。
func NewVisionExtractor(ollamaURL, model, prompt string, timeoutSec int) *VisionExtractor {
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultVisionPrompt
	}
	return &VisionExtractor{
		url:    strings.TrimRight(ollamaURL, "/"),
		model:  model,
		prompt: prompt,
		http:   &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}
}

type visionMsg struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"` // base64（無 data: 前綴）
}

type visionReq struct {
	Model    string      `json:"model"`
	Messages []visionMsg `json:"messages"`
	Stream   bool        `json:"stream"`
}

type visionResp struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

// Extract 把圖片 bytes 抽成文字。
func (v *VisionExtractor) Extract(ctx context.Context, image []byte) (string, error) {
	if v.url == "" {
		return "", fmt.Errorf("vision: ollama url not configured")
	}
	if v.model == "" {
		return "", fmt.Errorf("vision: model not configured")
	}
	b64 := base64.StdEncoding.EncodeToString(image)
	body, err := json.Marshal(visionReq{
		Model:  v.model,
		Stream: false,
		Messages: []visionMsg{
			{Role: "user", Content: v.prompt, Images: []string{b64}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("vision: marshal req: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.url+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision: call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("vision: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var r visionResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("vision: decode resp: %w", err)
	}
	return strings.TrimSpace(r.Message.Content), nil
}
