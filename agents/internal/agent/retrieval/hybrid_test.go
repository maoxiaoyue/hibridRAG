package retrieval

import (
	"context"
	"errors"
	"testing"

	"agent/internal/agent/vectordb"
)

type stubDense struct {
	hits []vectordb.SearchHit
	err  error
}

func (s stubDense) SearchByQuery(_ context.Context, _, _ string, _ int) ([]vectordb.SearchHit, error) {
	return s.hits, s.err
}

type stubSparse struct {
	hits []vectordb.SearchHit
	err  error
}

func (s stubSparse) Search(_ context.Context, _, _ string, _ int) ([]vectordb.SearchHit, error) {
	return s.hits, s.err
}

func mkHit(id, doc, text string, sim float32) vectordb.SearchHit {
	return vectordb.SearchHit{ChunkID: id, DocID: doc, Text: text, Similarity: sim}
}

func TestHybrid_DenseOnly_NopSparse(t *testing.T) {
	dense := stubDense{hits: []vectordb.SearchHit{
		mkHit("a", "d1", "alpha", 0.9),
		mkHit("b", "d1", "beta", 0.8),
	}}
	h := New(dense, nil, Options{FinalTopK: 2})
	out, err := h.SearchByQuery(context.Background(), "ag", "q", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 hits, got %d", len(out))
	}
	if out[0].ChunkID != "a" {
		t.Fatalf("want first=a, got %s", out[0].ChunkID)
	}
}

func TestHybrid_RRFFavorsOverlap(t *testing.T) {
	// 兩路都包含 "a"，a 應浮到第一名
	dense := stubDense{hits: []vectordb.SearchHit{
		mkHit("x", "d1", "x", 0.95), // dense rank 0
		mkHit("a", "d2", "a", 0.80), // dense rank 1
	}}
	sparse := stubSparse{hits: []vectordb.SearchHit{
		mkHit("a", "d2", "a", 0.0), // sparse rank 0
		mkHit("y", "d3", "y", 0.0), // sparse rank 1
	}}
	h := New(dense, sparse, Options{FinalTopK: 3, RRFK: 60})
	out, err := h.SearchByQuery(context.Background(), "ag", "q", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 fused, got %d", len(out))
	}
	if out[0].ChunkID != "a" {
		t.Fatalf("overlap should win; got first=%s", out[0].ChunkID)
	}
}

func TestHybrid_OneSideFailsStillReturns(t *testing.T) {
	dense := stubDense{hits: []vectordb.SearchHit{mkHit("a", "d", "a", 0.9)}}
	sparse := stubSparse{err: errors.New("pg down")}
	h := New(dense, sparse, Options{})
	out, err := h.SearchByQuery(context.Background(), "ag", "q", 0)
	if err != nil {
		t.Fatalf("one side failure should not error; got %v", err)
	}
	if len(out) != 1 || out[0].ChunkID != "a" {
		t.Fatalf("unexpected: %+v", out)
	}
}

func TestHybrid_BothFailErrors(t *testing.T) {
	h := New(stubDense{err: errors.New("d")}, stubSparse{err: errors.New("s")}, Options{})
	_, err := h.SearchByQuery(context.Background(), "ag", "q", 0)
	if err == nil {
		t.Fatal("expected error when both paths fail")
	}
}

func TestHybrid_SimilarityThreshold(t *testing.T) {
	dense := stubDense{hits: []vectordb.SearchHit{
		mkHit("a", "d", "a", 0.5),
		mkHit("b", "d", "b", 0.2),
	}}
	h := New(dense, nil, Options{SimilarityThreshold: 0.3, FinalTopK: 10})
	out, _ := h.SearchByQuery(context.Background(), "ag", "q", 0)
	if len(out) != 1 || out[0].ChunkID != "a" {
		t.Fatalf("threshold should drop b; got %+v", out)
	}
}

func TestHybrid_AgentAndQueryRequired(t *testing.T) {
	h := New(stubDense{}, nil, Options{})
	if _, err := h.SearchByQuery(context.Background(), "", "q", 0); err == nil {
		t.Fatal("empty agent_id should error")
	}
	if _, err := h.SearchByQuery(context.Background(), "a", "", 0); err == nil {
		t.Fatal("empty query should error")
	}
}

func TestHybrid_TopKOverridesFinalTopK(t *testing.T) {
	dense := stubDense{hits: []vectordb.SearchHit{
		mkHit("a", "d", "a", 0.9), mkHit("b", "d", "b", 0.8), mkHit("c", "d", "c", 0.7),
	}}
	h := New(dense, nil, Options{FinalTopK: 1})
	out, _ := h.SearchByQuery(context.Background(), "ag", "q", 2)
	if len(out) != 2 {
		t.Fatalf("caller topK should win; got %d", len(out))
	}
}

func TestHybrid_PanicOnNilDense(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil dense")
		}
	}()
	_ = New(nil, nil, Options{})
}
