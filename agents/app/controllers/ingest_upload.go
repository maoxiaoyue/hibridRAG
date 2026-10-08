// ingest_upload.go — POST /api/ingest/upload：multipart 上傳 + PDF/圖片抽文字。
//
// 後端原本不能解析二進位文件（ingest 只吃伺服器磁碟上的文字路徑）。本端點：
//   1. 收 multipart 檔案（PDF / 圖片 / 文字）
//   2. 落地到 uploads/{agent_id}/
//   3. 依型別抽成純文字 .txt sidecar：
//        - .txt/.md/.json → 原檔直用
//        - .pdf           → ledongthuc/pdf 抽文字（純文字型 PDF；掃描型不 OCR）
//        - 圖片           → VisionExtractor（Ollama 視覺模型）
//   4. 走既有 EnqueueReferences（chunk → embed → Cassandra）
//   5. 回 202 + jobID；client 用 GET /api/ingest/jobs/:id 輪詢
package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	hypctx "github.com/maoxiaoyue/hypgo/pkg/context"

	"github.com/ledongthuc/pdf"

	"agent/app/models"
	"agent/app/services"
	"agent/internal/logger"
)

// ── 注入（仿 SetIngestRunner） ──────────────────────────────────────────────

var (
	uploadMu      sync.RWMutex
	uploadsDir    = "./uploads"
	visionClient  *services.VisionExtractor
)

// SetUploadConfig 由 main.go 注入上傳落地目錄與（可選）視覺抽文字 client。
func SetUploadConfig(dir string, vision *services.VisionExtractor) {
	uploadMu.Lock()
	defer uploadMu.Unlock()
	if strings.TrimSpace(dir) != "" {
		uploadsDir = dir
	}
	visionClient = vision
}

func uploadSettings() (string, *services.VisionExtractor) {
	uploadMu.RLock()
	defer uploadMu.RUnlock()
	return uploadsDir, visionClient
}

const maxUploadBytes = 64 << 20 // 64 MiB

// IngestUpload POST /api/ingest/upload
func IngestUpload(ctx *hypctx.Context) {
	if err := ctx.Request.ParseMultipartForm(maxUploadBytes); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid multipart form", "details": err.Error()})
		return
	}
	agentID := strings.TrimSpace(ctx.Request.FormValue("agent_id"))
	if agentID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "agent_id required"})
		return
	}
	tags := splitTags(ctx.Request.FormValue("tags"))

	if ctx.Request.MultipartForm == nil || len(ctx.Request.MultipartForm.File["files"]) == 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "no files (field name must be 'files')"})
		return
	}
	files := ctx.Request.MultipartForm.File["files"]

	dir := filepath.Join(uploadsDirFor(agentID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Error("upload: mkdir %s: %v", dir, err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "cannot create upload dir"})
		return
	}

	var (
		paths    []string
		skipped  []H
		reqCtx   = ctx.Request.Context()
	)
	for _, fh := range files {
		rawPath, err := saveUpload(dir, fh.Filename, fh)
		if err != nil {
			skipped = append(skipped, H{"file": fh.Filename, "error": err.Error()})
			continue
		}
		textPath, err := extractToText(reqCtx, rawPath, fh.Filename)
		if err != nil {
			skipped = append(skipped, H{"file": fh.Filename, "error": err.Error()})
			continue
		}
		paths = append(paths, textPath)
	}

	if len(paths) == 0 {
		ctx.AbortWithStatusJSON(http.StatusUnprocessableEntity, H{
			"error":   "no ingestible files (all failed extraction)",
			"skipped": skipped,
		})
		return
	}

	job, err := getIngestService().EnqueueReferences(models.IngestReferencesRequest{
		AgentID: agentID,
		Paths:   paths,
		Tags:    tags,
	})
	if err != nil {
		logger.Error("upload: enqueue references: %v", err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "enqueue failed", "details": err.Error()})
		return
	}

	ctx.JSON(http.StatusAccepted, H{
		"success": true,
		"data":    models.IngestJobResponse{JobID: job.ID, Status: job.Status},
		"ingested_files": len(paths),
		"skipped":        skipped,
	})
}

// ── helpers ─────────────────────────────────────────────────────────────────

func uploadsDirFor(agentID string) string {
	dir, _ := uploadSettings()
	return filepath.Join(dir, sanitizeName(agentID))
}

// saveUpload 把上傳檔案存到 dir，回傳落地路徑（含隨機前綴避免覆蓋）。
func saveUpload(dir, filename string, fh *multipart.FileHeader) (string, error) {
	src, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("open upload: %w", err)
	}
	defer src.Close()

	dst := filepath.Join(dir, randPrefix()+"-"+sanitizeName(filename))
	out, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, src); err != nil {
		return "", fmt.Errorf("write %s: %w", dst, err)
	}
	return dst, nil
}

// extractToText 依副檔名把檔案抽成可攝取的文字檔路徑。
// 文字類直接回原路徑；PDF / 圖片寫 .txt sidecar 後回 sidecar 路徑。
func extractToText(ctx context.Context, rawPath, origName string) (string, error) {
	ext := strings.ToLower(filepath.Ext(origName))
	switch ext {
	case ".txt", ".md", ".markdown", ".json":
		return rawPath, nil

	case ".pdf":
		text, err := extractPDFText(rawPath)
		if err != nil {
			return "", fmt.Errorf("pdf extract: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("pdf has no extractable text (likely scanned; OCR not supported in v1)")
		}
		return writeSidecar(rawPath, text)

	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp":
		_, vision := uploadSettings()
		if vision == nil {
			return "", fmt.Errorf("image extraction disabled (ingest.vision not configured)")
		}
		raw, err := os.ReadFile(rawPath)
		if err != nil {
			return "", fmt.Errorf("read image: %w", err)
		}
		text, err := vision.Extract(ctx, raw)
		if err != nil {
			return "", fmt.Errorf("vision extract: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("vision returned empty text")
		}
		return writeSidecar(rawPath, text)

	default:
		return "", fmt.Errorf("unsupported file type %q", ext)
	}
}

// extractPDFText 用 ledongthuc/pdf 抽純文字（純文字型 PDF）。
func extractPDFText(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	plain, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if _, err := io.Copy(&sb, plain); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// writeSidecar 把抽出的文字寫成 rawPath + ".txt" 並回傳該路徑。
func writeSidecar(rawPath, text string) (string, error) {
	side := rawPath + ".txt"
	if err := os.WriteFile(side, []byte(text), 0o644); err != nil {
		return "", fmt.Errorf("write sidecar: %w", err)
	}
	return side, nil
}

func splitTags(s string) []string {
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

// sanitizeName 去掉路徑分隔與 .. 片段，避免目錄穿越。
func sanitizeName(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "..", "")
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		return "file"
	}
	return name
}

func randPrefix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
