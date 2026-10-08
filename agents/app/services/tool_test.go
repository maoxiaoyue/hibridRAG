package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agent/app/models"
)

// ── Registry ────────────────────────────────────────────────────────────────

type fakeTool struct{ name string }

func (f *fakeTool) Name() string         { return f.name }
func (f *fakeTool) Description() string  { return "fake" }
func (f *fakeTool) Schema() ToolSchema   { return NewObjectSchema() }
func (f *fakeTool) Call(_ context.Context, _ map[string]any) (ToolResult, error) {
	return ToolResult{Content: "ok"}, nil
}

func TestRegistry_RegisterDuplicateFails(t *testing.T) {
	r := NewToolRegistry()
	if err := r.Register(&fakeTool{name: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(&fakeTool{name: "x"}); err == nil {
		t.Fatal("expected duplicate error")
	}
	if err := r.Register(nil); err == nil {
		t.Fatal("expected nil error")
	}
	if err := r.Register(&fakeTool{name: ""}); err == nil {
		t.Fatal("expected empty-name error")
	}
}

func TestRegistry_ListSorted(t *testing.T) {
	r := NewToolRegistry()
	for _, n := range []string{"c", "a", "b"} {
		_ = r.Register(&fakeTool{name: n})
	}
	got := r.List()
	if len(got) != 3 || got[0].Name() != "a" || got[2].Name() != "c" {
		t.Fatalf("list not sorted: %v", got)
	}
}

// ── MemoryReadTool / MemoryWriteTool ───────────────────────────────────────

type fakeMemReader struct {
	personal   *models.PersonalMemory
	procedural []models.ProceduralMemory
	episodic   []models.EpisodicMemory
}

func (f *fakeMemReader) GetPersonalByAgent(_ context.Context, _ string) (*models.PersonalMemory, error) {
	return f.personal, nil
}
func (f *fakeMemReader) ListProceduralByAgent(_ context.Context, _ string, _ int) ([]models.ProceduralMemory, error) {
	return f.procedural, nil
}
func (f *fakeMemReader) ListEpisodicByAgent(_ context.Context, _ string, _ int) ([]models.EpisodicMemory, error) {
	return f.episodic, nil
}

func TestMemoryReadTool_KindDispatch(t *testing.T) {
	r := &fakeMemReader{
		personal:   &models.PersonalMemory{ID: "pm1", AgentID: "a1"},
		procedural: []models.ProceduralMemory{{ID: "x", ModelName: "m"}},
	}
	tool := &MemoryReadTool{AgentID: "a1", Reader: r}

	res, err := tool.Call(context.Background(), map[string]any{"kind": "personal"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "pm1") {
		t.Fatalf("personal content lost: %s", res.Content)
	}

	res, _ = tool.Call(context.Background(), map[string]any{"kind": "procedural"})
	if !strings.Contains(res.Content, "ModelName") && !strings.Contains(res.Content, "model_name") {
		t.Fatalf("procedural not serialized: %s", res.Content)
	}

	res, _ = tool.Call(context.Background(), map[string]any{"kind": "bogus"})
	if !res.IsError {
		t.Fatal("expected error for unknown kind")
	}
}

type fakeMemWriter struct{ called bool }

func (f *fakeMemWriter) UpsertProcedural(_ context.Context, req models.CreateProceduralMemoryRequest) (*models.ProceduralMemory, error) {
	f.called = true
	return &models.ProceduralMemory{ID: "pm_new", ModelName: req.ModelName}, nil
}

func TestMemoryWriteTool_RequiresFields(t *testing.T) {
	w := &fakeMemWriter{}
	tool := &MemoryWriteTool{AgentID: "a1", Writer: w}

	res, _ := tool.Call(context.Background(), map[string]any{"model_name": "m"})
	if !res.IsError {
		t.Fatal("missing content should error")
	}
	res, _ = tool.Call(context.Background(), map[string]any{"model_name": "m", "content": "c"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !w.called {
		t.Fatal("writer not called")
	}
}

// ── RAGQueryTool ───────────────────────────────────────────────────────────

type fakeRAG struct{ hits []RAGHit; err error }

func (f *fakeRAG) SearchByQuery(_ context.Context, _, _ string, _ int) ([]RAGHit, error) {
	return f.hits, f.err
}

func TestRAGQueryTool(t *testing.T) {
	tool := &RAGQueryTool{AgentID: "a1", Searcher: &fakeRAG{
		hits: []RAGHit{{ChunkID: "c1", Text: "alpha", Similarity: 0.9}},
	}}
	res, _ := tool.Call(context.Background(), map[string]any{"query": "hi"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(res.Content), &payload)
	if int(payload["count"].(float64)) != 1 {
		t.Fatalf("count wrong: %v", payload)
	}

	res, _ = tool.Call(context.Background(), map[string]any{})
	if !res.IsError {
		t.Fatal("missing query should error")
	}

	failTool := &RAGQueryTool{AgentID: "a1", Searcher: &fakeRAG{err: errors.New("down")}}
	res, _ = failTool.Call(context.Background(), map[string]any{"query": "x"})
	if !res.IsError {
		t.Fatal("searcher error should propagate as IsError")
	}
}

// ── Tool loop ──────────────────────────────────────────────────────────────

type stubLLM struct {
	turns []*ToolChatResponse // 依序回；index 用每次呼叫遞增
	i     int
}

func (s *stubLLM) Provider() string { return "stub" }
func (s *stubLLM) Chat(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	return nil, errors.New("not used")
}
func (s *stubLLM) ChatWithTools(_ context.Context, _ ChatRequest, _ []Tool) (*ToolChatResponse, error) {
	if s.i >= len(s.turns) {
		return nil, errors.New("stub exhausted")
	}
	r := s.turns[s.i]
	s.i++
	return r, nil
}

func TestRunToolLoop_FinalContentNoTools(t *testing.T) {
	llm := &stubLLM{turns: []*ToolChatResponse{
		{ChatResponse: ChatResponse{Content: "hello"}},
	}}
	out, msgs, traces, err := RunToolLoop(context.Background(), llm, NewToolRegistry(),
		[]ChatMessage{{Role: RoleUser, Content: "hi"}}, nil, ToolLoopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello" {
		t.Fatalf("want hello got %q", out)
	}
	if len(traces) != 0 {
		t.Fatalf("no tool calls expected; traces=%v", traces)
	}
	if msgs[len(msgs)-1].Role != RoleAssistant {
		t.Fatal("final message should be assistant")
	}
}

func TestRunToolLoop_CallsToolThenFinal(t *testing.T) {
	reg := NewToolRegistry()
	_ = reg.Register(&fakeTool{name: "echo"})
	llm := &stubLLM{turns: []*ToolChatResponse{
		{ChatResponse: ChatResponse{Content: ""}, ToolCalls: []ToolCall{{ID: "1", Name: "echo"}}},
		{ChatResponse: ChatResponse{Content: "done"}},
	}}
	out, msgs, traces, err := RunToolLoop(context.Background(), llm, reg,
		[]ChatMessage{{Role: RoleUser, Content: "hi"}}, nil, ToolLoopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "done" {
		t.Fatalf("want done got %q", out)
	}
	if len(traces) != 1 || !traces[0].OK || traces[0].Name != "echo" {
		t.Fatalf("traces wrong: %+v", traces)
	}
	// messages: user + assistant(placeholder) + user(tool_result) + assistant(done)
	if len(msgs) != 4 {
		t.Fatalf("msg count want 4 got %d: %+v", len(msgs), msgs)
	}
}

func TestRunToolLoop_UnknownToolReportedNotFatal(t *testing.T) {
	llm := &stubLLM{turns: []*ToolChatResponse{
		{ToolCalls: []ToolCall{{ID: "1", Name: "ghost"}}},
		{ChatResponse: ChatResponse{Content: "recovered"}},
	}}
	out, _, traces, err := RunToolLoop(context.Background(), llm, NewToolRegistry(),
		[]ChatMessage{}, nil, ToolLoopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "recovered" {
		t.Fatalf("want recovered got %q", out)
	}
	if len(traces) != 1 || traces[0].OK {
		t.Fatalf("expected one failed trace; got %+v", traces)
	}
}

func TestRunToolLoop_ExceedsMaxIter(t *testing.T) {
	reg := NewToolRegistry()
	_ = reg.Register(&fakeTool{name: "x"})
	// LLM 一直要求呼叫 tool，迴圈會用完
	turns := make([]*ToolChatResponse, 10)
	for i := range turns {
		turns[i] = &ToolChatResponse{ToolCalls: []ToolCall{{ID: "1", Name: "x"}}}
	}
	llm := &stubLLM{turns: turns}
	_, _, _, err := RunToolLoop(context.Background(), llm, reg, []ChatMessage{}, nil,
		ToolLoopOptions{MaxIterations: 2})
	if err == nil || !strings.Contains(err.Error(), "exceeded max iterations") {
		t.Fatalf("want max-iter error, got %v", err)
	}
}

// ── WebSearchTool (no provider) ─────────────────────────────────────────────

func TestWebSearchTool_NoProviderIsError(t *testing.T) {
	tool := &WebSearchTool{}
	res, _ := tool.Call(context.Background(), map[string]any{"query": "x"})
	if !res.IsError || !strings.Contains(res.Content, "disabled") {
		t.Fatalf("expected disabled error; got %+v", res)
	}
}

// ── HTTPSearchProvider digSlice ────────────────────────────────────────────

func TestDigSlice(t *testing.T) {
	root := map[string]any{
		"web": map[string]any{
			"results": []any{
				map[string]any{"title": "t1"},
				map[string]any{"title": "t2"},
			},
		},
	}
	arr := digSlice(root, "web.results")
	if len(arr) != 2 {
		t.Fatalf("want 2 got %d", len(arr))
	}
	if digSlice(root, "nope.path") != nil {
		t.Fatal("missing path should be nil")
	}
}
