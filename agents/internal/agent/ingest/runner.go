// Package ingest — Runner 實作 services.IngestRunner 介面
//
// RunSkill 流程：
//  1. 讀 SKILL.md（req.SkillPath）
//  2. persona.Parse() → ParseResult
//  3. UpsertPersonal  → personal_memory（每 agent 一筆，ON CONFLICT 覆寫）
//  4. UpsertProcedural × N → procedural_memory（每個心智模型一筆）
//  5. 更新 job.TotalChunks / DoneChunks（IngestService 呼叫完 Run* 後同步到 DB）
//
// RunReferences 流程：
//  1. 對每個 path：讀檔 → 切塊（DocumentChunker）→ UpsertTexts（agent_embedding）
//  2. 需 KnowledgeStore（由 WithKnowledgeStore 注入，指向 agent_embedding HTTP service）
package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"agent/app/models"
	"agent/internal/agent/persona"
	"agent/internal/agent/vectordb"
)

// MemoryWriter 供 Runner 依賴注入；由 app/services.MemoryService 滿足。
// 只暴露 Runner 實際需要的方法，避免對 MemoryService 完整型別的依賴。
type MemoryWriter interface {
	UpsertPersonal(ctx context.Context, req models.CreatePersonalMemoryRequest) (*models.PersonalMemory, error)
	UpsertProcedural(ctx context.Context, req models.CreateProceduralMemoryRequest) (*models.ProceduralMemory, error)
}

// KnowledgeStore 將文字 chunks 寫入向量知識庫（embed + store）。
// 由 vectordb.HTTPClient 滿足（轉發至 agent_embedding service）。
type KnowledgeStore interface {
	UpsertTexts(ctx context.Context, agentID, docID string, chunks []vectordb.UpsertText) error
}

// RunnerOption 供 NewRunner 進行函數式選項注入。
type RunnerOption func(*Runner)

// WithKnowledgeStore 注入 KnowledgeStore 以啟用 RunReferences。
func WithKnowledgeStore(s KnowledgeStore) RunnerOption {
	return func(r *Runner) { r.store = s }
}

// Runner 實作 services.IngestRunner
type Runner struct {
	mem   MemoryWriter
	store KnowledgeStore // nil → RunReferences 不可用
}

// NewRunner 建立 Runner；mem 不得為 nil。
// 可透過 WithKnowledgeStore 啟用 RunReferences。
func NewRunner(mem MemoryWriter, opts ...RunnerOption) *Runner {
	if mem == nil {
		panic("ingest.NewRunner: mem must not be nil")
	}
	r := &Runner{mem: mem}
	for _, o := range opts {
		o(r)
	}
	return r
}

// RunSkill 讀取並解析 SKILL.md，將 persona 資料 upsert 到 PostgreSQL。
// job 的 TotalChunks / DoneChunks 欄位在此更新，由 IngestService 統一寫回 DB。
func (r *Runner) RunSkill(ctx context.Context, job *models.IngestJob, req models.IngestSkillRequest) error {
	// 1. 讀檔
	raw, err := os.ReadFile(req.SkillPath)
	if err != nil {
		return fmt.Errorf("runner: read %s: %w", req.SkillPath, err)
	}

	// 2. 解析
	result := persona.Parse(string(raw))
	if result.IsEmpty() {
		return fmt.Errorf("runner: SKILL.md at %s parsed to empty result — check format", req.SkillPath)
	}

	// 計算總工作量：1 筆 personal + N 筆 procedural
	total := len(result.MentalModels)
	if result.ToPersonalMemoryRequest(req.AgentID) != nil {
		total++
	}
	job.TotalChunks = total

	done := 0

	// 3. Upsert PersonalMemory
	if pmReq := result.ToPersonalMemoryRequest(req.AgentID); pmReq != nil {
		if _, err := r.mem.UpsertPersonal(ctx, *pmReq); err != nil {
			return fmt.Errorf("runner: upsert personal memory for agent %s: %w", req.AgentID, err)
		}
		done++
		job.DoneChunks = done
	}

	// 4. Upsert ProceduralMemory（逐筆）
	for _, procReq := range result.ToProceduralMemoryRequests(req.AgentID) {
		if _, err := r.mem.UpsertProcedural(ctx, procReq); err != nil {
			return fmt.Errorf("runner: upsert procedural memory %q for agent %s: %w",
				procReq.ModelName, req.AgentID, err)
		}
		done++
		job.DoneChunks = done
	}

	return nil
}

// RunReferences 知識攝取管線（references/*.md → chunk → embed → Cassandra）。
// 需要 WithKnowledgeStore 注入 KnowledgeStore（agent_embedding HTTP client）。
func (r *Runner) RunReferences(ctx context.Context, job *models.IngestJob, req models.IngestReferencesRequest) error {
	// 初始估計：路徑數（向下相容舊行為；有 store 時會更新為實際 chunk 數）
	job.TotalChunks = len(req.Paths)
	job.DoneChunks = 0

	if r.store == nil {
		return fmt.Errorf("runner: RunReferences requires a KnowledgeStore — set embedding.mode=http and embedding.http_base_url in config")
	}

	chunker := NewChunkerRouter(nil) // DocumentChunker only（無 LLM 切塊）
	job.TotalChunks = 0

	for _, path := range req.Paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("runner: read %s: %w", path, err)
		}

		chunks, err := chunker.Chunk(ctx, ChunkInput{Path: path, Body: string(raw)})
		if err != nil {
			return fmt.Errorf("runner: chunk %s: %w", path, err)
		}
		if len(chunks) == 0 {
			continue
		}

		job.TotalChunks += len(chunks)

		docID := docIDFromPath(path)
		items := make([]vectordb.UpsertText, len(chunks))
		for i, ch := range chunks {
			items[i] = vectordb.UpsertText{
				ChunkID: fmt.Sprintf("%s:%d", docID, ch.Index),
				Text:    ch.Text,
			}
		}

		if err := r.store.UpsertTexts(ctx, req.AgentID, docID, items); err != nil {
			return fmt.Errorf("runner: upsert %s: %w", path, err)
		}

		job.DoneChunks += len(chunks)
	}

	return nil
}

// docIDFromPath 從檔案路徑提取文件 ID（檔名去副檔名）
func docIDFromPath(path string) string {
	base := filepath.Base(path)
	if ext := filepath.Ext(base); ext != "" {
		return base[:len(base)-len(ext)]
	}
	return base
}
