// Package services — hybrid_searcher.go: Dense + Sparse 並發檢索 + RRF Fusion
//
// 對應架構 v2 元件 ⑤：
//
//	Cassandra ANN Dense top-N ──┐
//	                            ├─→ RRF Fusion → top-K
//	PostgreSQL Sparse  top-N ──┘
//
// RRF (Reciprocal Rank Fusion) 公式：
//
//	score(d) = Σ 1 / (k + rank_i(d))
//
//	k = 60（標準值；可調），rank_i 為 d 在第 i 路結果中的 1-based 名次
//
// 為什麼用 RRF：
//   - 不需要正規化各路分數（dense cosine 與 sparse BM25 量級不同）
//   - 對單一路結果無命中的 doc 自動降權（沒出現就沒分）
//   - 數學上對 outlier 穩健，社群驗證效果優於 weighted-sum 平均
package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// DefaultRRFK RRF 公式中的 k 常數（學界標準值）
const DefaultRRFK = 60

// HybridSearchOptions 控制單次 hybrid search 行為
type HybridSearchOptions struct {
	// DenseTopN 各路 dense ANN 撈取數量（預設 20）
	DenseTopN int
	// SparseTopN 各路 sparse 撈取數量（預設 20）
	SparseTopN int
	// FinalTopK RRF 融合後最終回傳數量（預設 5）
	FinalTopK int
	// RRFK 公式參數，預設 60
	RRFK int
	// MinScore 過濾門檻（融合後 score < MinScore 的丟棄；0 = 不過濾）
	MinScore float64
}

func (o *HybridSearchOptions) applyDefaults() {
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
		o.RRFK = DefaultRRFK
	}
}

// HybridSearcher 將 dense（Cassandra ANN）+ sparse（PG）兩路檢索結果用 RRF 融合
type HybridSearcher struct {
	dense    *EmbeddingService // 提供 dense 搜尋（複用既有 SearchByText）
	sparse   SparseSearcher    // 可注入 PG / SPLADE / nop
	defaults HybridSearchOptions
}

// NewHybridSearcher 建立 HybridSearcher。
// dense 必填；sparse 可為 nil（會自動退化為 dense-only，仍走 RRF 但只有一路）。
func NewHybridSearcher(dense *EmbeddingService, sparse SparseSearcher, defaults HybridSearchOptions) *HybridSearcher {
	if dense == nil {
		panic("services.NewHybridSearcher: dense must not be nil")
	}
	if sparse == nil {
		sparse = NewNopSparseSearcher()
	}
	defaults.applyDefaults()
	return &HybridSearcher{dense: dense, sparse: sparse, defaults: defaults}
}

// SearchResult Hybrid search 回傳結果
type SearchResult struct {
	// Hits 融合後的 SearchHit（Similarity 欄位裝 RRF 分數）
	Hits []SearchHit
	// DenseCount / SparseCount 各路命中數（觀測用）
	DenseCount  int
	SparseCount int
	// FusedScores 各 chunk_id → RRF 融合分數（debug / metric 用）
	FusedScores map[string]float64
}

