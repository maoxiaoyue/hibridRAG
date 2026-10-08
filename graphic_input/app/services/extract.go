// extract.go — 文件抽文字：md/txt 原文直用、PDF 用 ledongthuc/pdf、
// 圖片用 Ollama vision OCR（移植自 agents 的 VisionExtractor，同一套請求格式）。
package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// DefaultVisionPrompt 預設 OCR 提示（同 agents 的 VisionExtractor）。
const DefaultVisionPrompt = "請擷取此圖片中的所有文字（OCR），逐字輸出；若圖片沒有文字，請以一段話客觀描述圖片內容。只輸出結果，不要加說明。"

// VisionClient 透過 Ollama 視覺模型把圖片轉文字；url 空 = 停用（圖片輸入回錯）。
type VisionClient struct {
	url    string
	model  string
	prompt string
	http   *http.Client
}

// NewVisionClient timeoutSec<=0 → 120s；prompt 空 → 內建預設。
func NewVisionClient(ollamaURL, model, prompt string, timeoutSec int) *VisionClient {
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultVisionPrompt
	}
	return &VisionClient{
		url:    strings.TrimRight(ollamaURL, "/"),
		model:  model,
		prompt: prompt,
		http:   &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}
}

// Enabled OCR 是否可用。
func (v *VisionClient) Enabled() bool { return v != nil && v.url != "" && v.model != "" }

type visionMsg struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

// OCR 把圖片 bytes 抽成文字。
func (v *VisionClient) OCR(ctx context.Context, image []byte) (string, error) {
	if !v.Enabled() {
		return "", fmt.Errorf("image input disabled (vision.ollama_url / vision.model not configured)")
	}
	body, err := json.Marshal(map[string]any{
		"model":  v.model,
		"stream": false,
		"messages": []visionMsg{
			{Role: "user", Content: v.prompt, Images: []string{base64.StdEncoding.EncodeToString(image)}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("vision: marshal: %w", err)
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
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("vision: decode: %w", err)
	}
	return strings.TrimSpace(out.Message.Content), nil
}

// FileKind 檔案分類。
type FileKind int

const (
	KindUnsupported FileKind = iota
	KindMarkdown
	KindText
	KindImage
	KindPDF
)

// ClassifyFile 依副檔名分類。
func ClassifyFile(name string) FileKind {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return KindMarkdown
	case ".txt", ".text":
		return KindText
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp":
		return KindImage
	case ".pdf":
		return KindPDF
	default:
		return KindUnsupported
	}
}

// ExtractText 依型別把檔案 bytes 抽成純文字。
// 圖片走 vision OCR（vision 未配置時明確回錯）；掃描型 PDF（抽不出文字）明確回錯。
func ExtractText(ctx context.Context, vision *VisionClient, name string, data []byte) (string, error) {
	switch ClassifyFile(name) {
	case KindMarkdown, KindText:
		if !utf8.Valid(data) {
			return "", fmt.Errorf("%s is not valid UTF-8 text", name)
		}
		return string(data), nil

	case KindPDF:
		text, err := extractPDFText(data)
		if err != nil {
			return "", fmt.Errorf("pdf extract: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("pdf has no extractable text (likely scanned; OCR for PDF not supported)")
		}
		return text, nil

	case KindImage:
		text, err := vision.OCR(ctx, data)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("vision returned empty text")
		}
		return text, nil

	default:
		return "", fmt.Errorf("unsupported file type %q", filepath.Ext(name))
	}
}

// extractPDFText 用 ledongthuc/pdf 抽純文字（bytes 版，透過 ReaderAt）。
func extractPDFText(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	plain, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if _, err := io.Copy(&sb, plain); err != nil {
		return "", err
	}
	return sb.String(), nil
}
