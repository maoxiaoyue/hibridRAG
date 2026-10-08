package services

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, skillsDir, id, body string) {
	t.Helper()
	dir := filepath.Join(skillsDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPersonaAdmin_GetSetSkillMD(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "agent-1", "# Agent One\n## Model\n- model: gemma4:e4b\n")
	a := NewPersonaAdmin(dir, nil) // nil loader = 跳過 reload，純測讀寫

	got, err := a.GetSkillMD("agent-1")
	if err != nil {
		t.Fatalf("GetSkillMD: %v", err)
	}
	if got != "# Agent One\n## Model\n- model: gemma4:e4b\n" {
		t.Fatalf("GetSkillMD content mismatch: %q", got)
	}

	updated := "# Agent One\n## Identity\n- role: tester\n## Model\n- model: qwen3:9b\n"
	info, err := a.SetSkillMD("agent-1", updated)
	if err != nil {
		t.Fatalf("SetSkillMD: %v", err)
	}
	if info.Model != "qwen3:9b" {
		t.Errorf("info.Model = %q, want qwen3:9b (readInfo should reflect the new content)", info.Model)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "agent-1", "SKILL.md"))
	if err != nil || string(onDisk) != updated {
		t.Fatalf("on-disk content = %q, err=%v, want %q", onDisk, err, updated)
	}
}

func TestPersonaAdmin_SetSkillMD_Validation(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "agent-1", "# Agent One\n")
	a := NewPersonaAdmin(dir, nil)

	if _, err := a.SetSkillMD("agent-1", "   "); err == nil {
		t.Error("empty content should be rejected")
	}
	if _, err := a.SetSkillMD("../escape", "content"); err == nil {
		t.Error("path-escaping agent_id should be rejected")
	}
	if _, err := a.SetSkillMD("does-not-exist", "content"); err == nil {
		t.Error("unknown persona should be rejected, not silently create a new one")
	}
	if _, err := a.GetSkillMD("../escape"); err == nil {
		t.Error("GetSkillMD should also reject path-escaping agent_id")
	}
}
