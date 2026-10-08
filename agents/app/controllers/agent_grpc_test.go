package controllers_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"agent/app/controllers"
	"agent/app/models"
	"agent/app/services"
)

// ── mock helpers ──────────────────────────────────────────────────────────────

// stubSkills implements controllers.SkillProvider.
type stubSkills struct {
	pack    *models.SkillPack
	loadErr error
}

func (s *stubSkills) Get(_ string) (*models.SkillPack, error) {
	if s.pack == nil {
		return nil, services.ErrNotFound
	}
	return s.pack, nil
}

func (s *stubSkills) Load(_ string, _ bool) (*models.SkillPack, bool, []string, error) {
	if s.loadErr != nil {
		return nil, false, nil, s.loadErr
	}
	return s.pack, false, []string{"SKILL.md"}, nil
}

// stubLLM implements services.LLMClient.
type stubLLM struct {
	resp   *services.ChatResponse
	err    error
	called chan services.ChatRequest // signals when Chat() is called
}

func (m *stubLLM) Chat(_ context.Context, req services.ChatRequest) (*services.ChatResponse, error) {
	if m.called != nil {
		select {
		case m.called <- req:
		default:
		}
	}
	return m.resp, m.err
}
func (m *stubLLM) Provider() string { return "stub" }

// stubDispatcher implements services.Dispatcher.
type stubDispatcher struct {
	mu   sync.Mutex
	sent []*models.OutboundMsg
	err  error
}

func (d *stubDispatcher) Send(_ context.Context, msg *models.OutboundMsg) (*models.InboundAck, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = append(d.sent, msg)
	if d.err != nil {
		return &models.InboundAck{Accepted: false, Code: 502, Error: d.err.Error()}, d.err
	}
	return &models.InboundAck{Accepted: true, Code: 0}, nil
}

func (d *stubDispatcher) Close() error { return nil }

