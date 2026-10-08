package persona_test

import (
	"strings"
	"testing"

	"agent/internal/agent/persona"
)

const fullSkillMD = `# My Customer Agent

## Identity
- name: SupportBot
- role: Customer Support Specialist
- domain: E-commerce
- language: zh-TW

## Expression DNA
- tone: warm yet professional
- style: concise, structured
- avoid: jargon, passive voice

## Mental Models

### First Principles Thinking
Apply first principles to break down complex problems into
their atomic components before synthesising a solution.

**Heuristics**
- challenge_assumptions: Ask "why" at least 3 times
- simplify: Break problems into smallest atomic units

### Customer Empathy
Always consider the emotional state of the customer
before jumping to a technical solution.

**Heuristics**
- acknowledge_first: Acknowledge feelings before solving
- solution_focus: Guide toward solutions, not problems
`

func TestParse_Full(t *testing.T) {
	r := persona.Parse(fullSkillMD)

	if r.AgentName != "My Customer Agent" {
		t.Errorf("AgentName = %q, want %q", r.AgentName, "My Customer Agent")
	}

	// Identity
	assertStr(t, "Identity.name", r.Identity, "name", "SupportBot")
	assertStr(t, "Identity.role", r.Identity, "role", "Customer Support Specialist")
	assertStr(t, "Identity.domain", r.Identity, "domain", "E-commerce")

	// Expression DNA
	assertStr(t, "ExpressionDNA.tone", r.ExpressionDNA, "tone", "warm yet professional")
	assertStr(t, "ExpressionDNA.style", r.ExpressionDNA, "style", "concise, structured")

	// Mental Models
	if len(r.MentalModels) != 2 {
		t.Fatalf("len(MentalModels) = %d, want 2", len(r.MentalModels))
	}

	m0 := r.MentalModels[0]
	if m0.Name != "First Principles Thinking" {
		t.Errorf("MentalModels[0].Name = %q", m0.Name)
	}
	if m0.Content == "" {
		t.Error("MentalModels[0].Content should not be empty")
	}
	assertStr(t, "MentalModels[0].Heuristics.simplify", m0.Heuristics, "simplify", "Break problems into smallest atomic units")

	m1 := r.MentalModels[1]
	if m1.Name != "Customer Empathy" {
		t.Errorf("MentalModels[1].Name = %q", m1.Name)
	}
	assertStr(t, "MentalModels[1].Heuristics.acknowledge_first", m1.Heuristics, "acknowledge_first", "Acknowledge feelings before solving")
}

func TestParse_MinimalIdentityOnly(t *testing.T) {
	md := `# Minimal Agent

## Identity
- role: Greeter
- domain: HR
`
	r := persona.Parse(md)
	if r.AgentName != "Minimal Agent" {
		t.Errorf("AgentName = %q", r.AgentName)
	}
	assertStr(t, "Identity.role", r.Identity, "role", "Greeter")
	if len(r.ExpressionDNA) != 0 {
		t.Errorf("ExpressionDNA should be empty, got %v", r.ExpressionDNA)
	}
	if len(r.MentalModels) != 0 {
		t.Errorf("MentalModels should be empty, got %v", r.MentalModels)
	}
}

func TestParse_NoH1(t *testing.T) {
	md := `## Identity
- name: Nameless
`
	r := persona.Parse(md)
	if r.AgentName != "" {
		t.Errorf("AgentName should be empty when no H1, got %q", r.AgentName)
	}
	assertStr(t, "Identity.name", r.Identity, "name", "Nameless")
}

func TestParse_EmptyBody(t *testing.T) {
	r := persona.Parse("")
	if !r.IsEmpty() {
		t.Error("empty body should produce IsEmpty() == true")
	}
}

