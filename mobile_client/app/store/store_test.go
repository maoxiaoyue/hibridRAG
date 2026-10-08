package store

import (
	"os"
	"path/filepath"
	"testing"

	"mobile_client/app/models"
)

func setupStore(t *testing.T) {
	t.Helper()
	Init(t.TempDir())
}

func TestLoadConfig_DefaultOnFirstRun(t *testing.T) {
	setupStore(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AgentID != "agent-main" {
		t.Fatalf("want default agent-main, got %q", cfg.AgentID)
	}
	// 首次載入即落地
	if _, err := os.Stat(filepath.Join(BaseDir(), "config.enc")); err != nil {
		t.Fatalf("config.enc not persisted: %v", err)
	}
}

func TestSaveLoadConfig_Roundtrip(t *testing.T) {
	setupStore(t)
	cfg := models.DefaultConfig()
	cfg.GatewayAddr = "10.0.0.1:9090"
	cfg.APIKey = "sak_secret"
	cfg.DeviceID = "and_abc"
	cfg.Temperature = 0.7
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.GatewayAddr != cfg.GatewayAddr || got.APIKey != cfg.APIKey ||
		got.DeviceID != cfg.DeviceID || got.Temperature != cfg.Temperature {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestConfigFile_IsEncrypted(t *testing.T) {
	setupStore(t)
	cfg := models.DefaultConfig()
	cfg.APIKey = "sak_should_not_be_plaintext"
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(BaseDir(), "config.enc"))
	if err != nil {
		t.Fatalf("read config.enc: %v", err)
	}
	if containsSub(raw, []byte("sak_should_not_be_plaintext")) {
		t.Fatal("API key appears in plaintext on disk")
	}
}

func containsSub(hay, needle []byte) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestSessions_SaveListGet(t *testing.T) {
	setupStore(t)
	if err := SaveSession("s1", "agent-main", "**You:** hi"); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if err := SaveSession("s2", "agent-x", "**You:** hello there"); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	metas, err := ListSessions(10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(metas))
	}
	// 新的在前
	if metas[0].SessionID != "s2" {
		t.Fatalf("want s2 first, got %s", metas[0].SessionID)
	}
	tr, err := GetSession("s1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if tr != "**You:** hi" {
		t.Fatalf("transcript mismatch: %q", tr)
	}
	if _, err := GetSession("nope"); err == nil {
		t.Fatal("want error for missing session")
	}
}

func TestSaveSession_EmptyIDRejected(t *testing.T) {
	setupStore(t)
	if err := SaveSession("  ", "a", "x"); err == nil {
		t.Fatal("want error for empty session id")
	}
}
