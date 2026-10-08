// edit.go — SKILL.md 的程式化編輯（目前只有 ## Model 段的 upsert）。
//
// 供 agent_client 經 agents 端點設定 persona 推理模型時，把選定的 model/role 寫回
// SKILL.md 的 ## Model 段（SKILL.md 是模型偏好的唯一事實來源，ROLE.md 為衍生檔）。
package persona

import (
	"regexp"
	"strings"
)

// reH2Line 逐行比對 H2 標題（與 Parse 用的 splitByH2 同義，但這裡要行首位移做替換）。
var reH2Line = regexp.MustCompile(`(?m)^##[ \t]+(.+?)[ \t]*$`)

// isModelHeader 與 Parse 內 ## Model 段的辨識規則一致。
func isModelHeader(title string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	return t == "model" || t == "model settings" || t == "模型設定"
}

// buildModelSection 產生 ## Model 段文字（末尾含換行）。role/model 空則略過該行。
func buildModelSection(model, role string) string {
	var sb strings.Builder
	sb.WriteString("## Model\n")
	if r := strings.TrimSpace(role); r != "" {
		sb.WriteString("- role: " + r + "\n")
	}
	if m := strings.TrimSpace(model); m != "" {
		sb.WriteString("- model: " + m + "\n")
	}
	return sb.String()
}

// UpsertModelSection 在 SKILL.md body 新增或替換 ## Model 段，回傳新 body。
// 既有段落（## Model / ## Model Settings / ## 模型設定）會被整段替換；否則附加在文末。
// 其餘內容一律保留。
func UpsertModelSection(body, model, role string) string {
	section := buildModelSection(model, role)
	locs := reH2Line.FindAllStringSubmatchIndex(body, -1)
	for i, loc := range locs {
		if !isModelHeader(body[loc[2]:loc[3]]) {
			continue
		}
		start := loc[0]
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0] // 到下一個 H2 行開頭為止
		}
		newBody := body[:start] + section
		if rest := body[end:]; strings.TrimSpace(rest) != "" {
			newBody = strings.TrimRight(newBody, "\n") + "\n\n" + strings.TrimLeft(rest, "\n")
		}
		return newBody
	}
	// 沒有既有段落 → 附加在文末
	trimmed := strings.TrimRight(body, "\n")
	if trimmed == "" {
		return section
	}
	return trimmed + "\n\n" + section
}
