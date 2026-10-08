package services

import "testing"

// 迴歸測試：assistant 的 tool_calls 必須無損編碼/還原成 Ollama 原生 tool_calls。
// 若退回成佔位文字（如 "[tool_calls x1]"），模型會忽略 tool result 甚至幻覺（實測 gemma4）。
func TestAssistantToolCalls_RoundTrip(t *testing.T) {
	calls := []ToolCall{
		{ID: "call_1", Name: "get_time", Args: map[string]any{}},
		{ID: "call_2", Name: "run_local", Args: map[string]any{"path": "C:\\tmp", "n": float64(3)}},
	}
	encoded := EncodeAssistantToolCalls(calls)

	msgs := []ChatMessage{
		{Role: RoleUser, Content: "do it"},
		{Role: RoleAssistant, Content: encoded},
		{Role: RoleUser, Content: "[tool_result name=get_time status=ok]\n2026-06-05 22:30:11"},
	}
	out := convertChatMessagesToOllama(msgs)

	if len(out) != 3 {
		t.Fatalf("want 3 messages, got %d", len(out))
	}
	// assistant turn → 原生 tool_calls，content 清空
	a := out[1]
	if a.Role != "assistant" || a.Content != "" {
		t.Errorf("assistant turn role=%q content=%q", a.Role, a.Content)
	}
	if len(a.ToolCalls) != 2 {
		t.Fatalf("want 2 tool_calls, got %d", len(a.ToolCalls))
	}
	if a.ToolCalls[0].Function.Name != "get_time" || a.ToolCalls[1].Function.Name != "run_local" {
		t.Errorf("tool call names = %q, %q", a.ToolCalls[0].Function.Name, a.ToolCalls[1].Function.Name)
	}
	if a.ToolCalls[1].Function.Arguments["path"] != "C:\\tmp" {
		t.Errorf("args not preserved: %v", a.ToolCalls[1].Function.Arguments)
	}
	// tool result turn → role=tool + name
	tr := out[2]
	if tr.Role != "tool" || tr.Name != "get_time" || tr.Content == "" {
		t.Errorf("tool result turn role=%q name=%q content=%q", tr.Role, tr.Name, tr.Content)
	}
}