// Search 執行 hybrid 檢索：
//  1. 並發跑 dense + sparse（goroutine）
//  2. RRF 融合（不需要重排原分數，只看 rank）
//  3. 取 top-K（過 MinScore 門檻）
//  4. 回填 SearchHit 完整資料（dense 有 text，sparse 也有；fusion 後合併）
//
// query 為文字 query；服務內部會自動產生 dense embedding。
func (h *HybridSearcher) Search(ctx context.Context, agentID, query string, opts *HybridSearchOptions) (*SearchResult, error) {
	if agentID == "" {
		return nil, errors.New("hybrid: agent_id required")
	}
	if query == "" {
		return nil, errors.New("hybrid: query required")
	}

	o := h.defaults
	if opts != nil {
		if opts.DenseTopN > 0 {
			o.DenseTopN = opts.DenseTopN
		}
		if opts.SparseTopN > 0 {
			o.SparseTopN = opts.SparseTopN
		}
		if opts.FinalTopK > 0 {
			o.FinalTopK = opts.FinalTopK
		}
		if opts.RRFK > 0 {
			o.RRFK = opts.RRFK
		}
		if opts.MinScore > 0 {
			o.MinScore = opts.MinScore
		}
	}

	// 1. 並發查兩路
	type pathResult struct {
		hits []SearchHit
		err  error
	}
	denseCh := make(chan pathResult, 1)
	sparseCh := make(chan pathResult, 1)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		hits, err := h.dense.SearchByText(ctx, agentID, query, o.DenseTopN)
		denseCh <- pathResult{hits: hits, err: err}
	}()

	go func() {
		defer wg.Done()
		hits, err := h.sparse.Search(ctx, agentID, query, o.SparseTopN)
		sparseCh <- pathResult{hits: hits, err: err}
	}()

	wg.Wait()
	close(denseCh)
	close(sparseCh)

	dr := <-denseCh
	sr := <-sparseCh

	// 任一路失敗仍嘗試用另一路；兩路皆失敗才報錯
	if dr.err != nil && sr.err != nil {
		return nil, fmt.Errorf("hybrid: both paths failed (dense=%v, sparse=%v)", dr.err, sr.err)
	}

	denseHits := dr.hits
	sparseHits := sr.hits

	// 2. RRF 融合
	fused := FuseRRF([][]SearchHit{denseHits, sparseHits}, o.RRFK)

	// 3. 過 MinScore（融合後分數）+ 取 top-K
	if o.MinScore > 0 {
		filtered := fused[:0]
		for _, h := range fused {
			if float64(h.Similarity) >= o.MinScore {
				filtered = append(filtered, h)
			}
		}
		fused = filtered
	}
	if len(fused) > o.FinalTopK {
		fused = fused[:o.FinalTopK]
	}

	// 4. 收集分數 map（observability）
	scores := make(map[string]float64, len(fused))
	for _, h := range fused {
		scores[h.ChunkID] = float64(h.Similarity)
	}

	return &SearchResult{
		Hits:        fused,
		DenseCount:  len(denseHits),
		SparseCount: len(sparseHits),
		FusedScores: scores,
	}, nil
}

// ============================================================================
// 純函式：RRF 融合
// ============================================================================

// FuseRRF 將多路排序結果以 RRF 公式融合為單一排序。
//
//	score(doc) = Σ 1 / (k + rank_i(doc))
//
// 各 list 假設已按相關性降序排列（list[0] 為該路第 1 名）。
// 結果 Similarity 欄位裝 RRF 分數（取代原始 dense similarity / sparse BM25）。
//
// 內容（Text / DocID 等）來源優先順序：第一個遇到該 chunk_id 的 list。
// 各路缺漏的 doc 自動 score=0 不計入。
func FuseRRF(lists [][]SearchHit, k int) []SearchHit {
	if k <= 0 {
		k = DefaultRRFK
	}

	type entry struct {
		hit   SearchHit
		score float64
	}
	merged := make(map[string]*entry)

	for _, list := range lists {
		for rank, h := range list {
			s := 1.0 / float64(k+rank+1) // rank 是 0-based，公式用 1-based 故 +1
			if e, ok := merged[h.ChunkID]; ok {
				e.score += s
				// 缺欄位時補
				if e.hit.Text == "" && h.Text != "" {
					e.hit.Text = h.Text
				}
				if e.hit.DocID == "" && h.DocID != "" {
					e.hit.DocID = h.DocID
				}
				if e.hit.AgentID == "" && h.AgentID != "" {
					e.hit.AgentID = h.AgentID
				}
			} else {
				merged[h.ChunkID] = &entry{hit: h, score: s}
			}
		}
	}

	// 收集到 slice 並排序
	out := make([]SearchHit, 0, len(merged))
	for _, e := range merged {
		hit := e.hit
		hit.Similarity = float32(e.score) // RRF 分數覆寫到 Similarity
		out = append(out, hit)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Similarity != out[j].Similarity {
			return out[i].Similarity > out[j].Similarity
		}
		return out[i].ChunkID < out[j].ChunkID // tie-break 確保確定性
	})
	return out
}
