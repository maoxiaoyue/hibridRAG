// Package retrieval — hybrid.go: Dense + Sparse 並發檢索 + RRF Fusion。
//
// 對應 TODO2 ⑤ / TODO4 P0-2：
//
//	[query]
//	   ├── DenseSearcher  (top_n) ─┐
//	   └── SparseSearcher (top_n) ─┴── RRF Fusion ── final top_k ── threshold filter
//
// 注意（2026-05-08）：
//   BGE-M3 sparse 路徑已放棄。SparseSearcher 預設 NopSparseSearcher（永遠回空），
//   此時 hybrid 結果等同 dense-only（仍經過 RRF/threshold 流程，行為一致）。
//   若 agent_embedding 端 sparse_strategy=fts，可改注入 RemoteSparseSearcher。
//
// 介面對齊：HybridSearcher 滿足 ctxbuilder.RAGSearcher（同樣的 SearchByQuery 簽名），
// 可在 main.go 直接替換掉 vectordb.HTTPClient 注入 ContextBuilder。
package retrieval

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"agent/internal/agent/vectordb"
)

// ── 介面 ─────────────────────────────────────────────────────────────────────

// DenseSearcher 語義（dense vector ANN）檢索。
// 由 *vectordb.HTTPClient 滿足（其 SearchByQuery 走 agent_embedding /api/search）。
type DenseSearcher interface {
	SearchByQuery(ctx context.Context, agentID, query string, topK int) ([]vectordb.SearchHit, error)
}

// SparseSearcher 詞彙（lexical）檢索。
// 預設由 NopSparseSearcher 滿足（回空切回 dense-only）；
// 可改注入 RemoteSparseSearcher（呼叫 agent_embedding /api/search/hybrid 取 sparse 部分）
// 或自寫 PG FTS client。
type SparseSearcher interface {
	Search(ctx context.Context, agentID, query string, topK int) ([]vectordb.SearchHit, error)
}

// NopSparseSearcher 永遠回空（dense-only 模式用）。
type NopSparseSearcher struct{}

// Search 永遠回 (nil, nil)。
func (NopSparseSearcher) Search(_ context.Context, _, _ string, _ int) ([]vectordb.SearchHit, error) {
	return nil, nil
}

// ── Options ─────────────────────────────────────────────────────────────────

// Options HybridSearcher 行為調整。所有值 <=0 → 走 defaults。
type Options struct {
	DenseTopN           int     // 每路 dense 取多少（預設 20）
	SparseTopN          int     // 每路 sparse 取多少（預設 20）
	FinalTopK           int     // RRF 融合後最終回傳（預設 5）
	RRFK                int     // RRF 公式 k 常數（預設 60）
	SimilarityThreshold float32 // 過濾相似度低於門檻者（套用於 dense 結果；預設 0 = 不過濾）
}

func (o Options) withDefaults() Options {
	if o.DenseTopN <= 0 {
		o.DenseTopN = 20
	}
	if o.SparseTopN <= 0 {
		o.SparseTopN = 20
	}
	if o.FinalTopK <= 0 {
		o.FinalTopK = 5
	}
	if o.RRFK <= 0 {
		o.RRFK = 60
	}
	// SimilarityThreshold == 0 表示不過濾，不需 default
	return o
}

// ── HybridSearcher ──────────────────────────────────────────────────────────

// HybridSearcher 並發跑 dense + sparse，RRF 融合後回 final_top_k。
type HybridSearcher struct {
	dense  DenseSearcher
	sparse SparseSearcher
	opts   Options
}

// New 建立 HybridSearcher。dense 必填；sparse 為 nil 時自動退到 NopSparseSearcher。
func New(dense DenseSearcher, sparse SparseSearcher, opts Options) *HybridSearcher {
	if dense == nil {
		panic("retrieval.New: dense searcher must not be nil")
	}
	if sparse == nil {
		sparse = NopSparseSearcher{}
	}
	return &HybridSearcher{
		dense:  dense,
		sparse: sparse,
		opts:   opts.withDefaults(),
	}
}

