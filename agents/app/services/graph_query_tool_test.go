package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeGraphQuerier struct {
	entities []GraphEntity
	ctxResp  GraphContext
	hybrid   GraphHybridResult
	proposed struct{ id, status string }
	err      error

	lastCall string
	lastArgs map[string]any
}

func (f *fakeGraphQuerier) SearchEntities(_ context.Context, query string, limit int) ([]GraphEntity, error) {
	f.lastCall = "search"
	f.lastArgs = map[string]any{"query": query, "limit": limit}
	if f.err != nil {
		return nil, f.err
	}
	return f.entities, nil
}

func (f *fakeGraphQuerier) GetLocalContext(_ context.Context, entityName string, hops, limit int) (GraphContext, error) {
	f.lastCall = "context"
	f.lastArgs = map[string]any{"entity_name": entityName, "hops": hops, "limit": limit}
	if f.err != nil {
		return GraphContext{}, f.err
	}
	return f.ctxResp, nil
}

func (f *fakeGraphQuerier) HybridSearch(_ context.Context, query string, topK, hops int) (GraphHybridResult, error) {
	f.lastCall = "hybrid_search"
	f.lastArgs = map[string]any{"query": query, "top_k": topK, "hops": hops}
	if f.err != nil {
		return GraphHybridResult{}, f.err
	}
	return f.hybrid, nil
}

func (f *fakeGraphQuerier) ProposeRelation(_ context.Context, source, target, relType string, confidence float64, sourceFile, extractedBy string) (string, string, error) {
	f.lastCall = "propose_relation"
	f.lastArgs = map[string]any{"source": source, "target": target, "type": relType, "confidence": confidence}
	if f.err != nil {
		return "", "", f.err
	}
	return f.proposed.id, f.proposed.status, nil
}

func TestGraphQueryTool_NotConfigured(t *testing.T) {
	tool := &GraphQueryTool{}
	res, _ := tool.Call(context.Background(), map[string]any{"kind": "search", "query": "x"})
	if !res.IsError {
		t.Fatal("expected error when Querier is nil")
	}
}

func TestGraphQueryTool_UnknownKind(t *testing.T) {
	tool := &GraphQueryTool{Querier: &fakeGraphQuerier{}}
	res, _ := tool.Call(context.Background(), map[string]any{"kind": "bogus"})
	if !res.IsError {
		t.Fatal("expected error for unknown kind")
	}
}

func TestGraphQueryTool_Search(t *testing.T) {
	fq := &fakeGraphQuerier{entities: []GraphEntity{{Name: "Foo"}, {Name: "Bar"}}}
	tool := &GraphQueryTool{Querier: fq}

	res, err := tool.Call(context.Background(), map[string]any{"kind": "search", "query": "foo", "limit": float64(5)})
	if err != nil || res.IsError {
		t.Fatalf("Call failed: err=%v content=%s", err, res.Content)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(res.Content), &payload)
	if int(payload["count"].(float64)) != 2 {
		t.Errorf("count = %v, want 2", payload["count"])
	}
	if fq.lastArgs["limit"] != 5 {
		t.Errorf("limit passed through = %v, want 5", fq.lastArgs["limit"])
	}

	res, _ = tool.Call(context.Background(), map[string]any{"kind": "search"})
	if !res.IsError {
		t.Fatal("missing query should error")
	}
}

func TestGraphQueryTool_SearchDefaultLimit(t *testing.T) {
	fq := &fakeGraphQuerier{}
	tool := &GraphQueryTool{Querier: fq}
	if _, err := tool.Call(context.Background(), map[string]any{"kind": "search", "query": "x"}); err != nil {
		t.Fatal(err)
	}
	if fq.lastArgs["limit"] != 10 {
		t.Errorf("default limit = %v, want 10", fq.lastArgs["limit"])
	}
}

func TestGraphQueryTool_Context(t *testing.T) {
	fq := &fakeGraphQuerier{ctxResp: GraphContext{
		Center:    GraphEntity{Name: "Center"},
		Neighbors: []GraphEntity{{Name: "N1"}},
	}}
	tool := &GraphQueryTool{Querier: fq}

	res, err := tool.Call(context.Background(), map[string]any{"kind": "context", "entity_name": "Center", "hops": float64(2)})
	if err != nil || res.IsError {
		t.Fatalf("Call failed: err=%v content=%s", err, res.Content)
	}
	if fq.lastArgs["hops"] != 2 {
		t.Errorf("hops passed through = %v, want 2", fq.lastArgs["hops"])
	}

	res, _ = tool.Call(context.Background(), map[string]any{"kind": "context"})
	if !res.IsError {
		t.Fatal("missing entity_name should error")
	}
}

func TestGraphQueryTool_HybridSearch(t *testing.T) {
	fq := &fakeGraphQuerier{hybrid: GraphHybridResult{Seeds: []GraphEntity{{Name: "Seed", Score: 0.9}}}}
	tool := &GraphQueryTool{Querier: fq}

	res, err := tool.Call(context.Background(), map[string]any{"kind": "hybrid_search", "query": "something", "top_k": float64(3)})
	if err != nil || res.IsError {
		t.Fatalf("Call failed: err=%v content=%s", err, res.Content)
	}
	if fq.lastArgs["top_k"] != 3 {
		t.Errorf("top_k passed through = %v, want 3", fq.lastArgs["top_k"])
	}

	res, _ = tool.Call(context.Background(), map[string]any{"kind": "hybrid_search"})
	if !res.IsError {
		t.Fatal("missing query should error")
	}
}

func TestGraphQueryTool_ProposeRelation(t *testing.T) {
	fq := &fakeGraphQuerier{}
	fq.proposed.id, fq.proposed.status = "pr_1", "pending"
	tool := &GraphQueryTool{Querier: fq}

	res, err := tool.Call(context.Background(), map[string]any{
		"kind": "propose_relation", "source_entity": "A", "target_entity": "B",
		"relation_type": "causes", "confidence": 0.4,
	})
	if err != nil || res.IsError {
		t.Fatalf("Call failed: err=%v content=%s", err, res.Content)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(res.Content), &payload)
	if payload["id"] != "pr_1" || payload["status"] != "pending" {
		t.Errorf("payload = %v", payload)
	}
	if fq.lastArgs["confidence"] != 0.4 {
		t.Errorf("confidence passed through = %v, want 0.4", fq.lastArgs["confidence"])
	}

	res, _ = tool.Call(context.Background(), map[string]any{"kind": "propose_relation", "source_entity": "A"})
	if !res.IsError {
		t.Fatal("missing target_entity should error")
	}
}

func TestGraphQueryTool_QuerierErrorPropagates(t *testing.T) {
	fq := &fakeGraphQuerier{err: errors.New("down")}
	tool := &GraphQueryTool{Querier: fq}
	res, _ := tool.Call(context.Background(), map[string]any{"kind": "search", "query": "x"})
	if !res.IsError {
		t.Fatal("querier error should propagate as IsError")
	}
}
