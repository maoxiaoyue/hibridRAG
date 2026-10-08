package controllers

import (
	"net/http"

	hypctx "github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_embedding/app/models"
	"agent_embedding/app/services"
)

// Upsert POST /api/upsert
//
// Embeds chunk texts and stores them in the vector DB under (agent_id, doc_id).
func Upsert(ctx *hypctx.Context) {
	var req models.UpsertRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.AgentID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "agent_id is required"})
		return
	}
	if len(req.Chunks) == 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "chunks is required"})
		return
	}

	ids, err := svc.UpsertChunks(ctx.Request.Context(), req.AgentID, req.DocID, req.Chunks)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, models.UpsertResponse{
		Upserted: len(ids),
		ChunkIDs: ids,
	})
}

// Search POST /api/search
//
// Vector similarity search. Body must provide either `query` (text) or `vector`.
// Filters by `agent_id`. Returns up to top_k hits (default 5).
func Search(ctx *hypctx.Context) {
	var req models.SearchRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.AgentID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "agent_id is required"})
		return
	}
	if req.Query == "" && len(req.Vector) == 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "either query or vector is required"})
		return
	}

	topK := req.TopK
	if topK <= 0 {
		topK = 5
	}

	var (
		hits []services.SearchHit
		err  error
	)
	c := ctx.Request.Context()
	if len(req.Vector) > 0 {
		hits, err = svc.SearchByVector(c, req.AgentID, req.Vector, topK)
	} else {
		hits, err = svc.SearchByText(c, req.AgentID, req.Query, topK)
	}
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, models.SearchResponse{
		Results: hits,
		Total:   len(hits),
	})
}

// DeleteByDoc DELETE /api/chunks?agent_id=...&doc_id=...
//
// Removes every chunk that matches the supplied agent_id + doc_id pair.
func DeleteByDoc(ctx *hypctx.Context) {
	agentID := ctx.Query("agent_id")
	docID := ctx.Query("doc_id")
	if agentID == "" || docID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "agent_id and doc_id are required"})
		return
	}
	n, err := svc.DeleteByDoc(ctx.Request.Context(), agentID, docID)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, models.DeleteResponse{Deleted: n})
}
