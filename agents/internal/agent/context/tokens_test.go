package context

import (
	"strings"
	"testing"
)

func TestHeuristicCounter_Count(t *testing.T) {
	c := NewHeuristicCounter()
	cases := []struct {
		name    string
		in      string
		minTok  int
		maxTok  int
	}{
		{"empty", "", 0, 0},
		{"ascii-short", "hello world", 2, 4},               // 11 chars / 4 ≈ 3
		{"cjk-10", "今天天氣真的非常好啊", 10, 10},                // 每字 1 token
		{"mixed", "你好 world 嗎？", 6, 9},
		{"ascii-long", strings.Repeat("a", 400), 95, 105},  // ~100 tokens
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Count(tc.in)
			if got < tc.minTok || got > tc.maxTok {
				t.Fatalf("Count(%q) = %d, want in [%d,%d]", tc.in, got, tc.minTok, tc.maxTok)
			}
		})
	}
}

func TestHeuristicCounter_Truncate(t *testing.T) {
	c := NewHeuristicCounter()

	t.Run("no-budget", func(t *testing.T) {
		s := "anything"
		if got := c.Truncate(s, 0); got != s {
			t.Fatalf("maxTokens=0 should return original; got %q", got)
		}
	})

	t.Run("under-budget", func(t *testing.T) {
		s := "hello"
		if got := c.Truncate(s, 100); got != s {
			t.Fatalf("under budget should return original; got %q", got)
		}
	})

	t.Run("cjk-cut", func(t *testing.T) {
		s := strings.Repeat("中", 50) // 約 50 tokens
		got := c.Truncate(s, 10)
		if !strings.Contains(got, "context truncated") {
			t.Fatalf("expected truncation marker; got %q", got)
		}
		// 截斷後估算 token 應接近 budget（含 marker 文字）
		head := strings.Split(got, "\n[...")[0]
		runes := []rune(head)
		if len(runes) > 12 || len(runes) < 8 {
			t.Fatalf("expected ~10 CJK runes before marker, got %d (%q)", len(runes), head)
		}
	})

	t.Run("utf8-safe", func(t *testing.T) {
		// 截斷不可破壞 UTF-8 字元
		s := "你好" + strings.Repeat("a", 100)
		got := c.Truncate(s, 5)
		if !strings.HasPrefix(got, "你好") {
			t.Fatalf("CJK prefix should be preserved intact; got %q", got[:20])
		}
	})
}