// SearchByQuery 是 ctxbuilder.RAGSearcher 介面的實作。
//
//	1. 並發跑 dense (top_n) + sparse (top_n)（goroutine + WaitGroup）
//	2. 任一路失敗：記在 errors，不中斷；另一路成功仍能組回結果
//	3. 套相似度門檻（僅 dense；sparse 無一致比例）
//	4. RRF Fusion：score = Σ 1/(k + rank_i)
//	5. 依 fused score 排序 → 取 topK 回傳（若 caller 給 topK<=0，用 opts.FinalTopK）
func (h *HybridSearcher) SearchByQuery(
	ctx context.Context,
	agentID, query string,
	topK int,
) ([]vectordb.SearchHit, error) {
	if agentID == "" || query == "" {
		return nil, fmt.Errorf("hybrid.SearchByQuery: agent_id and query required")
	}
	finalK := topK
	if finalK <= 0 {
		finalK = h.opts.FinalTopK
	}

	var (
		dense, sparse []vectordb.SearchHit
		denseErr, spErr error
		wg sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		dense, denseErr = h.dense.SearchByQuery(ctx, agentID, query, h.opts.DenseTopN)
	}()
	go func() {
		defer wg.Done()
		sparse, spErr = h.sparse.Search(ctx, agentID, query, h.opts.SparseTopN)
	}()
	wg.Wait()

	// 兩路全失敗才算錯
	if denseErr != nil && spErr != nil {
		return nil, fmt.Errorf("hybrid: dense=%v; sparse=%v", denseErr, spErr)
	}

	// dense 相似度門檻過濾
	if h.opts.SimilarityThreshold > 0 && len(dense) > 0 {
		filtered := dense[:0]
		for _, hit := range dense {
			if hit.Similarity >= h.opts.SimilarityThreshold {
				filtered = append(filtered, hit)
			}
		}
		dense = filtered
	}

	fused := rrfFuse(dense, sparse, h.opts.RRFK)
	if len(fused) > finalK {
		fused = fused[:finalK]
	}
	return fused, nil
}

// ── RRF Fusion ──────────────────────────────────────────────────────────────

// fusedItem 內部融合中介結構
type fusedItem struct {
	hit   vectordb.SearchHit
	score float64
}

// rrfFuse RRF 公式：score(d) = Σ 1/(k + rank_i(d))（rank 由 0 起算）
//
// 識別 key 優先用 ChunkID；空時退用 (DocID + "/" + Text 前 64 字)。
// 同 key 出現於兩路時，分數累加；資料用兩邊取較豐富者（Text 較長者）。
func rrfFuse(dense, sparse []vectordb.SearchHit, k int) []vectordb.SearchHit {
	if k <= 0 {
		k = 60
	}
	scores := make(map[string]*fusedItem, len(dense)+len(sparse))

	add := func(list []vectordb.SearchHit) {
		for rank, hit := range list {
			key := hitKey(hit)
			contrib := 1.0 / float64(k+rank+1) // rank+1 讓首位貢獻 1/(k+1)
			if cur, ok := scores[key]; ok {
				cur.score += contrib
				// 取資料較完整者
				if len(hit.Text) > len(cur.hit.Text) {
					cur.hit = hit
				}
			} else {
				scores[key] = &fusedItem{hit: hit, score: contrib}
			}
		}
	}
	add(dense)
	add(sparse)

	items := make([]*fusedItem, 0, len(scores))
	for _, it := range scores {
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		// tie-break：dense similarity 大者優先
		return items[i].hit.Similarity > items[j].hit.Similarity
	})

	out := make([]vectordb.SearchHit, len(items))
	for i, it := range items {
		out[i] = it.hit
		// 用 fused score 覆寫 Similarity 欄位，方便 caller 觀測排序依據
		// （注意：覆寫後不再是原 dense 的 cosine 相似度）
		out[i].Similarity = float32(it.score)
	}
	return out
}

// hitKey 為融合用的去重 key。
func hitKey(h vectordb.SearchHit) string {
	if h.ChunkID != "" {
		return "c:" + h.ChunkID
	}
	txt := h.Text
	if len(txt) > 64 {
		txt = txt[:64]
	}
	return "d:" + h.DocID + "|" + txt
}
