// persona_admin.go — 讓外部（agent_client，內網）查詢/設定各 persona 的推理模型。
//
// 設定流程：把選定的 model/role 寫進 skills/{agent_id}/SKILL.md 的 ## Model 段
// （SKILL.md 是唯一事實來源），再 force reload → CompilingSkillLoader 觸發
// RoleCompiler 重生 ROLE.md + 刷新 ModelResolution，使 /api/chat 立即改用新模型。
package services

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agent/internal/agent/persona"
)

// PersonaInfo 一個 persona（agent_id）的模型設定現況。
type PersonaInfo struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Role    string `json:"role,omitempty"`    // SKILL.md ## Model 段的 role
	Model   string `json:"model,omitempty"`   // SKILL.md ## Model 段的明確 model tag
	RoleMD  string `json:"role_md,omitempty"` // 目前 ROLE.md 內容（衍生檔，供顯示）
}

// PersonaAdmin 操作 skills/ 下各 persona 的 SKILL.md/ROLE.md。
type PersonaAdmin struct {
	skillsDir string
	loader    *CompilingSkillLoader // force reload → 重生 ROLE.md + 刷新 ModelResolution
}

func NewPersonaAdmin(skillsDir string, loader *CompilingSkillLoader) *PersonaAdmin {
	return &PersonaAdmin{skillsDir: skillsDir, loader: loader}
}

func validAgentID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("agent_id is required")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("invalid agent_id %q", id)
	}
	return nil
}

func (a *PersonaAdmin) skillPath(id string) string { return filepath.Join(a.skillsDir, id, "SKILL.md") }
func (a *PersonaAdmin) rolePath(id string) string  { return filepath.Join(a.skillsDir, id, "ROLE.md") }

func (a *PersonaAdmin) readInfo(id string) (*PersonaInfo, error) {
	body, err := os.ReadFile(a.skillPath(id))
	if err != nil {
		return nil, err
	}
	parsed := persona.Parse(string(body))
	info := &PersonaInfo{AgentID: id, Name: id}
	if parsed.AgentName != "" {
		info.Name = parsed.AgentName
	}
	if parsed.ModelPref != nil {
		info.Role = parsed.ModelPref.Role
		info.Model = parsed.ModelPref.Model
	}
	if rb, err := os.ReadFile(a.rolePath(id)); err == nil {
		info.RoleMD = string(rb)
	}
	return info, nil
}

// List 掃 skills/ 列出所有含 SKILL.md 的 persona 目錄。
func (a *PersonaAdmin) List() ([]PersonaInfo, error) {
	entries, err := os.ReadDir(a.skillsDir)
	if err != nil {
		return nil, fmt.Errorf("read skills dir: %w", err)
	}
	out := make([]PersonaInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if _, err := os.Stat(a.skillPath(id)); err != nil {
			continue // 沒有 SKILL.md 的目錄跳過
		}
		info, err := a.readInfo(id)
		if err != nil {
			continue
		}
		out = append(out, *info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out, nil
}

// Get 查單一 persona 現況。
func (a *PersonaAdmin) Get(id string) (*PersonaInfo, error) {
	if err := validAgentID(id); err != nil {
		return nil, err
	}
	return a.readInfo(id)
}

// GetSkillMD 回傳某 persona SKILL.md 的完整原始文字（供編輯 UI 載入）。
func (a *PersonaAdmin) GetSkillMD(id string) (string, error) {
	if err := validAgentID(id); err != nil {
		return "", err
	}
	body, err := os.ReadFile(a.skillPath(id))
	if err != nil {
		return "", fmt.Errorf("read SKILL.md for %s: %w", id, err)
	}
	return string(body), nil
}

// SetSkillMD 整份覆寫某 persona 的 SKILL.md 並 force reload。這是比 SetModel 更
// 寬的寫入面（SetModel 只改 ## Model 段、其餘保留；這裡整檔覆寫），供 rag_admin
// 的 SKILL.md 編輯器使用——內網管理工具、agent_id 已由 validAgentID 限定在
// skillsDir 底下已知的目錄名，不接受任意路徑。
func (a *PersonaAdmin) SetSkillMD(id, content string) (*PersonaInfo, error) {
	if err := validAgentID(id); err != nil {
		return nil, err
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("content is required")
	}
	path := a.skillPath(id)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("persona %s not found: %w", id, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write SKILL.md for %s: %w", id, err)
	}
	// force reload → 觸發 RoleCompiler 重生 ROLE.md + 刷新 ModelResolution
	// （使用者可能在編輯內容裡順手改了 ## Model 段，跟 SetModel 走同一收尾）
	if a.loader != nil {
		if _, _, _, err := a.loader.Load(id, true); err != nil {
			return nil, fmt.Errorf("reload skill %s after edit: %w", id, err)
		}
	}
	return a.readInfo(id)
}

// SetModel 把 model/role 寫進 persona 的 SKILL.md ## Model 段並 force reload。
func (a *PersonaAdmin) SetModel(id, model, role string) (*PersonaInfo, error) {
	if err := validAgentID(id); err != nil {
		return nil, err
	}
	if strings.TrimSpace(model) == "" && strings.TrimSpace(role) == "" {
		return nil, fmt.Errorf("model or role is required")
	}
	path := a.skillPath(id)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md for %s: %w", id, err)
	}
	updated := persona.UpsertModelSection(string(body), model, role)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return nil, fmt.Errorf("write SKILL.md for %s: %w", id, err)
	}
	// force reload → 觸發 RoleCompiler 重生 ROLE.md + 刷新 ModelResolution
	if a.loader != nil {
		if _, _, _, err := a.loader.Load(id, true); err != nil {
			return nil, fmt.Errorf("reload skill %s after model change: %w", id, err)
		}
	}
	return a.readInfo(id)
}
