package routers

import (
	"agent/app/models"

	"github.com/maoxiaoyue/hypgo/pkg/schema"
)

// RegisterSkill 註冊 skill runtime REST 路由的 schema metadata
//
// 這組是 agent runtime 線：每次 Inbound 推理前載入 SKILL.md + references/ 作為上下文，
// 與 /api/ingest/skill（離線寫向量庫）不同。
func RegisterSkill() {
	// POST /api/skill/load
	schema.Global().Register(schema.Route{
		Protocol: "http",
		Method:   "POST",
		Path:     "/api/skill/load",
		Command:  "skill.Load",
		Summary:  "載入 skills/{agent_id}/SKILL.md 與 references/，依 mtime+checksum 決定是否重讀",
		Tags:     []string{"skill", "runtime"},
		Input:    models.LoadSkillRequest{},
		Output:   models.LoadSkillResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "loaded or cache hit"},
			400: {Description: "missing agent_id"},
			404: {Description: "skills/{agent_id} not found"},
			500: {Description: "read / hash failure"},
		},
	})

	// GET /api/skill/{agent_id}
	schema.Global().Register(schema.Route{
		Protocol: "http",
		Method:   "GET",
		Path:     "/api/skill/{agent_id}",
		Command:  "skill.Get",
		Summary:  "取得目前 cache 中的 SkillPack（不觸發重讀）",
		Tags:     []string{"skill", "runtime"},
		Input:    models.GetSkillRequest{},
		Output:   models.SkillPack{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			404: {Description: "not loaded"},
		},
	})

	// POST /api/skill/select
	schema.Global().Register(schema.Route{
		Protocol: "http",
		Method:   "POST",
		Path:     "/api/skill/select",
		Command:  "skill.SelectReferences",
		Summary:  "依 query 從 references/ 動態挑選片段；供 Inbound 推理前使用，亦可獨立除錯",
		Tags:     []string{"skill", "runtime", "retrieval"},
		Input:    models.SelectReferencesRequest{},
		Output:   models.SelectReferencesResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			400: {Description: "missing agent_id or query"},
			404: {Description: "agent not loaded"},
		},
	})
}
