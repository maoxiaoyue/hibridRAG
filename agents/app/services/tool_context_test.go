package services

import (
	"context"
	"testing"

	"agent/app/models"
)

// ── resolveToolAgentID 優先序 ────────────────────────────────────────────────

func TestResolveToolAgentIDPrecedence(t *testing.T) {
	base := context.Background()
	injected := WithToolAgentID(base, "persona-b")

	cases := []struct {
		name     string
		ctx      context.Context
		args     map[string]any
		fallback string
		want     string
	}{
		{"args 覆寫最優先", injected, map[string]any{"agent_id": "explicit"}, "agent-main", "explicit"},
		{"ctx 注入蓋過靜態 fallback", injected, map[string]any{}, "agent-main", "persona-b"},
		{"未注入時退回靜態 fallback", base, map[string]any{}, "agent-main", "agent-main"},
		{"args 空字串不算覆寫", injected, map[string]any{"agent_id": ""}, "agent-main", "persona-b"},
		{"全部皆空回空字串", base, map[string]any{}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveToolAgentID(tc.ctx, tc.args, tc.fallback); got != tc.want {
				t.Errorf("resolveToolAgentID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWithToolAgentIDEmptyIsNoop(t *testing.T) {
	ctx := WithToolAgentID(context.Background(), "")
	if got := ToolAgentIDFrom(ctx); got != "" {
		t.Errorf("ToolAgentIDFrom = %q, want empty", got)
	}
}

// ── 工具端到端：memory_write 以 ctx 注入的 persona 為 scope ─────────────────

type capturingWriter struct {
	gotAgentID string
}

func (w *capturingWriter) UpsertProcedural(_ context.Context, req models.CreateProceduralMemoryRequest) (*models.ProceduralMemory, error) {
	w.gotAgentID = req.AgentID
	return &models.ProceduralMemory{ID: "prm_test", AgentID: req.AgentID, ModelName: req.ModelName}, nil
}

func TestMemoryWriteToolUsesRequestPersona(t *testing.T) {
	w := &capturingWriter{}
	tool := &MemoryWriteTool{AgentID: "agent-main", Writer: w}

	ctx := WithToolAgentID(context.Background(), "persona-b")
	res, err := tool.Call(ctx, map[string]any{
		"kind": "procedural", "model_name": "m", "content": "c",
	})
	if err != nil || res.IsError {
		t.Fatalf("Call failed: err=%v content=%s", err, res.Content)
	}
	if w.gotAgentID != "persona-b" {
		t.Errorf("wrote to agent %q, want request persona %q (config fallback leaked)", w.gotAgentID, "persona-b")
	}
}

func TestMemoryWriteToolFallsBackWithoutInjection(t *testing.T) {
	w := &capturingWriter{}
	tool := &MemoryWriteTool{AgentID: "agent-main", Writer: w}

	res, err := tool.Call(context.Background(), map[string]any{
		"kind": "procedural", "model_name": "m", "content": "c",
	})
	if err != nil || res.IsError {
		t.Fatalf("Call failed: err=%v content=%s", err, res.Content)
	}
	if w.gotAgentID != "agent-main" {
		t.Errorf("wrote to agent %q, want static fallback %q", w.gotAgentID, "agent-main")
	}
}
