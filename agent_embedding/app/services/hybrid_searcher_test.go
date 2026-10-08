package services_test

import (
	"math"
	"testing"

	"agent_embedding/app/services"
)

// ───── RRF 純函式測試（不需 DB / 網路）─────────────────────────────────────

func TestFuseRRF_BasicMath(t *testing.T) {
	// 兩路結果，預期 RRF 分數依 1/(k+rank_1based) 累加
	dense := []services.SearchHit{
		{ChunkID: "A", Text: "alpha"},
		{ChunkID: "B", Text: "bravo"},
		{ChunkID: "C", Text: "charlie"},
	}
	sparse := []services.SearchHit{
		{ChunkID: "C", Text: "charlie"},
		{ChunkID: "A", Text: "alpha"},
		{ChunkID: "D", Text: "delta"},
	}

	got := services.FuseRRF([][]services.SearchHit{dense, sparse}, 60)

	// 預期分數：
	//   A: 1/61 + 1/62 = 0.01639 + 0.01613 = 0.03252
	//   C: 1/63 + 1/61 = 0.01587 + 0.01639 = 0.03226
	//   B: 1/62 + 0     = 0.01613
	//   D: 0    + 1/63  = 0.01587
	//   排序：A > C > B > D
	expectedOrder := []string{"A", "C", "B", "D"}
	if len(got) != 4 {
		t.Fatalf("expected 4 results, got %d", len(got))
	}
	for i, exp := range expectedOrder {
		if got[i].ChunkID != exp {
			t.Errorf("rank %d: got %s, want %s (full order: %v)",
				i, got[i].ChunkID, exp, idsOf(got))
		}
	}

	// A 的分數應最大
	if got[0].Similarity <= got[1].Similarity {
		t.Errorf("expected top score strictly greater")
	}
	// 精確分數驗證 A
	expA := float32(1.0/61.0 + 1.0/62.0)
	if math.Abs(float64(got[0].Similarity-expA)) > 1e-6 {
		t.Errorf("A score = %v, want %v", got[0].Similarity, expA)
	}
}

func TestFuseRRF_EmptyLists(t *testing.T) {
	got := services.FuseRRF(nil, 60)
	if len(got) != 0 {
		t.Errorf("nil input → expected 0 hits, got %d", len(got))
	}
	got = services.FuseRRF([][]services.SearchHit{nil, {}}, 60)
	if len(got) != 0 {
		t.Errorf("empty inputs → expected 0 hits, got %d", len(got))
	}
}

func TestFuseRRF_SinglePath(t *testing.T) {
	dense := []services.SearchHit{
		{ChunkID: "A"},
		{ChunkID: "B"},
	}
	got := services.FuseRRF([][]services.SearchHit{dense}, 60)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].ChunkID != "A" || got[1].ChunkID != "B" {
		t.Errorf("order broken: %v", idsOf(got))
	}
}

func TestFuseRRF_KZeroFallsBackToDefault(t *testing.T) {
	a := []services.SearchHit{{ChunkID: "X"}}
	got := services.FuseRRF([][]services.SearchHit{a}, 0)
	// k=0 should fallback to DefaultRRFK (60); rank=1 → 1/61
	want := float32(1.0 / 61.0)
	if math.Abs(float64(got[0].Similarity-want)) > 1e-6 {
		t.Errorf("score = %v, want %v (k fallback)", got[0].Similarity, want)
	}
}

func TestFuseRRF_TieBreakByChunkID(t *testing.T) {
	// 兩個 chunk 完全只在第 1 名出現各一次 → 分數相同 → 依 chunk_id 字典序
	a := []services.SearchHit{{ChunkID: "B"}}
	b := []services.SearchHit{{ChunkID: "A"}}
	got := services.FuseRRF([][]services.SearchHit{a, b}, 60)
	if got[0].ChunkID != "A" {
		t.Errorf("tie-break failed; got order %v, want A first", idsOf(got))
	}
}

func TestFuseRRF_FillsMissingFieldsAcrossLists(t *testing.T) {
	// 某 chunk 在 list1 有 text 沒 doc_id；list2 有 doc_id 沒 text。融合後應都補上。
	list1 := []services.SearchHit{
		{ChunkID: "A", Text: "from-dense"},
	}
	list2 := []services.SearchHit{
		{ChunkID: "A", DocID: "doc-1"},
	}
	got := services.FuseRRF([][]services.SearchHit{list1, list2}, 60)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Text != "from-dense" {
		t.Errorf("Text not preserved: %q", got[0].Text)
	}
	if got[0].DocID != "doc-1" {
		t.Errorf("DocID not filled from list2: %q", got[0].DocID)
	}
}

// ───── HybridSearchOptions 預設值套用測試 ─────────────────────────────────

func TestHybridSearcher_NewWithNilSparseUsesNop(t *testing.T) {
	// nil sparse → 應自動換成 nop，不 panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("unexpected panic: %v", r)
		}
	}()
	// 用 nil dense 應該 panic（保護用法）
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil dense")
		}
	}()
	_ = services.NewHybridSearcher(nil, nil, services.HybridSearchOptions{})
}

func TestNopSparseSearcherReturnsEmpty(t *testing.T) {
	s := services.NewNopSparseSearcher()
	hits, err := s.Search(nil, "agent", "query", 10)
	if err != nil {
		t.Errorf("nop should not error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("nop returned hits: %d", len(hits))
	}
}

// ───── 工具 ──────────────────────────────────────────────────────────────────

func idsOf(hits []services.SearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ChunkID
	}
	return out
}