func TestParse_ChineseSection(t *testing.T) {
	md := `# 中文Agent

## 身份卡
- 名稱: 助理
- 角色: 客服

## 心智模型

### 同理心模型
關注客戶情感狀態。

**決策啟發式**
- 先理解: 先確認問題再給答案
`
	r := persona.Parse(md)
	if r.AgentName != "中文Agent" {
		t.Errorf("AgentName = %q", r.AgentName)
	}
	if _, ok := r.Identity["名稱"]; !ok {
		t.Errorf("Identity should have key '名稱', got %v", r.Identity)
	}
	if len(r.MentalModels) != 1 {
		t.Fatalf("len(MentalModels) = %d, want 1", len(r.MentalModels))
	}
	if r.MentalModels[0].Name != "同理心模型" {
		t.Errorf("MentalModels[0].Name = %q", r.MentalModels[0].Name)
	}
}

func TestParse_MentalModelWithoutHeuristics(t *testing.T) {
	md := `# Agent

## Mental Models

### Simple Model
Just a description, no heuristics section here.
`
	r := persona.Parse(md)
	if len(r.MentalModels) != 1 {
		t.Fatalf("len(MentalModels) = %d, want 1", len(r.MentalModels))
	}
	m := r.MentalModels[0]
	if m.Content == "" {
		t.Error("Content should not be empty")
	}
	if len(m.Heuristics) != 0 {
		t.Errorf("Heuristics should be nil/empty, got %v", m.Heuristics)
	}
}

func TestParse_ModelSection_RoleOnly(t *testing.T) {
	md := `# Agent

## Model
- role: chat_quality
`
	r := persona.Parse(md)
	if r.ModelPref == nil {
		t.Fatal("expected non-nil ModelPref")
	}
	if r.ModelPref.Role != "chat_quality" {
		t.Errorf("ModelPref.Role = %q, want %q", r.ModelPref.Role, "chat_quality")
	}
	if r.ModelPref.Model != "" {
		t.Errorf("ModelPref.Model = %q, want empty", r.ModelPref.Model)
	}
}

func TestParse_ModelSection_ExplicitModelOverride(t *testing.T) {
	md := `# Agent

## Model
- role: chat_quality
- model: qwen3.5:35b
`
	r := persona.Parse(md)
	if r.ModelPref == nil {
		t.Fatal("expected non-nil ModelPref")
	}
	if r.ModelPref.Role != "chat_quality" || r.ModelPref.Model != "qwen3.5:35b" {
		t.Errorf("ModelPref = %+v", r.ModelPref)
	}
}

func TestParse_ModelSection_ChineseAlias(t *testing.T) {
	md := `# Agent

## 模型設定
- role: tool_calling
`
	r := persona.Parse(md)
	if r.ModelPref == nil || r.ModelPref.Role != "tool_calling" {
		t.Fatalf("ModelPref = %+v", r.ModelPref)
	}
}

func TestParse_NoModelSection_NilPref(t *testing.T) {
	r := persona.Parse(fullSkillMD)
	if r.ModelPref != nil {
		t.Errorf("expected nil ModelPref when SKILL.md has no ## Model section, got %+v", r.ModelPref)
	}
}

func TestParse_ModelSection_DoesNotCollideWithMentalModelsAlias(t *testing.T) {
	// "模型" alone means Mental Models (existing Chinese alias); only "模型設定"
	// should be recognized as the new Model-preference section.
	md := `# Agent

## 模型
### 一個心智模型
內容。
`
	r := persona.Parse(md)
	if r.ModelPref != nil {
		t.Errorf("expected nil ModelPref, '模型' alone should still mean Mental Models, got %+v", r.ModelPref)
	}
	if len(r.MentalModels) != 1 {
		t.Errorf("expected 1 MentalModel via '模型' alias, got %d", len(r.MentalModels))
	}
}

