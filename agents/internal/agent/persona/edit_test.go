package persona

import "testing"

func TestUpsertModelSection_Append(t *testing.T) {
	body := "# 小柴\n\n## Identity\n- name: 小柴\n"
	got := UpsertModelSection(body, "qwen3.5:35b", "chat_quality")

	// 原內容保留
	if !contains(got, "## Identity") || !contains(got, "- name: 小柴") {
		t.Fatalf("original content lost:\n%s", got)
	}
	// 解析回來要拿得到偏好
	pref := Parse(got).ModelPref
	if pref == nil || pref.Model != "qwen3.5:35b" || pref.Role != "chat_quality" {
		t.Fatalf("parsed pref mismatch: %+v\n%s", pref, got)
	}
}

func TestUpsertModelSection_Replace(t *testing.T) {
	body := "# A\n\n## Model\n- model: old:1b\n\n## Identity\n- name: A\n"
	got := UpsertModelSection(body, "new:9b", "")

	pref := Parse(got).ModelPref
	if pref == nil || pref.Model != "new:9b" {
		t.Fatalf("expected model new:9b, got %+v\n%s", pref, got)
	}
	// 舊值不得殘留，且後續 section 仍在
	if contains(got, "old:1b") {
		t.Fatalf("old model leaked:\n%s", got)
	}
	if !contains(got, "## Identity") || !contains(got, "- name: A") {
		t.Fatalf("following section lost:\n%s", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
