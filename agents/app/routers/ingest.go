package routers

import (
	"agent/app/models"

	"github.com/maoxiaoyue/hypgo/pkg/schema"
)

// RegisterIngest 註冊知識攝取管線 REST 路由的 schema metadata。
//
// 對應 RAG pipeline 的「③ 知識攝取」與「④ 存儲」兩個階段：
//   - IngestSkill      — SKILL.md → persona.Parse → UpsertPersonal / UpsertProcedural（Phase 1，已實作）
//   - IngestReferences — references/*.md → Chunk → BGE-M3 → Cassandra/PG（Phase 2，待實作）
//   - IngestJob        — 非同步任務狀態查詢（pending / running / completed / failed）
func RegisterIngest() {
	// POST /api/ingest/skill
	schema.Global().Register(schema.Route{
		Protocol: "http",
		Method:   "POST",
		Path:     "/api/ingest/skill",
		Command:  "ingest.skill",
		Summary:  "觸發 SKILL.md 解析（persona.Parse）→ UpsertPersonal + UpsertProcedural 寫入 PG；非同步執行，立即回傳 job_id",
		Tags:     []string{"ingest", "skill"},
		Input:    models.IngestSkillRequest{},
		Output:   models.IngestJobResponse{},
		Responses: map[int]schema.ResponseSchema{
			202: {Description: "job accepted (async)"},
			400: {Description: "invalid request (missing agent_id or skill_path)"},
			500: {Description: "job creation failed"},
		},
	})

	// POST /api/ingest/references
	schema.Global().Register(schema.Route{
		Protocol: "http",
		Method:   "POST",
		Path:     "/api/ingest/references",
		Command:  "ingest.references",
		Summary:  "批次處理 references/*.md：Chunk → BGE-M3 Embed → Cassandra dense + PG sparse（Phase 2，RunReferences 待實作）",
		Tags:     []string{"ingest", "references"},
		Input:    models.IngestReferencesRequest{},
		Output:   models.IngestJobResponse{},
		Responses: map[int]schema.ResponseSchema{
			202: {Description: "job accepted (async)"},
			400: {Description: "invalid request (missing agent_id or paths)"},
			500: {Description: "job creation failed"},
		},
	})

	// GET /api/ingest/jobs/:id
	schema.Global().Register(schema.Route{
		Protocol: "http",
		Method:   "GET",
		Path:     "/api/ingest/jobs/:id",
		Command:  "ingest.job.Get",
		Summary:  "查詢攝取任務狀態；輪詢 status 直到 completed / failed，可從 total_chunks / done_chunks 算進度",
		Tags:     []string{"ingest"},
		Output:   models.IngestJob{},
		Params: []schema.ParamSchema{
			{Name: "id", In: "path", Required: true, Type: "string"},
		},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			404: {Description: "job not found"},
		},
	})
}
