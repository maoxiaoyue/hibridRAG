package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"agent/app/models"
)

// fakeSkillSource 回傳固定 SKILL.md body 的 SkillPackSource。
type fakeSkillSource struct {
	body string
	err  error
}

func (f *fakeSkillSource) Load(string, bool) (*models.SkillPack, bool, []string, error) {
	if f.err != nil {
		return nil, false, nil, f.err
	}
	return &models.SkillPack{Body: f.body}, true, nil, nil
}

// registryStub 記錄收到的註冊/反註冊請求。
type registryStub struct {
	mu          sync.Mutex
	registers   []registerAgentPayload
	deregisters []string
}

func (s *registryStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/registry/register", func(w http.ResponseWriter, r *http.Request) {
		var p registerAgentPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.registers = append(s.registers, p)
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/registry/agents/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.mu.Lock()
			s.deregisters = append(s.deregisters, r.URL.Path)
			s.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	return mux
}

func (s *registryStub) registerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.registers)
}

const testSkillMD = `# 小測
## Identity
- role: 測試員
- domain: QA
## Mental Models
### 先驗證後實作
內容
`

func TestRegisterOncePayload(t *testing.T) {
	stub := &registryStub{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	builder := NewAgentCardBuilder("agent-x", "0.1.0", "https://localhost:8081/")
	r := NewRegistryRegistrar(RegistrarOptions{
		RegistryURL:  srv.URL,
		AgentID:      "agent-x",
		GRPCEndpoint: "localhost:9091",
	}, builder, &fakeSkillSource{body: testSkillMD})

	if err := r.RegisterOnce(context.Background()); err != nil {
		t.Fatalf("RegisterOnce: %v", err)
	}
	if stub.registerCount() != 1 {
		t.Fatalf("register count = %d, want 1", stub.registerCount())
	}
	p := stub.registers[0]
	if p.ID != "agent-x" {
		t.Errorf("payload.ID = %q, want agent-x", p.ID)
	}
	if p.GRPCEndpoint != "localhost:9091" {
		t.Errorf("payload.GRPCEndpoint = %q", p.GRPCEndpoint)
	}
	if p.URL == "" {
		t.Error("payload.URL empty; should fall back to card URL")
	}
	if len(p.Skills) == 0 {
		t.Fatal("payload.Skills empty; expected skills derived from SKILL.md mental models")
	}
	if p.Skills[0].Name != "先驗證後實作" {
		t.Errorf("skill name = %q, want 先驗證後實作", p.Skills[0].Name)
	}
}

func TestRegisterOnceFallsBackWithoutSkillPack(t *testing.T) {
	stub := &registryStub{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	builder := NewAgentCardBuilder("agent-y", "0.1.0", "")
	r := NewRegistryRegistrar(RegistrarOptions{
		RegistryURL: srv.URL,
		AgentID:     "agent-y",
	}, builder, &fakeSkillSource{err: ErrNotFound})

	if err := r.RegisterOnce(context.Background()); err != nil {
		t.Fatalf("RegisterOnce: %v", err)
	}
	p := stub.registers[0]
	if len(p.Skills) == 0 {
		t.Fatal("expected fallback default skill when no SkillPack available")
	}
}

func TestHeartbeatAndDeregisterOnStop(t *testing.T) {
	stub := &registryStub{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	builder := NewAgentCardBuilder("agent-z", "0.1.0", "https://localhost:8081/")
	r := NewRegistryRegistrar(RegistrarOptions{
		RegistryURL:  srv.URL,
		AgentID:      "agent-z",
		HeartbeatSec: 1, // 最小心跳間隔
	}, builder, &fakeSkillSource{body: testSkillMD})

	r.Start()
	// 等到至少 2 次註冊（啟動立即 1 次 + 心跳 >= 1 次）
	deadline := time.Now().Add(3 * time.Second)
	for stub.registerCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := stub.registerCount(); got < 2 {
		t.Fatalf("register count = %d, want >= 2 (startup + heartbeat)", got)
	}

	r.Stop()
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.deregisters) != 1 {
		t.Fatalf("deregister count = %d, want 1", len(stub.deregisters))
	}
	if want := "/registry/agents/agent-z"; stub.deregisters[0] != want {
		t.Errorf("deregister path = %q, want %q", stub.deregisters[0], want)
	}
}

func TestNewRegistrarDisabledWithoutURL(t *testing.T) {
	if r := NewRegistryRegistrar(RegistrarOptions{AgentID: "x"}, nil, nil); r != nil {
		t.Error("expected nil registrar when RegistryURL is empty")
	}
}
