// Package services — sparse_searcher.go: 稀疏向量檢索（Hybrid Search 的 sparse 路徑）
//
// 對應架構 v2：Vector Search ☆ — dense / sparse / hybrid (RRF)
//
// 兩種實作：
//
//   1. PGTextSearcher — PostgreSQL 內建 to_tsvector full-text search。
//      作為一種「不需 sparse 向量資料」的 sparse 候選：對 PG `chunks.text`
//      做 FTS，回 ts_rank 作為相似度。中文情境建議將 FTSConfig 設為
//      'jiebacfg' / 'zh'（需 pg_jieba / zhparser extension）。
//
//   2. nopSparseSearcher — 永遠回空；HybridSearcher 退化為 dense-only。
//      2026-05-08 決議放棄真正的 sparse 向量路徑（Ollama BGE-M3 backend 不出
//      sparse），預設策略走 nop / fts。
package services

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SparseSearcher Hybrid Search 中 sparse 路徑的抽象介面。
type SparseSearcher interface {
	Search(ctx context.Context, agentID, query string, topK int) ([]SearchHit, error)
}

// nopSparseSearcher 永遠回空（disabled 時用）。
type nopSparseSearcher struct{}

func (nopSparseSearcher) Search(_ context.Context, _, _ string, _ int) ([]SearchHit, error) {
	return nil, nil
}

// NewNopSparseSearcher returns a no-op sparse searcher（hybrid → dense-only）。
func NewNopSparseSearcher() SparseSearcher { return nopSparseSearcher{} }

// ============================================================================
// PGTextSearcher — PostgreSQL FTS（不需 sparse 向量資料）
// ============================================================================

// PGTextSearcher 使用 `to_tsvector(cfg, text) @@ plainto_tsquery(cfg, ?)`
// 對 PG `chunks` 表做 full-text search。中文情境建議將 FTSConfig 設為 'jiebacfg'
// 或 'zh'（需 pg_jieba / zhparser extension）。
//
// 此 searcher 不查向量；它是「無 sparse 向量資料時」的 hybrid sparse 路徑替身。
type PGTextSearcher struct {
	db        *sql.DB
	FTSConfig string
}

// NewPGTextSearcher 建立 FTS-based sparse searcher。db 必填。
func NewPGTextSearcher(db *sql.DB) *PGTextSearcher {
	if db == nil {
		panic("services.NewPGTextSearcher: db must not be nil")
	}
	return &PGTextSearcher{db: db, FTSConfig: "simple"}
}

// Search 執行 FTS 並回傳 SearchHit，相似度欄位填 ts_rank 分數。
func (s *PGTextSearcher) Search(ctx context.Context, agentID, query string, topK int) ([]SearchHit, error) {
	if agentID == "" {
		return nil, fmt.Errorf("sparse: agent_id required")
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if topK <= 0 {
		topK = 20
	}

	stmt := `
		SELECT id, agent_id, doc_id, text,
		       ts_rank(to_tsvector($1, text), plainto_tsquery($1, $2)) AS score
		FROM chunks
		WHERE agent_id = $3
		  AND to_tsvector($1, text) @@ plainto_tsquery($1, $2)
		ORDER BY score DESC
		LIMIT $4
	`
	rows, err := s.db.QueryContext(ctx, stmt, s.FTSConfig, q, agentID, topK)
	if err != nil {
		return nil, fmt.Errorf("sparse FTS query: %w", err)
	}
	defer rows.Close()

	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		var score sql.NullFloat64
		if err := rows.Scan(&h.ChunkID, &h.AgentID, &h.DocID, &h.Text, &score); err != nil {
			return nil, fmt.Errorf("sparse FTS scan: %w", err)
		}
		if score.Valid {
			h.Similarity = float32(score.Float64)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