func TestToInstruction_ExcludesModelPref(t *testing.T) {
	md := `# Agent

## Identity
- name: Bot
- role: Helper

## Model
- role: chat_quality
- model: qwen3.5:35b
`
	r := persona.Parse(md)
	instruction := r.ToInstruction()
	if strings.Contains(instruction, "chat_quality") || strings.Contains(instruction, "qwen3.5:35b") {
		t.Errorf("ToInstruction() leaked ModelPref into system prompt: %q", instruction)
	}
}

func TestToPersonalMemoryRequest(t *testing.T) {
	r := persona.Parse(fullSkillMD)
	req := r.ToPersonalMemoryRequest("agent-001")
	if req == nil {
		t.Fatal("expected non-nil request")
	}
	if req.AgentID != "agent-001" {
		t.Errorf("AgentID = %q", req.AgentID)
	}
	if _, ok := req.Identity["name"]; !ok {
		t.Error("Identity should contain 'name'")
	}
	if len(req.ExpressionDNA) == 0 {
		t.Error("ExpressionDNA should not be empty")
	}
}

func TestToPersonalMemoryRequest_EmptyResult(t *testing.T) {
	r := persona.Parse("")
	req := r.ToPersonalMemoryRequest("agent-001")
	if req != nil {
		t.Error("expected nil request when no identity or expression dna")
	}
}

func TestToProceduralMemoryRequests(t *testing.T) {
	r := persona.Parse(fullSkillMD)
	reqs := r.ToProceduralMemoryRequests("agent-001")
	if len(reqs) != 2 {
		t.Fatalf("len(reqs) = %d, want 2", len(reqs))
	}
	if reqs[0].AgentID != "agent-001" {
		t.Errorf("AgentID = %q", reqs[0].AgentID)
	}
	if reqs[0].ModelName != "First Principles Thinking" {
		t.Errorf("ModelName = %q", reqs[0].ModelName)
	}
	if reqs[0].Content == "" {
		t.Error("Content should not be empty")
	}
	if len(reqs[0].Heuristics) == 0 {
		t.Error("Heuristics should not be empty")
	}
}

func TestToPersonalMemoryRequest_AgentNameFallback(t *testing.T) {
	md := `# FallbackBot

## Identity
- role: Helper
`
	r := persona.Parse(md)
	req := r.ToPersonalMemoryRequest("a1")
	// name 應從 H1 填入（Identity 中未設定 name）
	if req.Identity["name"] != "FallbackBot" {
		t.Errorf("expected Identity[name]=FallbackBot, got %v", req.Identity["name"])
	}
}

// ---- ToInstruction tests ----

func TestToInstruction_Full(t *testing.T) {
	r := persona.Parse(fullSkillMD)
	out := r.ToInstruction()

	// 必須包含的關鍵片段（不依賴完整字串比對，避免格式化變動就壞）
	mustContain := []string{
		"You are SupportBot, Customer Support Specialist.",
		"Your domain is E-commerce.",
		"## About You",
		"- language: zh-TW",
		"## Communication Style",
		"- tone: warm yet professional",
		"- style: concise, structured",
		"## Mental Models",
		"### First Principles Thinking",
		"Apply first principles",
		"Heuristics:",
		"- challenge_assumptions: Ask \"why\" at least 3 times",
		"### Customer Empathy",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("ToInstruction() missing %q\n--- output ---\n%s", s, out)
		}
	}
}

