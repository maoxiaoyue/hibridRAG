// Package controllers — graphic_input 的 REST（dev_090 client「Graph Input」分頁用）
// 與 gRPC handler；業務邏輯都在 services.InputService，這裡只做綁定與回應。
package controllers

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"graphic_input/app/models"
	"graphic_input/app/services"
)

// H JSON 回應 shorthand。
type H map[string]interface{}

var inputSvc *services.InputService

// SetInputService 由 main.go 注入。
func SetInputService(s *services.InputService) { inputSvc = s }

// HealthCheck GET /health
func HealthCheck(ctx *context.Context) {
	ctx.JSON(http.StatusOK, H{
		"status":         "ok",
		"vault_enabled":  inputSvc.Vault.Enabled(),
		"vision_enabled": inputSvc.Vision.Enabled(),
	})
}

// UploadDocs POST /api/input/upload（multipart：files[] + targets + tags + actor）
func UploadDocs(ctx *context.Context) {
	const maxUpload = 64 << 20
	if err := ctx.Request.ParseMultipartForm(maxUpload); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid multipart form", "details": err.Error()})
		return
	}
	targets := ctx.Request.FormValue("targets")
	actor := ctx.Request.FormValue("actor")
	tags := splitComma(ctx.Request.FormValue("tags"))

	if ctx.Request.MultipartForm == nil || len(ctx.Request.MultipartForm.File["files"]) == 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "no files (field name must be 'files')"})
		return
	}

	var files []services.DocFile
	for _, fh := range ctx.Request.MultipartForm.File["files"] {
		src, err := fh.Open()
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "open " + fh.Filename, "details": err.Error()})
			return
		}
		data, err := io.ReadAll(src)
		src.Close()
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "read " + fh.Filename, "details": err.Error()})
			return
		}
		files = append(files, services.DocFile{Name: fh.Filename, Data: data})
	}

	results, err := inputSvc.UploadDocuments(ctx.Request.Context(), targets, actor, tags, files)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": results})
}

// UpsertEntity POST /api/input/entity
func UpsertEntity(ctx *context.Context) {
	var req models.UpsertEntityRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	resp, err := inputSvc.UpsertEntity(ctx.Request.Context(), req)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": resp})
}

// UpsertRelation POST /api/input/relation
func UpsertRelation(ctx *context.Context) {
	var req models.UpsertRelationRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	if err := inputSvc.UpsertRelation(ctx.Request.Context(), req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true})
}

// CheckDuplicates POST /api/input/check
func CheckDuplicates(ctx *context.Context) {
	var req models.CheckDuplicatesRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	candidates, err := inputSvc.CheckDuplicates(ctx.Request.Context(), req.Name)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": candidates})
}

// BatchImport POST /api/input/batch
func BatchImport(ctx *context.Context) {
	var req models.BatchImportRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	rows, err := services.ParseBatch(req)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": err.Error()})
		return
	}
	resp := inputSvc.BatchImport(ctx.Request.Context(), req.Actor, rows)
	ctx.JSON(http.StatusOK, H{"success": true, "data": resp})
}

// ListLog GET /api/input/log?limit=
func ListLog(ctx *context.Context) {
	limit, _ := strconv.Atoi(ctx.Query("limit"))
	entries, err := inputSvc.ListLog(ctx.Request.Context(), limit)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": entries})
}

func splitComma(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
