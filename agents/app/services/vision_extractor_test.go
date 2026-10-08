package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVisionExtractor_Extract(t *testing.T) {
	var gotBody visionReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_ = json.NewEncoder(w).Encode(visionResp{Message: struct {
			Content string `json:"content"`
		}{Content: "  hello from image  "}})
	}))
	defer srv.Close()

	v := NewVisionExtractor(srv.URL, "llava", "", 10)
	out, err := v.Extract(context.Background(), []byte{0x89, 0x50, 0x4e, 0x47}) // fake PNG bytes
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if out != "hello from image" {
		t.Errorf("content trimmed? got %q", out)
	}
	if gotBody.Model != "llava" {
		t.Errorf("model = %q", gotBody.Model)
	}
	if len(gotBody.Messages) != 1 || len(gotBody.Messages[0].Images) != 1 {
		t.Fatalf("expected 1 message with 1 image, got %+v", gotBody.Messages)
	}
	if gotBody.Messages[0].Content == "" || !strings.Contains(gotBody.Messages[0].Content, "OCR") {
		t.Errorf("expected default OCR prompt, got %q", gotBody.Messages[0].Content)
	}
}

func TestVisionExtractor_NotConfigured(t *testing.T) {
	v := NewVisionExtractor("", "llava", "", 10)
	if _, err := v.Extract(context.Background(), []byte{1}); err == nil {
		t.Error("expected error when url empty")
	}
	v2 := NewVisionExtractor("http://x", "", "", 10)
	if _, err := v2.Extract(context.Background(), []byte{1}); err == nil {
		t.Error("expected error when model empty")
	}
}
