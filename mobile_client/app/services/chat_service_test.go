package services

import (
	"context"
	"errors"
	"testing"

	"mobile_client/app/models"
)

// stubBackend 可程式化的 ChatBackend。
type stubBackend struct {
	responses []*models.ChatResponse
	requests  []models.ChatRequest
}

func (s *stubBackend) AgentID() string { return "agent-test" }

func (s *stubBackend) Chat(_ context.Context, req models.ChatRequest) (*models.ChatResponse, error) {
	s.requests = append(s.requests, req)
	if len(s.responses) == 0 {
		return nil, errors.New("no more stub responses")
	}
	resp := s.responses[0]
	s.responses = s.responses[1:]
	return resp, nil
}

func TestSend_FinalDirect(t *testing.T) {
	b := &stubBackend{responses: []*models.ChatResponse{
		{Status: "final", Content: "hi!", SessionID: "sess_a",
			Usage: &models.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}},
	}}
	eng := NewChatEngine(b, nil)
	out, err := eng.Send(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if out != "hi!" {
		t.Fatalf("want hi!, got %q", out)
	}
	if eng.SessionID() != "sess_a" {
		t.Fatalf("session not adopted: %q", eng.SessionID())
	}
	if eng.Usage() == nil || eng.Usage().TotalTokens != 3 {
		t.Fatalf("usage not recorded: %+v", eng.Usage())
	}
}

// 手機版不支援本機工具：server 回 tool_calls 時逐一回錯並續推理到 final。
func TestSend_ToolCallsDeclined(t *testing.T) {
	b := &stubBackend{responses: []*models.ChatResponse{
		{Status: "tool_calls", SessionID: "sess_b", ContinuationID: "cont_1",
			ToolCalls: []models.ToolCall{{ID: "c1", Name: "run_thing"}}},
		{Status: "final", Content: "done without tool", SessionID: "sess_b"},
	}}
	eng := NewChatEngine(b, nil)
	out, err := eng.Send(context.Background(), "do it")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if out != "done without tool" {
		t.Fatalf("unexpected reply: %q", out)
	}
	if len(b.requests) != 2 {
		t.Fatalf("want 2 requests, got %d", len(b.requests))
	}
	second := b.requests[1]
	if second.ContinuationID != "cont_1" {
		t.Fatalf("continuation not forwarded: %q", second.ContinuationID)
	}
	if len(second.ToolResults) != 1 || !second.ToolResults[0].IsError {
		t.Fatalf("tool call should be declined with IsError result: %+v", second.ToolResults)
	}
}

func TestSend_RoundLimit(t *testing.T) {
	// 永遠回 tool_calls → 應在 12 回合後回 ErrChatRoundLimit
	responses := make([]*models.ChatResponse, 0, 13)
	for i := 0; i < 13; i++ {
		responses = append(responses, &models.ChatResponse{
			Status: "tool_calls", SessionID: "sess_c",
			ToolCalls: []models.ToolCall{{ID: "x", Name: "loop"}},
		})
	}
	b := &stubBackend{responses: responses}
	eng := NewChatEngine(b, nil)
	_, err := eng.Send(context.Background(), "loop")
	if !errors.Is(err, ErrChatRoundLimit) {
		t.Fatalf("want ErrChatRoundLimit, got %v", err)
	}
}

func TestSend_PreferredSessionID(t *testing.T) {
	b := &stubBackend{responses: []*models.ChatResponse{
		{Status: "final", Content: "ok", SessionID: "sess_client"},
	}}
	eng := NewChatEngine(b, nil)
	eng.SetPreferredSessionID("sess_client")
	if _, err := eng.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if b.requests[0].SessionID != "sess_client" {
		t.Fatalf("preferred session id not sent: %q", b.requests[0].SessionID)
	}
}

func TestNormalizeGatewayAddr(t *testing.T) {
	cases := map[string]string{
		"https://1.2.3.4:9090/": "1.2.3.4:9090",
		"http://h:1":            "h:1",
		"  h:2 ":                "h:2",
		"":                      "",
	}
	for in, want := range cases {
		if got := normalizeGatewayAddr(in); got != want {
			t.Errorf("normalizeGatewayAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeHeaderValue(t *testing.T) {
	if got := sanitizeHeaderValue("HUI 的電腦"); got != "HUI" {
		t.Errorf("want non-ASCII dropped, got %q", got)
	}
	if got := sanitizeHeaderValue("電腦"); got != "unknown" {
		t.Errorf("want fallback unknown, got %q", got)
	}
}
