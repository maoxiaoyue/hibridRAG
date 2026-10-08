package models

import "agent_embedding/app/services"

// ── Vector search ───────────────────────────────────────────────────────────

type SearchRequest struct {
	AgentID string    `json:"agent_id"`
	Query   string    `json:"query,omitempty"`  // either query OR vector
	Vector  []float32 `json:"vector,omitempty"` // pre-computed vector path
	TopK    int       `json:"top_k,omitempty"`  // default 5
}

type SearchResponse struct {
	Results []services.SearchHit `json:"results"`
	Total   int                  `json:"total"`
}

// ── Delete ──────────────────────────────────────────────────────────────────

type DeleteResponse struct {
	Deleted int `json:"deleted"`
}

// ── Hybrid Search (Dense + Sparse + RRF Fusion) ─────────────────────────────

// HybridSearchRequest POST /api/search/hybrid 請求
//
// 任一 *_top_n / final_top_k / rrf_k / min_score 為 0 表示使用伺服器預設。
type HybridSearchRequest struct {
	AgentID     string  `json:"agent_id"`
	Query       string  `json:"query"`
	DenseTopN   int     `json:"dense_top_n,omitempty"`
	SparseTopN  int     `json:"sparse_top_n,omitempty"`
	FinalTopK   int     `json:"final_top_k,omitempty"`
	RRFK        int     `json:"rrf_k,omitempty"`
	MinScore    float64 `json:"min_score,omitempty"`
}

// HybridSearchResponse POST /api/search/hybrid 回應
type HybridSearchResponse struct {
	Results     []services.SearchHit `json:"results"`
	Total       int                  `json:"total"`
	DenseCount  int                  `json:"dense_count"`  // dense 路命中數（RRF 前）
	SparseCount int                  `json:"sparse_count"` // sparse 路命中數（RRF 前）
	RRFK        int                  `json:"rrf_k"`        // 實際使用的 k 值
}
