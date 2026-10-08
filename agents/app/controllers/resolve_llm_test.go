package controllers

// White-box tests for resolveLLM — internal (package controllers, not
// controllers_test) since resolveLLM itself is unexported.

import (
	"testing"

	"agent/app/services"
)

type fakeModelResolver struct {
	res map[string]*services.ModelResolution
}

func (f *fakeModelResolver) ModelResolutionFor(agentID string) (*services.ModelResolution, bool) {
	r, ok := f.res[agentID]
	return r, ok
}

func TestResolveLLM_FallsBackWhenResolverOrPoolNil(t *testing.T) {
	def := services.NewOllamaClient(services.OllamaConfig{Model: "default-model"})
	srv := NewAgentServer("self", nil, def, nil)

	if got := srv.resolveLLM("agent-a"); got != def {
		t.Error("expected fallback to s.llm when modelResolver/llmPool both nil")
	}

	srv.WithModelResolver(&fakeModelResolver{res: map[string]*services.ModelResolution{}})
	if got := srv.resolveLLM("agent-a"); got != def {
		t.Error("expected fallback to s.llm when llmPool is still nil even with modelResolver set")
	}
}

func TestResolveLLM_FallsBackWhenNoResolutionOrEmptyModel(t *testing.T) {
	def := services.NewOllamaClient(services.OllamaConfig{Model: "default-model"})
	pool := services.NewLLMClientPool(services.OllamaConfig{URL: "http://x", Model: "ignored"})
	srv := NewAgentServer("self", nil, def, nil).
		WithModelResolver(&fakeModelResolver{res: map[string]*services.ModelResolution{
			"agent-b": {Model: ""}, // present but empty model → still fallback
		}}).
		WithLLMPool(pool)

	if got := srv.resolveLLM("agent-unknown"); got != def {
		t.Error("expected fallback to s.llm for an agentID with no cached resolution")
	}
	if got := srv.resolveLLM("agent-b"); got != def {
		t.Error("expected fallback to s.llm when the cached resolution has an empty Model")
	}
}

func TestResolveLLM_UsesPoolWhenResolutionHasModel(t *testing.T) {
	def := services.NewOllamaClient(services.OllamaConfig{Model: "default-model"})
	pool := services.NewLLMClientPool(services.OllamaConfig{URL: "http://x"})
	srv := NewAgentServer("self", nil, def, nil).
		WithModelResolver(&fakeModelResolver{res: map[string]*services.ModelResolution{
			"agent-c": {Model: "special-model"},
		}}).
		WithLLMPool(pool)

	got := srv.resolveLLM("agent-c")
	if got == def {
		t.Error("expected a pool-resolved client, not the default s.llm")
	}
	if want := pool.Get("special-model"); got != want {
		t.Error("expected the same cached pool client on repeated resolution")
	}
}
