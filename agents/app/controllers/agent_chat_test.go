package controllers_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent/app/controllers"
	"agent/app/models"
	"agent/app/services"
)

// ── mocks ───────────────────────────────────────────────────────────────────

// stubToolLLM implements services.ToolCallerLLM with a scripted response queue.
type stubToolLLM struct {
	responses    []*services.ToolChatResponse
	idx          int
	lastMessages []services.ChatMessage // messages seen on the most recent ChatWithTools
}

func (m *stubToolLLM) Chat(_ context.Context, _ services.ChatRequest) (*services.ChatResponse, error) {
	r := m.responses[m.idx]
	m.idx++
	return &r.ChatResponse, nil
}
func (m *stubToolLLM) Provider() string { return "stubtool" }
func (m *stubToolLLM) ChatWithTools(_ context.Context, req services.ChatRequest, _ []services.Tool) (*services.ToolChatResponse, error) {
	m.lastMessages = req.Messages
	r := m.responses[m.idx]
	m.idx++
	return r, nil
}

// echoTool is a server-side tool that records invocations.
type echoTool struct{ calls *int }

func (e *echoTool) Name() string                   { return "server_echo" }
func (e *echoTool) Description() string             { return "echo for tests" }
func (e *echoTool) Schema() services.ToolSchema     { return services.NewObjectSchema() }
func (e *echoTool) Call(context.Context, map[string]any) (services.ToolResult, error) {
	*e.calls++
	return services.ToolResult{Content: "echoed"}, nil
}

// ── tests ─────────────────────────────────────────────────────────────────────

// LLM 不支援 tool calling → 退回純 Chat，直接 final。
func TestChatSync_PlainChatFallback(t *testing.T) {
	llm := &stubLLM{resp: &services.ChatResponse{Content: "plain reply"}}
	srv := controllers.NewAgentServer("self", &stubSkills{}, llm, nil)

	resp, code, err := srv.ChatSync(context.Background(), models.ChatTurnRequest{
		AgentID:  "demo",
		Messages: []models.ChatTurnMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil || code != 200 {
		t.Fatalf("unexpected: code=%d err=%v", code, err)
	}
	if resp.Status != "final" || resp.Content != "plain reply" {
		t.Errorf("want final/'plain reply', got %s/%q", resp.Status, resp.Content)
	}
	if resp.SessionID == "" {
		t.Error("expected a session_id to be assigned")
	}
}

// server 工具就地執行：第一輪呼叫 server_echo，第二輪終局 → status=final，工具被執行一次。
func TestChatSync_ServerToolInline(t *testing.T) {
	calls := 0
	reg := services.NewToolRegistry()
	reg.MustRegister(&echoTool{calls: &calls})

	llm := &stubToolLLM{responses: []*services.ToolChatResponse{
		{ToolCalls: []services.ToolCall{{ID: "c1", Name: "server_echo", Args: map[string]any{}}}},
		{ChatResponse: services.ChatResponse{Content: "final answer"}},
	}}
	srv := controllers.NewAgentServer("self", &stubSkills{}, llm, nil).
		WithToolRegistry(reg, services.ToolLoopOptions{})

	resp, code, err := srv.ChatSync(context.Background(), models.ChatTurnRequest{
		AgentID:  "demo",
		Messages: []models.ChatTurnMessage{{Role: "user", Content: "use the tool"}},
	})
	if err != nil || code != 200 {
		t.Fatalf("unexpected: code=%d err=%v", code, err)
	}
	if resp.Status != "final" || resp.Content != "final answer" {
		t.Errorf("want final/'final answer', got %s/%q", resp.Status, resp.Content)
	}
	if calls != 1 {
		t.Errorf("server tool should run once inline, ran %d times", calls)
	}
}

// client 工具回合制：第一輪呼叫 client 工具 → 暫停回 tool_calls；
// 帶 continuation_id + tool_results resume → 第二輪終局 final，且 LLM 第二次看到 tool_result。
func TestChatSync_ClientToolRoundTrip(t *testing.T) {
	reg := services.NewToolRegistry()
	llm := &stubToolLLM{responses: []*services.ToolChatResponse{
		{ToolCalls: []services.ToolCall{{ID: "call_1", Name: "local_run", Args: map[string]any{"x": float64(1)}}}},
		{ChatResponse: services.ChatResponse{Content: "done", Usage: services.TokenUsage{TotalTokens: 7}}},
	}}
	srv := controllers.NewAgentServer("self", &stubSkills{}, llm, nil).
		WithToolRegistry(reg, services.ToolLoopOptions{})

	// 首次：宣告 client 工具 local_run
	resp, code, err := srv.ChatSync(context.Background(), models.ChatTurnRequest{
		AgentID:  "demo",
		Messages: []models.ChatTurnMessage{{Role: "user", Content: "run locally"}},
		ClientTools: []models.ClientToolSpec{
			{Name: "local_run", Description: "run a local program", Schema: json.RawMessage(`{"type":"object"}`)},
		},
	})
	if err != nil || code != 200 {
		t.Fatalf("unexpected: code=%d err=%v", code, err)
	}
	if resp.Status != "tool_calls" {
		t.Fatalf("want tool_calls, got %s", resp.Status)
	}
	if resp.ContinuationID == "" {
		t.Fatal("expected continuation_id")
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "local_run" || resp.ToolCalls[0].ID != "call_1" {
		t.Fatalf("unexpected tool_calls: %+v", resp.ToolCalls)
	}

	// resume：回送本地執行結果
	resp2, code2, err2 := srv.ChatSync(context.Background(), models.ChatTurnRequest{
		AgentID:        "demo",
		ContinuationID: resp.ContinuationID,
		ToolResults: []models.ClientToolResult{
			{ToolCallID: "call_1", Name: "local_run", Content: "exit=0 stdout=42"},
		},
	})
	if err2 != nil || code2 != 200 {
		t.Fatalf("resume unexpected: code=%d err=%v", code2, err2)
	}
	if resp2.Status != "final" || resp2.Content != "done" {
		t.Errorf("want final/'done', got %s/%q", resp2.Status, resp2.Content)
	}
	if resp2.SessionID != resp.SessionID {
		t.Errorf("session_id should persist across resume: %q vs %q", resp.SessionID, resp2.SessionID)
	}

	// LLM 第二次呼叫應在 messages 中看到 tool_result
	found := false
	for _, m := range llm.lastMessages {
		if strings.Contains(m.Content, "[tool_result name=local_run") && strings.Contains(m.Content, "exit=0 stdout=42") {
			found = true
		}
	}
	if !found {
		t.Errorf("LLM did not see the client tool result; messages=%+v", llm.lastMessages)
	}
}

// 接續一個不存在/過期的 continuation_id → 404。
func TestChatSync_UnknownContinuation(t *testing.T) {
	llm := &stubToolLLM{}
	srv := controllers.NewAgentServer("self", &stubSkills{}, llm, nil).
		WithToolRegistry(services.NewToolRegistry(), services.ToolLoopOptions{})

	_, code, err := srv.ChatSync(context.Background(), models.ChatTurnRequest{
		AgentID:        "demo",
		ContinuationID: "cont_doesnotexist",
		ToolResults:    []models.ClientToolResult{{ToolCallID: "x", Name: "y", Content: "z"}},
	})
	if code != 404 || err == nil {
		t.Errorf("want 404 error, got code=%d err=%v", code, err)
	}
}
