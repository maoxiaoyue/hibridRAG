package services

import (
	"testing"

	"agent/internal/agent/persona"
)

func TestAgentCardBuilder_Build_FallsBackWithoutSkillPack(t *testing.T) {
	b := NewAgentCardBuilder("agent-main", "0.1.0", "https://localhost:18080/")
	card := b.Build("agent-main", nil)

	if card.Name != "agent-main" {
		t.Errorf("Name = %q", card.Name)
	}
	if card.Description != defaultDescription {
		t.Errorf("Description = %q, want fallback %q", card.Description, defaultDescription)
	}
	if len(card.Skills) != 1 || card.Skills[0].ID != "chat" {
		t.Errorf("expected fallback single 'chat' skill, got %+v", card.Skills)
	}
}

func TestAgentCardBuilder_Build_DerivesFromPersona(t *testing.T) {
	b := NewAgentCardBuilder("agent-main", "0.1.0", "https://localhost:18080/")
	parsed := persona.Parse(`# 小柴

## Identity
- role: 程式設計助理
- domain: Go backend

## Mental Models

### 先驗證後實作
內容描述。

### Interface Segregation
Another description that is reasonably short.
`)
	card := b.Build("agent-main", parsed)

	if card.Name != "小柴" {
		t.Errorf("Name = %q", card.Name)
	}
	if card.Description != "程式設計助理 — Go backend" {
		t.Errorf("Description = %q", card.Description)
	}
	if len(card.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d: %+v", len(card.Skills), card.Skills)
	}
	if card.Skills[0].ID != "先驗證後實作" {
		t.Errorf("Skills[0].ID = %q", card.Skills[0].ID)
	}
	if card.Skills[1].ID != "interface-segregation" {
		t.Errorf("Skills[1].ID = %q", card.Skills[1].ID)
	}
}

func TestAgentCardBuilder_Build_EmptyMentalModelsFallsBack(t *testing.T) {
	b := NewAgentCardBuilder("agent-main", "0.1.0", "")
	parsed := persona.Parse("# Demo\n\n## Identity\n- name: Demo\n")
	card := b.Build("agent-main", parsed)

	if len(card.Skills) != 1 || card.Skills[0].ID != "chat" {
		t.Errorf("expected fallback single 'chat' skill when no MentalModels, got %+v", card.Skills)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Interface Segregation": "interface-segregation",
		"先驗證後實作":                "先驗證後實作",
		"  Multi   Space  ":     "multi-space",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
