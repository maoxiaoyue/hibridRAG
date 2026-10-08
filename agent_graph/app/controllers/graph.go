package controllers

import (
	gocontext "context"
	"errors"
	"net/http"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent_graph/app/models"
	"agent_graph/app/services"
	"agent_graph/internal/logger"
)

var graphSvc *services.GraphFacade

// SetGraphFacade 由 main.go 注入已配置好的 GraphFacade
func SetGraphFacade(f *services.GraphFacade) { graphSvc = f }

func graphErrStatus(err error) int {
	switch {
	case errors.Is(err, services.ErrGraphDisabled), errors.Is(err, services.ErrVectorDisabled):
		return http.StatusServiceUnavailable
	case errors.Is(err, services.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, services.ErrAlreadyReviewed):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// SearchEntities POST /api/graph/search
// query 空字串是合法輸入（等同 Kùzu 端 CONTAINS ""，比對全部實體）——client 端
// 「進分頁先預載幾筆」的流程就是靠這個語意運作，不可拒絕空 query。
func SearchEntities(ctx *context.Context) {
	var req models.SearchEntitiesRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	entities, err := graphSvc.SearchEntities(ctx.Request.Context(), req.Query, req.Limit)
	if err != nil {
		logger.Error("search entities: %v", err)
		ctx.AbortWithStatusJSON(graphErrStatus(err), H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": entities})
}

// GetLocalContext POST /api/graph/context
func GetLocalContext(ctx *context.Context) {
	var req models.LocalContextRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.EntityName == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "entity_name is required"})
		return
	}
	resp, err := graphSvc.GetLocalContext(ctx.Request.Context(), req.EntityName, req.Hops, req.Limit)
	if err != nil {
		logger.Error("get local context: %v", err)
		ctx.AbortWithStatusJSON(graphErrStatus(err), H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": resp})
}

// ExecuteCypher POST /api/graph/cypher
func ExecuteCypher(ctx *context.Context) {
	var req models.ExecuteCypherRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.Query == "" || len(req.Columns) == 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "query and columns are required"})
		return
	}
	rows, err := graphSvc.ExecuteCypher(ctx.Request.Context(), req.Query, req.Params, req.Columns)
	if err != nil {
		logger.Error("execute cypher: %v", err)
		ctx.AbortWithStatusJSON(graphErrStatus(err), H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": models.ExecuteCypherResponse{Rows: rows}})
}

// HybridSearch POST /api/graph/hybrid_search
func HybridSearch(ctx *context.Context) {
	var req models.HybridSearchRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.Query == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "query is required"})
		return
	}
	resp, err := graphSvc.HybridSearch(ctx.Request.Context(), req.Query, req.TopK, req.Hops)
	if err != nil {
		logger.Error("hybrid search: %v", err)
		ctx.AbortWithStatusJSON(graphErrStatus(err), H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": resp})
}

// ProposeRelation POST /api/graph/propose_relation
func ProposeRelation(ctx *context.Context) {
	var req models.ProposeRelationRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.SourceEntity == "" || req.TargetEntity == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "source_entity and target_entity are required"})
		return
	}
	pr, err := graphSvc.ProposeRelation(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("propose relation: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, H{"success": true, "data": pr})
}

// ListProposed POST /api/graph/proposed/list
func ListProposed(ctx *context.Context) {
	var req models.ListProposedRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	list, err := graphSvc.ListProposed(ctx.Request.Context(), req.Status, req.Limit)
	if err != nil {
		logger.Error("list proposed: %v", err)
		ctx.AbortWithStatusJSON(graphErrStatus(err), H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": list})
}

// ApproveProposed POST /api/graph/proposed/approve
func ApproveProposed(ctx *context.Context) {
	reviewProposed(ctx, graphSvc.ApproveProposed)
}

// RejectProposed POST /api/graph/proposed/reject
func RejectProposed(ctx *context.Context) {
	reviewProposed(ctx, graphSvc.RejectProposed)
}

func reviewProposed(ctx *context.Context, do func(c gocontext.Context, id string) (*models.ProposedRelation, error)) {
	var req models.ReviewProposedRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.ID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "id is required"})
		return
	}
	pr, err := do(ctx.Request.Context(), req.ID)
	if err != nil {
		logger.Error("review proposed %s: %v", req.ID, err)
		ctx.AbortWithStatusJSON(graphErrStatus(err), H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": pr})
}

// ---- dev_090 graphic_input 配套：直接寫入 + 純文字攝取 ----

// UpsertEntity POST /api/graph/entity
func UpsertEntity(ctx *context.Context) {
	var req models.UpsertEntityRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.Name == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "name is required"})
		return
	}
	resp, err := graphSvc.UpsertEntityDirect(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("upsert entity: %v", err)
		ctx.AbortWithStatusJSON(graphErrStatus(err), H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": resp})
}

// UpsertRelationDirect POST /api/graph/relation
func UpsertRelationDirect(ctx *context.Context) {
	var req models.UpsertRelationRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.Source == "" || req.Target == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "source and target are required"})
		return
	}
	if err := graphSvc.UpsertRelationDirect(ctx.Request.Context(), req); err != nil {
		logger.Error("upsert relation: %v", err)
		ctx.AbortWithStatusJSON(graphErrStatus(err), H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true})
}

// IngestText POST /api/graph/ingest_text
func IngestText(ctx *context.Context) {
	var req models.IngestTextRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if req.Text == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "text is required"})
		return
	}
	resp, err := graphSvc.IngestText(ctx.Request.Context(), req)
	if err != nil {
		logger.Error("ingest text: %v", err)
		ctx.AbortWithStatusJSON(graphErrStatus(err), H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": resp})
}
