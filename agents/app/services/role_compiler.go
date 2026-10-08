// role_compiler.go — 把 persona.ParseResult 的 ## Model 偏好解析成具體模型，
// 並把結果寫成 skills/{agent_id}/ROLE.md（衍生檔，供除錯/檢視用，人工仍只寫 SKILL.md）。
//
// 這是唯一同時認識 persona（純解析）、ModelRegistry（純模型解析）、檔案 I/O
// 三者的接縫；SkillLoader 本身維持純檔案 I/O + cache，不知道 persona/model 的存在。
package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent/internal/agent/persona"
)

// RoleCompiler 依 ApplyToOllamaConfig 相同的慣例解析 persona 的模型偏好：
//   - ModelPref 為 nil 或兩欄皆空 → 沒有覆寫，呼叫端應 fallback 用預設 LLM
//   - ModelPref.Model 非空 → 明確覆寫優先，直接採用
//   - 否則 ModelPref.Role 非空 → 透過 ModelRegistry.Resolve 解析成具體模型
type RoleCompiler struct {
	Registry *ModelRegistry
	RootDir  string // skills/ 根目錄，跟 SkillLoader 同一份
}

// NewRoleCompiler 建立 RoleCompiler。
func NewRoleCompiler(reg *ModelRegistry, rootDir string) *RoleCompiler {
	return &RoleCompiler{Registry: reg, RootDir: rootDir}
}

// Compile 解析 parsed.ModelPref 並寫出 skills/{agentID}/ROLE.md。
// 回傳值 resolvedModel/resolvedRole 是實際解析出的結果，不受寫檔成敗影響
// （寫檔失敗只透過 err 回報，呼叫端可選擇只 log 不中斷）。
func (c *RoleCompiler) Compile(agentID string, parsed *persona.ParseResult) (resolvedModel string, resolvedRole ModelRole, err error) {
	var pref *persona.ModelPreference
	if parsed != nil {
		pref = parsed.ModelPref
	}

	if pref != nil {
		switch {
		case strings.TrimSpace(pref.Model) != "":
			resolvedModel = strings.TrimSpace(pref.Model)
			resolvedRole = ModelRole(strings.TrimSpace(pref.Role)) // 僅供記錄，未參與解析
		case strings.TrimSpace(pref.Role) != "":
			resolvedRole = ModelRole(strings.TrimSpace(pref.Role))
			if c.Registry == nil {
				err = fmt.Errorf("role compiler: no ModelRegistry configured, cannot resolve role %q", resolvedRole)
			} else if spec, resolveErr := c.Registry.Resolve(resolvedRole); resolveErr != nil {
				err = resolveErr
			} else {
				resolvedModel = spec.Name
			}
		}
	}

	if writeErr := c.writeRoleMD(agentID, parsed, resolvedModel, resolvedRole); writeErr != nil {
		if err != nil {
			err = fmt.Errorf("%w; also failed to write ROLE.md: %v", err, writeErr)
		} else {
			err = fmt.Errorf("role compiler: write ROLE.md: %w", writeErr)
		}
	}
	return resolvedModel, resolvedRole, err
}

// writeRoleMD 寫出 skills/{agentID}/ROLE.md——純資訊性文件，不是 LLM prompt，
// 不重用 ToInstruction()。一律寫出（即使沒有覆寫），標示「(none)」以利可視化/除錯。
func (c *RoleCompiler) writeRoleMD(agentID string, parsed *persona.ParseResult, resolvedModel string, resolvedRole ModelRole) error {
	if c.RootDir == "" {
		return fmt.Errorf("role compiler: RootDir not set")
	}

	name := agentID
	if parsed != nil && parsed.AgentName != "" {
		name = parsed.AgentName
	}

	roleDisplay := "(none)"
	if resolvedRole != "" {
		roleDisplay = string(resolvedRole)
	}
	modelDisplay := "(none — 使用預設 LLM)"
	if resolvedModel != "" {
		modelDisplay = resolvedModel
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s — Resolved Role\n", name)
	sb.WriteString("<!-- 自動產生，請勿編輯；要改請改 SKILL.md -->\n\n")
	fmt.Fprintf(&sb, "- role: %s\n", roleDisplay)
	fmt.Fprintf(&sb, "- model: %s\n", modelDisplay)
	fmt.Fprintf(&sb, "- resolved_at: %s\n", time.Now().Format(time.RFC3339))

	dir := filepath.Join(c.RootDir, agentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "ROLE.md"), []byte(sb.String()), 0o644)
}
