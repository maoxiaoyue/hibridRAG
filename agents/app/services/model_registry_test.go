package services

import "testing"

func TestResolveRolesPreferLoaded(t *testing.T) {
	reg := DefaultModelRegistry()

	cases := map[ModelRole]string{
		RoleChat:             "gemma4:12b",
		RoleChatFast:         "gemma4:e4b",
		RoleChatQuality:      "qwen3.5:35b",
		RoleChatLong:         "gemma4-64k:latest",
		RoleToolCalling:      "qwen3.5:9b",
		RoleVision:           "qwen3-vl:4b",
		RoleEmbedding:        "bge-m3:latest",
		RoleChunking:         "gemma4:e4b",
		RoleUncensored:       "qwen3-14b-un:latest",
	}
	for role, want := range cases {
		if got := reg.ModelFor(role); got != want {
			t.Errorf("ModelFor(%q) = %q, want %q", role, got, want)
		}
	}
}

func TestResolveSkipsUnloadedPreference(t *testing.T) {
	reg := DefaultModelRegistry()

	// Reasoning 首選 Mellum2（未載入）→ 應跳過，回第一顆已載入偏好（qwen3.5:35b）。
	m, err := reg.Resolve(RoleReasoning)
	if err != nil {
		t.Fatalf("Resolve(reasoning) err: %v", err)
	}
	if m.Name != "qwen3.5:35b" {
		t.Errorf("reasoning resolved to %q, want first *loaded* preference qwen3.5:35b", m.Name)
	}
	if m.ResolvedHost() != DefaultOllamaHost {
		t.Errorf("host = %q, want %q", m.ResolvedHost(), DefaultOllamaHost)
	}
}

func TestResolveFallsBackToFirstWhenNoneLoaded(t *testing.T) {
	// 自訂清單：全部偏好皆未載入 → 應回第一順位（呼叫端可決定 pull）。
	catalog := []ModelSpec{
		{Name: "alpha", Capabilities: CapChat, Loaded: false},
		{Name: "beta", Capabilities: CapChat, Loaded: false},
	}
	prefs := map[ModelRole][]string{RoleChat: {"alpha", "beta"}}
	reg := NewModelRegistry(catalog, prefs)

	m, err := reg.Resolve(RoleChat)
	if err != nil {
		t.Fatalf("Resolve err: %v", err)
	}
	if m.Name != "alpha" {
		t.Errorf("resolved to %q, want first preference alpha", m.Name)
	}
	if m.ResolvedHost() != DefaultOllamaHost {
		t.Errorf("host = %q, want default %q", m.ResolvedHost(), DefaultOllamaHost)
	}
}

func TestResolveUnknownRole(t *testing.T) {
	reg := DefaultModelRegistry()
	if _, err := reg.Resolve("does_not_exist"); err == nil {
		t.Error("expected error for unknown role")
	}
}

func TestListByCapability(t *testing.T) {
	reg := DefaultModelRegistry()

	vision := reg.ListByCapability(CapVision, true)
	if len(vision) < 3 {
		t.Fatalf("expected >=3 loaded vision models, got %d", len(vision))
	}
	// 應依尺寸由小到大；首個須為已載入視覺模型。
	for i := 1; i < len(vision); i++ {
		if vision[i].SizeGB < vision[i-1].SizeGB {
			t.Errorf("ListByCapability not sorted by size: %v", vision)
			break
		}
	}
	for _, m := range vision {
		if !m.Capabilities.Has(CapVision) || !m.Loaded {
			t.Errorf("unexpected model in loaded-vision list: %+v", m)
		}
	}

	// 含未載入：uncensored 視覺模型應出現。
	allVision := reg.ListByCapability(CapVision, false)
	if len(allVision) <= len(vision) {
		t.Errorf("expected unloaded vision model included, loaded=%d all=%d", len(vision), len(allVision))
	}
}

func TestOllamaConfigFor(t *testing.T) {
	reg := DefaultModelRegistry()
	cfg, err := reg.OllamaConfigFor(RoleToolCalling, 30, 4096)
	if err != nil {
		t.Fatalf("OllamaConfigFor err: %v", err)
	}
	if cfg.Model != "qwen3.5:9b" {
		t.Errorf("model = %q, want qwen3.5:9b", cfg.Model)
	}
	if cfg.URL != DefaultOllamaHost {
		t.Errorf("url = %q, want %q", cfg.URL, DefaultOllamaHost)
	}
	if cfg.Timeout != 30 || cfg.MaxTokens != 4096 {
		t.Errorf("timeout/maxtokens = %d/%d, want 30/4096", cfg.Timeout, cfg.MaxTokens)
	}
}

func TestVisionExtractorForRejectsNonVision(t *testing.T) {
	reg := DefaultModelRegistry()
	if _, err := reg.VisionExtractorFor(RoleChat, "", 60); err == nil {
		t.Error("expected error resolving vision extractor to a non-vision role")
	}
	if _, err := reg.VisionExtractorFor(RoleVision, "", 60); err != nil {
		t.Errorf("RoleVision should build extractor: %v", err)
	}
}

func TestApplyToOllamaConfig(t *testing.T) {
	reg := DefaultModelRegistry()

	// 1) 明示 model → 不動，URL 空則補預設 host
	cfg := OllamaConfig{Model: "qwen3.5:35b"}
	if err := reg.ApplyToOllamaConfig(&cfg, RoleChat); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if cfg.Model != "qwen3.5:35b" || cfg.URL != DefaultOllamaHost {
		t.Errorf("explicit model not preserved: %+v", cfg)
	}

	// 2) model 空 + role 設定 → registry 解析
	cfg = OllamaConfig{Role: "tool_calling"}
	if err := reg.ApplyToOllamaConfig(&cfg, RoleChat); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if cfg.Model != "qwen3.5:9b" {
		t.Errorf("role=tool_calling → model %q, want qwen3.5:9b", cfg.Model)
	}

	// 3) model 空 + role 空 → 用 defaultRole
	cfg = OllamaConfig{}
	if err := reg.ApplyToOllamaConfig(&cfg, RoleChatFast); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if cfg.Model != "gemma4:e4b" {
		t.Errorf("defaultRole=chat_fast → model %q, want gemma4:e4b", cfg.Model)
	}

	// 4) 全空 + defaultRole 空 → 不動（讓 NewOllamaClient 套硬編碼 fallback）
	cfg = OllamaConfig{}
	if err := reg.ApplyToOllamaConfig(&cfg, ""); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if cfg.Model != "" {
		t.Errorf("no role + no default → model should remain empty, got %q", cfg.Model)
	}
}

func TestCapabilityString(t *testing.T) {
	c := CapChat | CapTools
	if !c.Has(CapChat) || !c.Has(CapTools) || c.Has(CapVision) {
		t.Errorf("Has() wrong for %v", c)
	}
	if c.String() != "chat|tools" {
		t.Errorf("String() = %q, want chat|tools", c.String())
	}
}
