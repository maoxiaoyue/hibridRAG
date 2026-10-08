package controllers

import (
	"net/http"

	hypctx "github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_embedding/app/models"
)

// Embed POST /api/embed
//
// Single text → dense vector. Uses Redis cache when available.
func Embed(ctx *hypctx.Context) {
	var req models.EmbedRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.Text == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "text is required"})
		return
	}

	vec, cached, err := svc.EmbedOne(ctx.Request.Context(), req.Text)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, models.EmbedResponse{
		Vector:    vec,
		Dimension: len(vec),
		Model:     svc.Embedder().Model,
		Cached:    cached,
	})
}

// EmbedBatch POST /api/embed/batch
//
// Multiple texts → vectors in input order. Auto-batches by max_batch_size.
func EmbedBatch(ctx *hypctx.Context) {
	var req models.EmbedBatchRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if len(req.Texts) == 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "texts is required"})
		return
	}

	vecs, err := svc.EmbedBatch(ctx.Request.Context(), req.Texts)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	dim := 0
	if len(vecs) > 0 {
		dim = len(vecs[0])
	}
	ctx.JSON(http.StatusOK, models.EmbedBatchResponse{
		Vectors:   vecs,
		Dimension: dim,
		Model:     svc.Embedder().Model,
		Count:     len(vecs),
	})
}
