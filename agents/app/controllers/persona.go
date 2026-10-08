// persona.go — persona 模型設定端點（內網 / dev_090 專用；gateway 不轉發）。
//
//	GET /api/personas                  列出所有 persona + 目前模型
//	GET /api/personas/:agent_id        單一 persona 現況
//	PUT /api/personas/:agent_id/model  設定該 persona 推理模型（寫 SKILL.md ## Model + reload）
//	GET /api/personas/:agent_id/skill  取得該 persona SKILL.md 完整原始文字
//	PUT /api/personas/:agent_id/skill  整份覆寫該 persona SKILL.md + reload
package controllers

import (
	"net/http"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	"agent/app/services"
)

var personaAdmin *services.PersonaAdmin

// SetPersonaAdmin 由 main.go 注入（skillsDir + compilingLoader）。
func SetPersonaAdmin(a *services.PersonaAdmin) { personaAdmin = a }

// SetPersonaModelRequest PUT /api/personas/:agent_id/model 的 body。
type SetPersonaModelRequest struct {
	Model string `json:"model"`
	Role  string `json:"role,omitempty"`
}

func ListPersonas(ctx *context.Context) {
	if personaAdmin == nil {
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, H{"error": "persona admin not configured"})
		return
	}
	list, err := personaAdmin.List()
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": H{"personas": list, "total": len(list)}})
}

func GetPersona(ctx *context.Context) {
	if personaAdmin == nil {
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, H{"error": "persona admin not configured"})
		return
	}
	info, err := personaAdmin.Get(ctx.Param("agent_id"))
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": info})
}

func SetPersonaModel(ctx *context.Context) {
	if personaAdmin == nil {
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, H{"error": "persona admin not configured"})
		return
	}
	var req SetPersonaModelRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	info, err := personaAdmin.SetModel(ctx.Param("agent_id"), req.Model, req.Role)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": info})
}

// SetPersonaSkillRequest PUT /api/personas/:agent_id/skill 的 body。
type SetPersonaSkillRequest struct {
	Content string `json:"content"`
}

func GetPersonaSkill(ctx *context.Context) {
	if personaAdmin == nil {
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, H{"error": "persona admin not configured"})
		return
	}
	content, err := personaAdmin.GetSkillMD(ctx.Param("agent_id"))
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": H{"content": content}})
}

func SetPersonaSkill(ctx *context.Context) {
	if personaAdmin == nil {
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, H{"error": "persona admin not configured"})
		return
	}
	var req SetPersonaSkillRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	info, err := personaAdmin.SetSkillMD(ctx.Param("agent_id"), req.Content)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, H{"success": true, "data": info})
}
