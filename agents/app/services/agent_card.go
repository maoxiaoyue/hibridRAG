// agent_card.go — 從已解析的 SKILL.md（persona.ParseResult）+ 靜態 config
// 動態組出 A2A Agent Card（TODO4.md P1-2），取代先前完全寫死的 JSON。
package services

import (
	"fmt"
	"regexp"
	"strings"

	"agent/app/models"
	"agent/internal/agent/persona"
)

// AgentCardBuilder 從 persona.ParseResult 推導 models.AgentCard。無狀態，
// 可併發使用；建構一次、每個 request 呼叫 Build()。
type AgentCardBuilder struct {
	DefaultAgentID string // parsed 沒有可用名稱時的 fallback（通常是 cfg.Agent.ID）
	Version        string
	PublicURL      string
}

// NewAgentCardBuilder 建立 builder。
func NewAgentCardBuilder(defaultAgentID, version, publicURL string) *AgentCardBuilder {
	return &AgentCardBuilder{DefaultAgentID: defaultAgentID, Version: version, PublicURL: publicURL}
}

// defaultDescription 是今天寫死的字串，parsed 無法推出更好的描述時當 fallback。
const defaultDescription = "Single agent — gRPC Inbound + A2A HTTP/2"

// defaultSkills 是今天寫死的單一 skill，parsed.MentalModels 為空時的 fallback
// （demo skill、或這個 agentID 根本沒有載入任何 SkillPack 時），確保行為不變。
func defaultSkills() []models.A2ASkill {
	return []models.A2ASkill{
		{ID: "chat", Name: "Chat", Description: "General conversation via LLM"},
	}
}

// Build 從 parsed（可能為 nil，表示沒有 SkillPack 可用）推導 AgentCard。
func (b *AgentCardBuilder) Build(agentID string, parsed *persona.ParseResult) models.AgentCard {
	card := models.AgentCard{
		Name:        b.name(agentID, parsed),
		Description: b.description(parsed),
		Version:     b.Version,
		URL:         b.PublicURL,
		Capabilities: models.A2ACapabilities{
			Streaming:         false,
			PushNotifications: false,
		},
		Skills: b.skills(parsed),
	}
	return card
}

func (b *AgentCardBuilder) name(agentID string, parsed *persona.ParseResult) string {
	if parsed != nil {
		if parsed.AgentName != "" {
			return parsed.AgentName
		}
		if v, ok := parsed.Identity["name"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if b.DefaultAgentID != "" {
		return b.DefaultAgentID
	}
	return agentID
}

func (b *AgentCardBuilder) description(parsed *persona.ParseResult) string {
	if parsed != nil {
		role, _ := parsed.Identity["role"].(string)
		domain, _ := parsed.Identity["domain"].(string)
		role, domain = strings.TrimSpace(role), strings.TrimSpace(domain)
		switch {
		case role != "" && domain != "":
			return role + " — " + domain
		case role != "":
			return role
		}
	}
	return defaultDescription
}

func (b *AgentCardBuilder) skills(parsed *persona.ParseResult) []models.A2ASkill {
	if parsed == nil || len(parsed.MentalModels) == 0 {
		return defaultSkills()
	}
	out := make([]models.A2ASkill, 0, len(parsed.MentalModels))
	for i, m := range parsed.MentalModels {
		id := slugify(m.Name)
		if id == "" {
			id = fmt.Sprintf("skill-%d", i+1) // 名稱全為符號等罕見情況的保底，避免空/重複 ID
		}
		out = append(out, models.A2ASkill{
			ID:          id,
			Name:        m.Name,
			Description: truncateRunes(m.Content, 140),
		})
	}
	return out
}

// reSlugNonWord 保留 unicode 字母/數字（含中日韓字元），其餘轉成 "-"；
// 這裡刻意不限縮到 a-z0-9——SKILL.md 的 Mental Model 名稱常是中文（例如
// demo skill 的「先驗證後實作」），限縮到 ASCII 會讓每個 ID 都變成空字串。
var reSlugNonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// slugify 把一個標題轉成適合當 A2ASkill.ID 的識別字串（lowercase、非字母數字→"-"、去除頭尾/重複"-"）。
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = reSlugNonWord.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// truncateRunes 截斷到 n 個 rune（非 byte，CJK 安全），超過則加上省略號；
// 用於 skill 描述避免整段 mental model content 塞進 Agent Card。
func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
