package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent/internal/agent/persona"
)

func TestRoleCompiler_Compile_RoleOnly(t *testing.T) {
	dir := t.TempDir()
	rc := NewRoleCompiler(DefaultModelRegistry(), dir)

	parsed := persona.Parse("# Test Agent\n\n## Model\n- role: chat_fast\n")
	model, role, err := rc.Compile("test-agent", parsed)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if role != RoleChatFast {
		t.Errorf("role = %q, want %q", role, RoleChatFast)
	}
	if model == "" {
		t.Error("expected a resolved model, got empty string")
	}

	data, err := os.ReadFile(filepath.Join(dir, "test-agent", "ROLE.md"))
	if err != nil {
		t.Fatalf("ROLE.md not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, string(role)) || !strings.Contains(content, model) {
		t.Errorf("ROLE.md content missing role/model: %s", content)
	}
}

func TestRoleCompiler_Compile_ExplicitModelOverridesRole(t *testing.T) {
	dir := t.TempDir()
	rc := NewRoleCompiler(DefaultModelRegistry(), dir)

	parsed := persona.Parse("# Test\n\n## Model\n- role: chat_fast\n- model: custom-model:1b\n")
	model, _, err := rc.Compile("agent2", parsed)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if model != "custom-model:1b" {
		t.Errorf("model = %q, want explicit override %q", model, "custom-model:1b")
	}
}

func TestRoleCompiler_Compile_NoPreference(t *testing.T) {
	dir := t.TempDir()
	rc := NewRoleCompiler(DefaultModelRegistry(), dir)

	parsed := persona.Parse("# Test\n\n## Identity\n- name: x\n")
	model, role, err := rc.Compile("agent3", parsed)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if model != "" || role != "" {
		t.Errorf("expected empty model/role, got model=%q role=%q", model, role)
	}
	data, err := os.ReadFile(filepath.Join(dir, "agent3", "ROLE.md"))
	if err != nil {
		t.Fatalf("ROLE.md not written: %v", err)
	}
	if !strings.Contains(string(data), "(none") {
		t.Errorf("expected (none) placeholder in ROLE.md, got: %s", data)
	}
}

func TestRoleCompiler_Compile_UnknownRole(t *testing.T) {
	dir := t.TempDir()
	rc := NewRoleCompiler(DefaultModelRegistry(), dir)

	parsed := persona.Parse("# Test\n\n## Model\n- role: not_a_real_role\n")
	_, _, err := rc.Compile("agent4", parsed)
	if err == nil {
		t.Error("expected error for unknown role")
	}
}

func TestCompilingSkillLoader_LoadCompilesOnRealReloadOnly(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agentx")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(agentDir, "SKILL.md")
	body := "# AgentX\n\n## Model\n- role: chat_fast\n"
	if err := os.WriteFile(skillPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewSkillLoader(root)
	rc := NewRoleCompiler(DefaultModelRegistry(), root)
	cl := NewCompilingSkillLoader(loader, rc)

	if _, ok := cl.ModelResolutionFor("agentx"); ok {
		t.Fatal("expected no resolution before first Load")
	}

	pack, cached, _, err := cl.Load("agentx", false)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if pack == nil {
		t.Fatal("expected non-nil pack")
	}
	if cached {
		t.Error("first load should not report cached=true")
	}

	res, ok := cl.ModelResolutionFor("agentx")
	if !ok || res == nil {
		t.Fatal("expected a cached ModelResolution after first Load")
	}
	if res.Role != RoleChatFast {
		t.Errorf("res.Role = %q, want %q", res.Role, RoleChatFast)
	}
	if res.Model == "" {
		t.Error("expected a non-empty resolved model")
	}

	// Second load with unchanged file should hit SkillLoader's cache — same
	// pack instance, cached=true.
	pack2, cached2, _, err := cl.Load("agentx", false)
	if err != nil {
		t.Fatalf("second Load error: %v", err)
	}
	if !cached2 {
		t.Error("second load with unchanged file should be cached")
	}
	if pack2 != pack {
		t.Error("expected the same cached pack instance on cache hit")
	}
}
