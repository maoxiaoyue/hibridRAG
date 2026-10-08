// chat_image.go — POST /api/chat/image：對話附加圖片的辨識端點（內部；經 gateway ChatImage 轉發）。
//
// 流程（重用 ingest_upload 的 extractToText / EnqueueReferences）：
//   1. 收原始圖片 bytes（query 帶 agent_id / filename）
//   2. 落地 uploads/{agent_id}/ → VisionExtractor 抽文字（OCR / 描述）
//   3. 同時 EnqueueReferences 進 RAG（best-effort，未來可檢索）
//   4. 回傳抽出的文字 → client 把它併進當下這輪對話（讓 agent「辨識」圖片內容）
package controllers

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	hypctx "github.com/maoxiaoyue/hypgo/pkg/context"

	"agent/app/models"
	"agent/internal/logger"
)

const maxChatImageBytes = 32 << 20 // 32 MiB

// ChatImage POST /api/chat/image?agent_id=&filename=（body = 原始圖片 bytes）
func ChatImage(ctx *hypctx.Context) {
	agentID := strings.TrimSpace(ctx.Query("agent_id"))
	if agentID == "" {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "agent_id required"})
		return
	}
	filename := strings.TrimSpace(ctx.Query("filename"))
	if filename == "" {
		filename = "image.png"
	}

	body, err := io.ReadAll(io.LimitReader(ctx.Request.Body, maxChatImageBytes+1))
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "read body failed", "details": err.Error()})
		return
	}
	if len(body) == 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "empty image"})
		return
	}
	if len(body) > maxChatImageBytes {
		ctx.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, H{"error": "image too large (max 32 MiB)"})
		return
	}

	dir := uploadsDirFor(agentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Error("chat image: mkdir %s: %v", dir, err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "cannot create upload dir"})
		return
	}
	rawPath := filepath.Join(dir, randPrefix()+"-"+sanitizeName(filename))
	if err := os.WriteFile(rawPath, body, 0o644); err != nil {
		logger.Error("chat image: write %s: %v", rawPath, err)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "cannot save image"})
		return
	}

	// 抽文字（圖片 → VisionExtractor；未設定視覺模型時回錯）。
	textPath, err := extractToText(ctx.Request.Context(), rawPath, filename)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnprocessableEntity, H{"error": "image recognition failed", "details": err.Error()})
		return
	}
	textBytes, err := os.ReadFile(textPath)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": "read extracted text failed"})
		return
	}
	text := strings.TrimSpace(string(textBytes))

	// 同時 ingest 進 RAG（best-effort，不阻擋當下對話回覆）。
	if _, err := getIngestService().EnqueueReferences(models.IngestReferencesRequest{
		AgentID: agentID,
		Paths:   []string{textPath},
		Tags:    []string{"chat-image"},
	}); err != nil {
		logger.Error("chat image: ingest enqueue failed (non-fatal): %v", err)
	}

	ctx.JSON(http.StatusOK, H{"success": true, "data": H{"text": text, "chars": len(text)}})
}
