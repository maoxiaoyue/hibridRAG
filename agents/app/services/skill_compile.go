// skill_compile.go — CompilingSkillLoader：包一層 *SkillLoader，讓每次「真的
// 重讀」SKILL.md 都自動觸發 persona.Parse + RoleCompiler.Compile，並快取解析
// 出的模型偏好供 AgentServer 在推理時查詢（見 resolveLLM）。
//
// SkillLoader 本身維持純檔案 I/O + cache，不知道 persona/ModelRegistry 存在；
// 這一層是唯一串起三者的接縫。
package services

import (
	"log"
	"sync"

	"agent/app/models"
	"agent/internal/agent/persona"
)

// ModelResolution 是某個 agentID 已解析出的模型偏好。
type ModelResolution struct {
	Role  ModelRole
	Model string // 已解析出的具體 model tag；"" 表示無覆寫，呼叫端應 fallback 用預設 LLM
}

// CompilingSkillLoader 包一層 *SkillLoader，滿足跟它完全相同的 Get/Load 簽名
// （透過 embedding），額外多做「reload 後自動編譯」與「查詢已編譯結果」兩件事。
type CompilingSkillLoader struct {
	*SkillLoader
	Compiler *RoleCompiler

	mu         sync.RWMutex
	modelPrefs map[string]*ModelResolution // agentID → 已解析結果
}

// NewCompilingSkillLoader 包裝既有的 loader + compiler。
func NewCompilingSkillLoader(loader *SkillLoader, compiler *RoleCompiler) *CompilingSkillLoader {
	return &CompilingSkillLoader{
		SkillLoader: loader,
		Compiler:    compiler,
		modelPrefs:  make(map[string]*ModelResolution),
	}
}

// Load 覆寫嵌入的 SkillLoader.Load：委派做實際的檔案 I/O，只有「真的重讀」
// （cached == false，含首次載入）才觸發 persona.Parse + Compiler.Compile 並
// 更新 modelPrefs；cache 命中時完全跳過，不重複解析/寫檔。
func (c *CompilingSkillLoader) Load(agentID string, force bool) (pack *models.SkillPack, cached bool, reloaded []string, err error) {
	pack, cached, reloaded, err = c.SkillLoader.Load(agentID, force)
	if err != nil || pack == nil || cached {
		return pack, cached, reloaded, err
	}

	parsed := persona.Parse(pack.Body)
	model, role, cerr := c.Compiler.Compile(agentID, parsed)
	if cerr != nil {
		log.Printf("[role-compiler] compile failed for agent=%s: %v", agentID, cerr)
	}

	c.mu.Lock()
	c.modelPrefs[agentID] = &ModelResolution{Role: role, Model: model}
	c.mu.Unlock()

	return pack, cached, reloaded, err
}

// ModelResolutionFor 回傳 agentID 目前已快取的模型解析結果。
// 回傳 (nil, false) 表示這個 agentID 從未經過 Load()（例如只呼叫過 Get()、
// 或該 agentID 根本沒有 SkillPack）——呼叫端應 fallback 用預設 LLM。
func (c *CompilingSkillLoader) ModelResolutionFor(agentID string) (*ModelResolution, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res, ok := c.modelPrefs[agentID]
	return res, ok
}
