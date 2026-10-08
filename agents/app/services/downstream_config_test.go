package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownstreamConfig_ApplyDefaults(t *testing.T) {
	c := &DownstreamConfig{
		Agents: []DownstreamAgentConfig{
			{ID: "a", Endpoint: "x:1"},
			{ID: "b", Endpoint: "y:1", DialTimeout: 7 * time.Second, CallTimeout: 9 * time.Second},
		},
	}
	c.ApplyDefaults()

	// Defaults 應被填補
	if c.Defaults.DialTimeout != 3*time.Second {
		t.Fatalf("default DialTimeout wrong: %v", c.Defaults.DialTimeout)
	}
	if c.Defaults.CallTimeout != 5*time.Second {
		t.Fatalf("default CallTimeout wrong: %v", c.Defaults.CallTimeout)
	}
	if c.Defaults.RetryBackoff != 200*time.Millisecond {
		t.Fatalf("default RetryBackoff wrong: %v", c.Defaults.RetryBackoff)
	}
	// Agent a：未指定 → cascade 自 Defaults
	if c.Agents[0].DialTimeout != 3*time.Second || c.Agents[0].CallTimeout != 5*time.Second {
		t.Fatalf("agent a cascade wrong: %+v", c.Agents[0])
	}
	// Agent b：已指定 → 不應被覆蓋
	if c.Agents[1].DialTimeout != 7*time.Second || c.Agents[1].CallTimeout != 9*time.Second {
		t.Fatalf("agent b not preserved: %+v", c.Agents[1])
	}
}

func TestDownstreamConfig_Validate(t *testing.T) {
	cases := []struct {
		name      string
		cfg       DownstreamConfig
		wantError bool
	}{
		{"ok", DownstreamConfig{Agents: []DownstreamAgentConfig{{ID: "a", Endpoint: "x:1"}}}, false},
		{"missing-id", DownstreamConfig{Agents: []DownstreamAgentConfig{{Endpoint: "x:1"}}}, true},
		{"missing-endpoint", DownstreamConfig{Agents: []DownstreamAgentConfig{{ID: "a"}}}, true},
		{"duplicate-id", DownstreamConfig{Agents: []DownstreamAgentConfig{
			{ID: "a", Endpoint: "x:1"}, {ID: "a", Endpoint: "y:2"},
		}}, true},
		{"tls-no-ca", DownstreamConfig{Agents: []DownstreamAgentConfig{
			{ID: "a", Endpoint: "x:1", TLSEnabled: true},
		}}, true},
		{"tls-with-skip-verify", DownstreamConfig{
			Defaults: DownstreamDefaults{InsecureSkipVerify: true},
			Agents:   []DownstreamAgentConfig{{ID: "a", Endpoint: "x:1", TLSEnabled: true}},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantError {
				t.Fatalf("want error=%v got %v", tc.wantError, err)
			}
		})
	}
}

func TestDownstreamConfig_Find(t *testing.T) {
	c := &DownstreamConfig{Agents: []DownstreamAgentConfig{
		{ID: "a", Endpoint: "x:1"},
		{ID: "b", Endpoint: "y:1"},
	}}
	if got, ok := c.Find("b"); !ok || got.Endpoint != "y:1" {
		t.Fatalf("Find(b) returned %+v ok=%v", got, ok)
	}
	if _, ok := c.Find("zzz"); ok {
		t.Fatal("Find(zzz) should miss")
	}
}

func TestLoadDownstreamConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ds.yaml")
	body := `
downstream:
  defaults:
    dial_timeout: 1s
    call_timeout: 2s
    max_retries: 4
    retry_backoff: 250ms
  agents:
    - id: "a1"
      endpoint: "10.0.0.1:9091"
      tags: ["primary"]
    - id: "a2"
      endpoint: "10.0.0.2:9091"
      call_timeout: 3s
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDownstreamConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 2 {
		t.Fatalf("want 2 agents, got %d", len(cfg.Agents))
	}
	if cfg.Defaults.MaxRetries != 4 || cfg.Defaults.RetryBackoff != 250*time.Millisecond {
		t.Fatalf("defaults parsed wrong: %+v", cfg.Defaults)
	}
	// cascade
	if cfg.Agents[0].CallTimeout != 2*time.Second {
		t.Fatalf("agent[0] should inherit call_timeout; got %v", cfg.Agents[0].CallTimeout)
	}
	if cfg.Agents[1].CallTimeout != 3*time.Second {
		t.Fatalf("agent[1] should preserve override; got %v", cfg.Agents[1].CallTimeout)
	}
}

func TestLoadDownstreamConfig_BadFile(t *testing.T) {
	if _, err := LoadDownstreamConfig("does/not/exist.yaml"); err == nil {
		t.Fatal("expected error for missing file")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(path, []byte("downstream:\n  agents:\n    - id: \"a\"\n"), 0644) // missing endpoint
	if _, err := LoadDownstreamConfig(path); err == nil {
		t.Fatal("expected validate error for missing endpoint")
	}
}
