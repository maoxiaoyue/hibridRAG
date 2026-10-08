package context_test

import (
	"context"
	stderr "errors"
	"strings"
	"testing"

	"agent/app/models"
	"agent/app/services"
	ctxbuilder "agent/internal/agent/context"
	"agent/internal/agent/persona"
	"agent/internal/agent/vectordb"
)

// ── Mocks ──────────────────────────────────────────────────────────────────

type fakeMemory struct {
	personal   *models.PersonalMemory
	personalErr error
	procedural []models.ProceduralMemory
	episodic   []models.EpisodicMemory

	// 觀測：實際被呼叫時的 limit 值
	gotProceduralLimit int
	gotEpisodicLimit   int
}

func (f *fakeMemory) GetPersonalByAgent(_ context.Context, _ string) (*models.PersonalMemory, error) {
	if f.personalErr != nil {
		return nil, f.personalErr
	}
	return f.personal, nil
}

func (f *fakeMemory) ListProceduralByAgent(_ context.Context, _ string, limit int) ([]models.ProceduralMemory, error) {
	f.gotProceduralLimit = limit
	if limit > 0 && len(f.procedural) > limit {
		return f.procedural[:limit], nil
	}
	return f.procedural, nil
}

func (f *fakeMemory) ListEpisodicByAgent(_ context.Context, _ string, limit int) ([]models.EpisodicMemory, error) {
	f.gotEpisodicLimit = limit
	if limit > 0 && len(f.episodic) > limit {
		return f.episodic[:limit], nil
	}
	return f.episodic, nil
}

type fakeRAG struct {
	hits  []vectordb.SearchHit
	err   error
	gotK  int
	gotQ  string
}

func (f *fakeRAG) SearchByQuery(_ context.Context, _ string, query string, topK int) ([]vectordb.SearchHit, error) {
	f.gotK = topK
	f.gotQ = query
	if f.err != nil {
		return nil, f.err
	}
	if topK > 0 && len(f.hits) > topK {
		return f.hits[:topK], nil
	}
	return f.hits, nil
}

// ── Helpers ────────────────────────────────────────────────────────────────

func newDefaults() ctxbuilder.Quotas {
	return ctxbuilder.Quotas{Personal: 1, Procedural: 3, RAG: 5, Episodic: 2, MaxChars: 10000}
}

// ── Tests ──────────────────────────────────────────────────────────────────

func TestBuild_RAG_AND_EpisodicAreInjected(t *testing.T) {
	mem := &fakeMemory{
		episodic: []models.EpisodicMemory{
			{ID: "em_1", AgentID: "a1", SessionID: "s_1", Summary: "問過退款政策"},
			{ID: "em_2", AgentID: "a1", SessionID: "s_2", Summary: "問過配送時間"},
		},
	}
	rag := &fakeRAG{
		hits: []vectordb.SearchHit{
			{ChunkID: "ck_1", DocID: "faq", Text: "退款 7 天內全額"},
			{ChunkID: "ck_2", DocID: "faq", Text: "配送 1-3 工作天"},
		},
	}
	b := ctxbuilder.New(mem, rag, newDefaults())

	res, err := b.BuildWithPersona(context.Background(), "a1", "可以退貨嗎", nil, "You are SupportBot.")
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	sp := res.SystemPrompt

	// instruction 必須在最前
	if !strings.HasPrefix(sp, "You are SupportBot.") {
		t.Errorf("instruction not at start; got %q", sp[:30])
	}
	// RAG 段
	if !strings.Contains(sp, "## Knowledge") || !strings.Contains(sp, "退款 7 天內全額") {
		t.Error("RAG section missing in system prompt")
	}
	// Episodic 段
	if !strings.Contains(sp, "## Recent Conversations") || !strings.Contains(sp, "問過退款政策") {
		t.Error("Episodic section missing")
	}
	// counts
	if res.Counts.RAG != 2 || res.Counts.Episodic != 2 {
		t.Errorf("counts wrong: %+v", res.Counts)
	}
	// query 確實傳給 RAG
	if rag.gotQ != "可以退貨嗎" {
		t.Errorf("rag query = %q", rag.gotQ)
	}
}

func TestBuild_PersonaQuotasOverrideDefaults(t *testing.T) {
	mem := &fakeMemory{episodic: makeEpisodics(10)}
	rag := &fakeRAG{hits: makeHits(10)}
	b := ctxbuilder.New(mem, rag, newDefaults())

	// SKILL.md 指定 context_rag=1, context_episodic=4 → 應覆蓋 defaults (5, 2)
	parsed := &persona.ParseResult{
		Identity: map[string]interface{}{
			"context_rag":      "1",
			"context_episodic": 4,
		},
	}
	res, err := b.BuildWithPersona(context.Background(), "a1", "query", parsed, "You are X.")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rag.gotK != 1 {
		t.Errorf("rag topK = %d, want 1 (override)", rag.gotK)
	}
	if mem.gotEpisodicLimit != 4 {
		t.Errorf("episodic limit = %d, want 4 (override)", mem.gotEpisodicLimit)
	}
	if res.Counts.RAG != 1 || res.Counts.Episodic != 4 {
		t.Errorf("counts: %+v", res.Counts)
	}
}

