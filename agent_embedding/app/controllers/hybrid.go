package controllers

import (
	"net/http"

	hypctx "github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_embedding/app/models"
	"agent_embedding/app/services"
)

// HybridSearch POST /api/search/hybrid
//
// 對應架構 v2：Vector Search ☆ — dense + sparse + RRF。
//
// 流程：
//  1. dense ANN（Cassandra）並發 sparse FTS（PostgreSQL）
//  2. RRF 融合（k 預設 60）
//  3. 取 top-K 回傳
//
// 若 HybridSearcher 未注入（PG 未配置）→ 回 503 Service Unavailable。
func HybridSearch(ctx *hypctx.Context) {
	if hybridSearch == nil {
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, H{
			"error": "hybrid search disabled (postgres not configured)",
		})
		return
	}

	var req models.HybridSearchRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{
			"error": "invalid request", "details": err.Error(),
		})
		return
	}
	if req.AgentID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "agent_id is required"})
		return
	}
	if req.Query == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "query is required"})
		return
	}

	opts := &services.HybridSearchOptions{
		DenseTopN:  req.DenseTopN,
		SparseTopN: req.SparseTopN,
		FinalTopK:  req.FinalTopK,
		RRFK:       req.RRFK,
		MinScore:   req.MinScore,
	}

	result, err := hybridSearch.Search(ctx.Request.Context(), req.AgentID, req.Query, opts)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}

	rrfK := req.RRFK
	if rrfK <= 0 {
		rrfK = services.DefaultRRFK
	}

	ctx.JSON(http.StatusOK, models.HybridSearchResponse{
		Results:     result.Hits,
		Total:       len(result.Hits),
		DenseCount:  result.DenseCount,
		SparseCount: result.SparseCount,
		RRFK:        rrfK,
	})
}