func TestToInstruction_Empty(t *testing.T) {
	r := persona.Parse("")
	if got := r.ToInstruction(); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestToInstruction_OnlyIdentity(t *testing.T) {
	md := `# Slim Agent
## Identity
- name: SlimBot
- role: Greeter
`
	out := persona.Parse(md).ToInstruction()
	if !strings.Contains(out, "You are SlimBot, Greeter.") {
		t.Errorf("opening line wrong:\n%s", out)
	}
	// 沒有其他 section 應不出現
	for _, s := range []string{"## About You", "## Communication Style", "## Mental Models"} {
		if strings.Contains(out, s) {
			t.Errorf("unexpected section %q present:\n%s", s, out)
		}
	}
}

func TestToInstruction_ChineseAliases(t *testing.T) {
	md := `# 中文Agent

## 身份卡
- 名稱: 助理
- 角色: 客服
- 領域: 電商

## 心智模型

### 同理心模型
關注客戶情感狀態。

**決策啟發式**
- 先理解: 先確認問題再給答案
`
	out := persona.Parse(md).ToInstruction()

	// 中文 alias 應正確映射到 opening
	if !strings.Contains(out, "You are 助理, 客服.") {
		t.Errorf("expected Chinese name+role mapped:\n%s", out)
	}
	if !strings.Contains(out, "Your domain is 電商.") {
		t.Errorf("expected Chinese domain mapped:\n%s", out)
	}
	if !strings.Contains(out, "### 同理心模型") {
		t.Errorf("expected Chinese mental model name:\n%s", out)
	}
	if !strings.Contains(out, "- 先理解: 先確認問題再給答案") {
		t.Errorf("expected Chinese heuristic:\n%s", out)
	}
}

func TestToInstruction_AgentNameFallback(t *testing.T) {
	md := `# FallbackBot

## Identity
- role: Helper
`
	out := persona.Parse(md).ToInstruction()
	// Identity 沒給 name，應從 H1 取
	if !strings.Contains(out, "You are FallbackBot, Helper.") {
		t.Errorf("H1 should fall back into name slot:\n%s", out)
	}
}

func TestToInstruction_NoMentalHeuristics(t *testing.T) {
	md := `# Agent

## Identity
- name: Bot

## Mental Models

### Simple Model
Just a description.
`
	out := persona.Parse(md).ToInstruction()
	if !strings.Contains(out, "### Simple Model") {
		t.Errorf("expected model heading:\n%s", out)
	}
	if strings.Contains(out, "Heuristics:") {
		t.Errorf("should not have Heuristics: section when none defined:\n%s", out)
	}
}

func TestToInstruction_AboutYouOmitsOpeningKeys(t *testing.T) {
	md := `# Bot

## Identity
- name: A
- role: B
- domain: C
- language: zh-TW
- mood: cheerful
`
	out := persona.Parse(md).ToInstruction()
	// name/role/domain 已用於 opening，不應再出現於 About You
	about := extractSection(out, "## About You", "##")
	for _, forbidden := range []string{"- name:", "- role:", "- domain:"} {
		if strings.Contains(about, forbidden) {
			t.Errorf("About You should not contain %q:\n%s", forbidden, about)
		}
	}
	// 其他 keys 應留下
	for _, want := range []string{"- language: zh-TW", "- mood: cheerful"} {
		if !strings.Contains(about, want) {
			t.Errorf("About You missing %q:\n%s", want, about)
		}
	}
}

func TestToInstruction_DeterministicOutput(t *testing.T) {
	md := `# Bot
## Identity
- name: A
- alpha: 1
- beta: 2
- gamma: 3
`
	a := persona.Parse(md).ToInstruction()
	for i := 0; i < 10; i++ {
		b := persona.Parse(md).ToInstruction()
		if a != b {
			t.Fatalf("non-deterministic output:\nA=%s\nB=%s", a, b)
		}
	}
}

// extractSection 從 output 取出 startMarker 之後直到 nextMarker 之前的內容
func extractSection(out, startMarker, nextMarker string) string {
	i := strings.Index(out, startMarker)
	if i < 0 {
		return ""
	}
	rest := out[i+len(startMarker):]
	j := strings.Index(rest, nextMarker)
	if j < 0 {
		return rest
	}
	return rest[:j]
}

// ---- helpers ----

func assertStr(t *testing.T, label string, m map[string]interface{}, key, want string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("%s: key %q not found in map", label, key)
		return
	}
	if s, _ := v.(string); s != want {
		t.Errorf("%s: got %q, want %q", label, s, want)
	}
}