func TestBuild_FallbackUsesDB_WhenInstructionEmpty(t *testing.T) {
	mem := &fakeMemory{
		personal: &models.PersonalMemory{
			AgentID: "a1",
			Identity: map[string]interface{}{
				"name": "FallbackBot",
				"role": "Helper",
			},
		},
		procedural: []models.ProceduralMemory{
			{AgentID: "a1", ModelName: "First Principles", Content: "拆解問題"},
		},
	}
	b := ctxbuilder.New(mem, nil, newDefaults())

	// instruction 空 → 應從 DB 取 personal + procedural
	res, err := b.BuildWithPersona(context.Background(), "a1", "", nil, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	sp := res.SystemPrompt

	if !strings.Contains(sp, "## About You") || !strings.Contains(sp, "FallbackBot") {
		t.Error("Personal fallback section missing")
	}
	if !strings.Contains(sp, "## Mental Models") || !strings.Contains(sp, "First Principles") {
		t.Error("Procedural fallback section missing")
	}
	if res.Counts.Personal != 1 || res.Counts.Procedural != 1 {
		t.Errorf("counts: %+v", res.Counts)
	}
}

func TestBuild_NoQueryNoRAG(t *testing.T) {
	mem := &fakeMemory{}
	rag := &fakeRAG{hits: makeHits(5)}
	b := ctxbuilder.New(mem, rag, newDefaults())

	// query 空 → 不查 RAG
	_, err := b.BuildWithPersona(context.Background(), "a1", "", nil, "instruction")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rag.gotK != 0 { // 完全沒呼叫 RAG
		t.Errorf("RAG should not be called, got topK=%d", rag.gotK)
	}
}

func TestBuild_NilRAGStillWorks(t *testing.T) {
	mem := &fakeMemory{episodic: makeEpisodics(3)}
	b := ctxbuilder.New(mem, nil, newDefaults())

	res, err := b.BuildWithPersona(context.Background(), "a1", "anything", nil, "instruction")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Counts.RAG != 0 {
		t.Errorf("RAG count should be 0 with nil searcher")
	}
	if res.Counts.Episodic != 2 {
		t.Errorf("episodic count = %d, want 2 (default)", res.Counts.Episodic)
	}
}

func TestBuild_PersonalNotFoundIsTolerated(t *testing.T) {
	mem := &fakeMemory{personalErr: services.ErrNotFound}
	b := ctxbuilder.New(mem, nil, newDefaults())

	// instruction 空 → 嘗試取 personal，但回 NotFound 不算錯
	res, err := b.BuildWithPersona(context.Background(), "a1", "", nil, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Counts.Personal != 0 {
		t.Errorf("personal count = %d, want 0", res.Counts.Personal)
	}
}

func TestBuild_PanicOnNilMemory(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on nil memory")
		}
	}()
	_ = ctxbuilder.New(nil, nil, newDefaults())
}

func TestBuild_TruncatesByMaxChars(t *testing.T) {
	mem := &fakeMemory{}
	rag := &fakeRAG{
		hits: []vectordb.SearchHit{{ChunkID: "ck", DocID: "doc", Text: strings.Repeat("中", 5000)}},
	}
	defaults := newDefaults()
	defaults.MaxChars = 200
	b := ctxbuilder.New(mem, rag, defaults)

	res, err := b.BuildWithPersona(context.Background(), "a1", "q", nil, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// runes count, 不是 byte
	if r := []rune(res.SystemPrompt); len(r) > 250 {
		t.Errorf("system prompt not truncated: %d runes", len(r))
	}
	if !strings.Contains(res.SystemPrompt, "context truncated") {
		t.Error("missing truncation marker")
	}
}

func TestBuild_AgentIDRequired(t *testing.T) {
	mem := &fakeMemory{}
	b := ctxbuilder.New(mem, nil, newDefaults())
	_, err := b.BuildWithPersona(context.Background(), "", "q", nil, "instruction")
	if err == nil {
		t.Error("expected error when agentID is empty")
	}
}

func TestBuild_RAGErrorDoesNotBreakAssembly(t *testing.T) {
	mem := &fakeMemory{episodic: makeEpisodics(2)}
	rag := &fakeRAG{err: stderr.New("RAG service down")}
	b := ctxbuilder.New(mem, rag, newDefaults())

	res, err := b.BuildWithPersona(context.Background(), "a1", "q", nil, "instruction")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// RAG 失敗，但 instruction + episodic 仍應在
	if !strings.Contains(res.SystemPrompt, "instruction") {
		t.Error("instruction lost on RAG failure")
	}
	if res.Counts.Episodic != 2 {
		t.Errorf("episodic should still appear, got %d", res.Counts.Episodic)
	}
}

// ── 小工具 ───────────────────────────────────────────────────────────────────

func makeHits(n int) []vectordb.SearchHit {
	out := make([]vectordb.SearchHit, n)
	for i := range out {
		out[i] = vectordb.SearchHit{ChunkID: "ck", DocID: "d", Text: "片段"}
	}
	return out
}

func makeEpisodics(n int) []models.EpisodicMemory {
	out := make([]models.EpisodicMemory, n)
	for i := range out {
		out[i] = models.EpisodicMemory{AgentID: "a1", SessionID: "s", Summary: "歷史對話"}
	}
	return out
}
