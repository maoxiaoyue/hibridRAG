package services

import (
	"encoding/json"
	"testing"
)

func TestModalitiesFromCaps(t *testing.T) {
	cases := []struct {
		name     string
		caps     []string
		families []string
		want     [3]bool // text, image, audio
	}{
		{"chat only", []string{"completion", "tools"}, nil, [3]bool{true, false, false}},
		{"vision cap", []string{"completion", "vision"}, nil, [3]bool{true, true, false}},
		{"embedding", []string{"embedding"}, nil, [3]bool{true, false, false}},
		{"audio cap", []string{"completion", "audio"}, nil, [3]bool{true, false, true}},
		{"vision via family fallback", nil, []string{"clip"}, [3]bool{true, true, false}},
		{"empty", nil, nil, [3]bool{true, false, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := modalitiesFromCaps(c.caps, c.families)
			if got.Text != c.want[0] || got.Image != c.want[1] || got.Audio != c.want[2] {
				t.Errorf("modalitiesFromCaps(%v,%v) = {t:%v i:%v a:%v}, want %v",
					c.caps, c.families, got.Text, got.Image, got.Audio, c.want)
			}
		})
	}
}

func TestContextLenFromModelInfo(t *testing.T) {
	var mi map[string]any
	_ = json.Unmarshal([]byte(`{"general.architecture":"qwen3","qwen3.context_length":32768,"qwen3.embedding_length":4096}`), &mi)
	if got := contextLenFromModelInfo(mi); got != 32768 {
		t.Errorf("contextLenFromModelInfo = %d, want 32768", got)
	}
	if got := contextLenFromModelInfo(map[string]any{"foo": "bar"}); got != 0 {
		t.Errorf("expected 0 when no context_length key, got %d", got)
	}
}