func (d *stubDispatcher) lastSent() *models.OutboundMsg {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.sent) == 0 {
		return nil
	}
	return d.sent[len(d.sent)-1]
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestInbound_NilPayload(t *testing.T) {
	srv := controllers.NewAgentServer("self", &stubSkills{}, nil, nil)
	ack, err := srv.Inbound(context.Background(), &models.InboundReq{TraceId: "t1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ack.Accepted || ack.Code != 400 {
		t.Errorf("expected rejected with 400, got accepted=%v code=%d", ack.Accepted, ack.Code)
	}
}

func TestInbound_NilReq(t *testing.T) {
	srv := controllers.NewAgentServer("self", &stubSkills{}, nil, nil)
	ack, err := srv.Inbound(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ack.Accepted || ack.Code != 400 {
		t.Errorf("expected rejected with 400, got accepted=%v code=%d", ack.Accepted, ack.Code)
	}
}

func TestInbound_EmptyMessages(t *testing.T) {
	srv := controllers.NewAgentServer("self", &stubSkills{}, nil, nil)
	ack, err := srv.Inbound(context.Background(), &models.InboundReq{
		TraceId: "t2",
		Payload: &models.AgentReq{AgentId: "demo"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ack.Accepted || ack.Code != 400 {
		t.Errorf("expected rejected, got accepted=%v code=%d", ack.Accepted, ack.Code)
	}
}

func TestInbound_MissingAgentID(t *testing.T) {
	srv := controllers.NewAgentServer("self", &stubSkills{}, nil, nil)
	ack, err := srv.Inbound(context.Background(), &models.InboundReq{
		TraceId: "t3",
		Payload: &models.AgentReq{
			Messages: []*models.Message{{Role: "user", Content: "hi"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ack.Accepted || ack.Code != 400 {
		t.Errorf("expected rejected with 400, got accepted=%v code=%d", ack.Accepted, ack.Code)
	}
}

func TestInbound_AcceptsValidRequest(t *testing.T) {
	srv := controllers.NewAgentServer("self", &stubSkills{}, nil, nil)
	ack, err := srv.Inbound(context.Background(), validReq("trace-ok"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ack.Accepted {
		t.Errorf("expected accepted=true, got %v (error=%s)", ack.Accepted, ack.Error)
	}
	if ack.Code != 0 {
		t.Errorf("expected code=0, got %d", ack.Code)
	}
	if ack.MessageId == "" {
		t.Error("expected non-empty message_id")
	}
	if ack.TraceId != "trace-ok" {
		t.Errorf("trace_id passthrough failed: got %q", ack.TraceId)
	}
}

func TestInbound_MessageIDUnique(t *testing.T) {
	srv := controllers.NewAgentServer("self", &stubSkills{}, nil, nil)
	ack1, _ := srv.Inbound(context.Background(), validReq("t1"))
	ack2, _ := srv.Inbound(context.Background(), validReq("t2"))
	if ack1.MessageId == ack2.MessageId {
		t.Errorf("message IDs should be unique, both = %s", ack1.MessageId)
	}
}

// TestInbound_FullPipeline verifies the end-to-end goroutine:
// SkillPack (no-op) → LLM Chat → Dispatcher.Send
func TestInbound_FullPipeline(t *testing.T) {
	llmCalled := make(chan services.ChatRequest, 1)
	llm := &stubLLM{
		resp: &services.ChatResponse{
			Content:      "Hello from LLM",
			FinishReason: "stop",
			Usage:        services.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
		called: llmCalled,
	}
	disp := &stubDispatcher{}

	srv := controllers.NewAgentServer("agent-self", &stubSkills{}, llm, disp)

	req := &models.InboundReq{
		TraceId: "e2e-trace",
		From:    "agent-b",
		Payload: &models.AgentReq{
			SessionId: "sess-1",
			AgentId:   "demo",
			Messages:  []*models.Message{{Role: "user", Content: "ping"}},
		},
	}

	ack, err := srv.Inbound(context.Background(), req)
	if err != nil {
		t.Fatalf("Inbound error: %v", err)
	}
	if !ack.Accepted {
		t.Fatalf("not accepted: %s", ack.Error)
	}

	// Wait for async goroutine (LLM is instant in stub)
	select {
	case chatReq := <-llmCalled:
		// Verify user message passed through
		found := false
		for _, m := range chatReq.Messages {
			if m.Role == services.RoleUser && m.Content == "ping" {
				found = true
			}
		}
		if !found {
			t.Errorf("user message 'ping' not found in ChatRequest.Messages: %+v", chatReq.Messages)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: LLM.Chat was not called within 3s")
	}

	// Give dispatcher a moment to be called after LLM returns
	time.Sleep(50 * time.Millisecond)

	sent := disp.lastSent()
	if sent == nil {
		t.Fatal("dispatcher.Send was not called")
	}
	if sent.To != "agent-b" {
		t.Errorf("OutboundMsg.To = %q, want %q", sent.To, "agent-b")
	}
	if sent.Direction != "reply" {
		t.Errorf("OutboundMsg.Direction = %q, want %q", sent.Direction, "reply")
	}
	if sent.From != "agent-self" {
		t.Errorf("OutboundMsg.From = %q, want %q", sent.From, "agent-self")
	}
	if len(sent.Payload.Messages) == 0 || sent.Payload.Messages[0].Content != "Hello from LLM" {
		t.Errorf("reply content wrong: %+v", sent.Payload.Messages)
	}
	if sent.Payload.SessionId != "sess-1" {
		t.Errorf("session_id not propagated: %q", sent.Payload.SessionId)
	}
}

// TestInbound_SkillPackUsedAsSystemPrompt verifies that SkillPack.Body is parsed
// and prepended as a system message.
func TestInbound_SkillPackUsedAsSystemPrompt(t *testing.T) {
	skillBody := "# TestBot\n## Identity\n- role: Test Assistant\n"
	skills := &stubSkills{
		pack: &models.SkillPack{AgentID: "demo", Body: skillBody},
	}
	llmCalled := make(chan services.ChatRequest, 1)
	llm := &stubLLM{
		resp:   &services.ChatResponse{Content: "ok"},
		called: llmCalled,
	}

	srv := controllers.NewAgentServer("self", skills, llm, nil)
	_, _ = srv.Inbound(context.Background(), validReq("skill-trace"))

	select {
	case chatReq := <-llmCalled:
		if len(chatReq.Messages) == 0 {
			t.Fatal("no messages sent to LLM")
		}
		first := chatReq.Messages[0]
		if first.Role != services.RoleSystem {
			t.Errorf("first message should be system, got %q", first.Role)
		}
		if first.Content == "" {
			t.Error("system prompt should not be empty when SkillPack has body")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for LLM call")
	}
}

// TestInbound_SkillNotFoundProceedsWithoutSystemPrompt verifies that missing SkillPack
// does not block inference — proceeds with user messages only.
func TestInbound_SkillNotFoundProceedsWithoutSystemPrompt(t *testing.T) {
	skills := &stubSkills{loadErr: errors.New("skill dir not found")}
	llmCalled := make(chan services.ChatRequest, 1)
	llm := &stubLLM{
		resp:   &services.ChatResponse{Content: "ok"},
		called: llmCalled,
	}

	srv := controllers.NewAgentServer("self", skills, llm, nil)
	_, _ = srv.Inbound(context.Background(), validReq("nosk-trace"))

	select {
	case chatReq := <-llmCalled:
		// should have exactly 1 message (user), no system
		for _, m := range chatReq.Messages {
			if m.Role == services.RoleSystem {
				t.Errorf("unexpected system message when no SkillPack: %q", m.Content)
			}
		}
		if len(chatReq.Messages) != 1 {
			t.Errorf("expected 1 user message, got %d", len(chatReq.Messages))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for LLM call")
	}
}

// TestInbound_LLMErrorSendsErrorReply verifies that LLM failure still triggers
// a dispatch with an error message.
func TestInbound_LLMErrorSendsErrorReply(t *testing.T) {
	llm := &stubLLM{err: errors.New("model overloaded")}
	disp := &stubDispatcher{}

	srv := controllers.NewAgentServer("self", &stubSkills{}, llm, disp)
	_, _ = srv.Inbound(context.Background(), &models.InboundReq{
		TraceId: "err-trace",
		From:    "upstream",
		Payload: &models.AgentReq{
			AgentId:  "demo",
			Messages: []*models.Message{{Role: "user", Content: "hi"}},
		},
	})

	time.Sleep(100 * time.Millisecond)
	sent := disp.lastSent()
	if sent == nil {
		t.Fatal("expected error reply to be dispatched, but nothing was sent")
	}
	if len(sent.Payload.Messages) == 0 {
		t.Fatal("expected error content in reply")
	}
	content := sent.Payload.Messages[0].Content
	if content == "" {
		t.Error("error reply content should not be empty")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func validReq(traceID string) *models.InboundReq {
	return &models.InboundReq{
		TraceId: traceID,
		From:    "agent-b",
		Payload: &models.AgentReq{
			SessionId: "sess-1",
			AgentId:   "demo",
			Messages:  []*models.Message{{Role: "user", Content: "hello"}},
		},
	}
}
