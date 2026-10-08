package controllers

// White-box tests for reviseWithCritic — internal (package controllers) since
// it and the AgentServer.critic/criticMaxRev fields are unexported.

import (
	"context"
	"testing"

	"agent/app/services"
)

type fakeCritic struct {
	calls []struct {
		question, answer string
		revision         int
	}
	// evals[i] is returned on the i-th call (in order); last entry repeats if
	// more calls happen than entries provided.
	evals []*services.CriticEvaluation
	err   error
}

func (f *fakeCritic) Evaluate(_ context.Context, _, _, question, answer string, _ []string, revision int) (*services.CriticEvaluation, error) {
	f.calls = append(f.calls, struct {
		question, answer string
		revision         int
	}{question, answer, revision})
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.evals) {
		idx = len(f.evals) - 1
	}
	return f.evals[idx], nil
}

func newTestServer() *AgentServer {
	def := services.NewOllamaClient(services.OllamaConfig{Model: "default-model"})
	return NewAgentServer("self", nil, def, nil)
}

func TestReviseWithCritic_NilCriticIsNoop(t *testing.T) {
	srv := newTestServer()
	got := srv.reviseWithCritic(context.Background(), "trace", "agent-a", "sess", "q", "src", "original answer",
		nil, func([]services.ChatMessage) (string, error) { t.Fatal("generate should not be called"); return "", nil })
	if got != "original answer" {
		t.Errorf("got %q, want unchanged original answer", got)
	}
}

func TestReviseWithCritic_PassesOnFirstTry_NoRegenerate(t *testing.T) {
	critic := &fakeCritic{evals: []*services.CriticEvaluation{{Passed: true, Overall: 0.9}}}
	srv := newTestServer().WithCritic(critic, 3)

	regenerateCalled := false
	got := srv.reviseWithCritic(context.Background(), "trace", "agent-a", "sess", "q", "src", "good answer",
		nil, func([]services.ChatMessage) (string, error) { regenerateCalled = true; return "revised", nil })

	if got != "good answer" {
		t.Errorf("got %q, want original answer unchanged", got)
	}
	if regenerateCalled {
		t.Error("generate should not be called when the first evaluation already passes")
	}
	if len(critic.calls) != 1 || critic.calls[0].revision != 0 {
		t.Errorf("expected exactly 1 evaluate call at revision=0, got %+v", critic.calls)
	}
}

func TestReviseWithCritic_RegeneratesUntilPassOrLimit(t *testing.T) {
	critic := &fakeCritic{evals: []*services.CriticEvaluation{
		{Passed: false, Overall: 0.3, Feedback: []string{"missing grounding"}},
		{Passed: true, Overall: 0.85},
	}}
	srv := newTestServer().WithCritic(critic, 2)

	genCalls := 0
	got := srv.reviseWithCritic(context.Background(), "trace", "agent-a", "sess", "q", "src", "v0",
		nil, func(msgs []services.ChatMessage) (string, error) {
			genCalls++
			// feedback must be appended as the last message for the LLM to see it
			last := msgs[len(msgs)-1]
			if last.Role != services.RoleUser {
				t.Errorf("expected feedback appended as user turn, got role=%s", last.Role)
			}
			return "v1", nil
		})

	if got != "v1" {
		t.Errorf("got %q, want v1 (the regenerated, passing answer)", got)
	}
	if genCalls != 1 {
		t.Errorf("generate called %d times, want 1 (should stop once passed)", genCalls)
	}
	if len(critic.calls) != 2 {
		t.Fatalf("expected 2 evaluate calls (v0 fails, v1 passes), got %d", len(critic.calls))
	}
	if critic.calls[0].revision != 0 || critic.calls[1].revision != 1 {
		t.Errorf("expected revision sequence 0,1; got %d,%d", critic.calls[0].revision, critic.calls[1].revision)
	}
}

func TestReviseWithCritic_StopsAtMaxRevisionsEvenIfStillFailing(t *testing.T) {
	failing := &services.CriticEvaluation{Passed: false, Overall: 0.1, Feedback: []string{"nope"}}
	critic := &fakeCritic{evals: []*services.CriticEvaluation{failing, failing, failing}}
	srv := newTestServer().WithCritic(critic, 1) // at most 1 regenerate attempt

	genCalls := 0
	got := srv.reviseWithCritic(context.Background(), "trace", "agent-a", "sess", "q", "src", "v0",
		nil, func([]services.ChatMessage) (string, error) {
			genCalls++
			return "v1", nil
		})

	if got != "v1" {
		t.Errorf("got %q, want v1 (last generated answer, even though it still fails)", got)
	}
	if genCalls != 1 {
		t.Errorf("generate called %d times, want exactly 1 (MaxRevisions=1)", genCalls)
	}
}

func TestReviseWithCritic_EvaluateErrorAcceptsCurrentAnswer(t *testing.T) {
	critic := &fakeCritic{err: context.DeadlineExceeded}
	srv := newTestServer().WithCritic(critic, 3)

	got := srv.reviseWithCritic(context.Background(), "trace", "agent-a", "sess", "q", "src", "answer",
		nil, func([]services.ChatMessage) (string, error) { t.Fatal("generate should not be called on evaluate error"); return "", nil })

	if got != "answer" {
		t.Errorf("got %q, want unchanged answer when evaluate errors", got)
	}
}

func TestReviseWithCritic_GenerateErrorKeepsPreviousAnswer(t *testing.T) {
	critic := &fakeCritic{evals: []*services.CriticEvaluation{{Passed: false, Feedback: []string{"x"}}}}
	srv := newTestServer().WithCritic(critic, 2)

	got := srv.reviseWithCritic(context.Background(), "trace", "agent-a", "sess", "q", "src", "v0",
		nil, func([]services.ChatMessage) (string, error) { return "", context.Canceled })

	if got != "v0" {
		t.Errorf("got %q, want v0 (kept when regenerate fails)", got)
	}
}
