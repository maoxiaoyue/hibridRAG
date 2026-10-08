// Package persona 解析 SKILL.md，抽出 PersonalMemory 與 ProceduralMemory 所需的結構化資料。
//
// SKILL.md 預期格式（各 section 皆可選）：
//
//	# Agent Name
//
//	## Identity
//	- name: My Agent
//	- role: Customer Support
//	- domain: E-commerce
//
//	## Expression DNA
//	- tone: warm yet professional
//	- style: concise, structured
//
//	## Mental Models
//
//	### First Principles Thinking
//	Content describing the mental model...
//
//	**Heuristics**
//	- challenge_assumptions: Ask "why" at least 3 times
//	- simplify: Break problems into smallest atomic units
//
//	### Customer Empathy
//	Another model...
package persona

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"agent/app/models"
)

// ParseResult SKILL.md 解析結果，對應兩張 PG 表的輸入資料
type ParseResult struct {
	AgentName     string
	Identity      map[string]interface{} // → PersonalMemory.Identity
	ExpressionDNA map[string]interface{} // → PersonalMemory.ExpressionDNA
	MentalModels  []MentalModel          // → 多筆 ProceduralMemory
	ModelPref     *ModelPreference       // → 推理用模型偏好；nil 表示沒有 ## Model 段
}

// ModelPreference 宣告這個 persona 該用哪顆 LLM 推理。
// Role 是慣用寫法（對應 app/services.ModelRole 的字串值，例如 "chat_quality"）；
// Model 是可選的明確 model tag，設定時覆寫 Role 的解析結果。
// 這裡刻意用純 string（不是 services.ModelRole），讓 persona 套件不需要依賴
// app/services——實際的 role→model 解析發生在 services 層的接縫（RoleCompiler）。
//
// 屬於操作性資料，刻意不會被 ToInstruction() 渲染進 system prompt。
type ModelPreference struct {
	Role  string
	Model string
}

// MentalModel 對應 ProceduralMemory 的一筆記錄
type MentalModel struct {
	Name       string                 // → ProceduralMemory.ModelName
	Content    string                 // → ProceduralMemory.Content
	Heuristics map[string]interface{} // → ProceduralMemory.Heuristics
}

// IsEmpty 判斷解析結果是否完全空白
func (r *ParseResult) IsEmpty() bool {
	return r.AgentName == "" &&
		len(r.Identity) == 0 &&
		len(r.ExpressionDNA) == 0 &&
		len(r.MentalModels) == 0
}

// ContextQuotas 從 SKILL.md Identity 段抽出 context 配額覆蓋值。
// 任一欄位 < 0 表示未設定，呼叫端應使用 config 預設值。
//
// 支援的 SKILL.md 寫法（皆為 optional，放在 ## Identity 段內）：
//
//	## Identity
//	- name: SupportBot
//	- role: Customer Support
//	- context_personal: 1
//	- context_procedural: 5     ← 客服 agent 想要更多心智模型
//	- context_rag: 3
//	- context_episodic: 5       ← 客服更需要過去對話
//	- context_semantic: 2       ← LongTermMemory ANN top-K
type ContextQuotas struct {
	Personal   int // PersonalMemory 數量（通常 0 or 1）
	Procedural int // ProceduralMemory top-K
	RAG        int // RAG chunks top-K
	Episodic   int // EpisodicMemory top-K
	Semantic   int // LongTermMemory ANN top-K
}

// HasOverride 判斷是否有任何欄位設定值（>= 0 視為已設）
func (q ContextQuotas) HasOverride() bool {
	return q.Personal >= 0 || q.Procedural >= 0 || q.RAG >= 0 || q.Episodic >= 0 || q.Semantic >= 0
}

// ContextQuotas 從 Identity map 抽出 context_* 數字，未設定回 -1
func (r *ParseResult) ContextQuotas() ContextQuotas {
	q := ContextQuotas{Personal: -1, Procedural: -1, RAG: -1, Episodic: -1, Semantic: -1}
	if r == nil {
		return q
	}
	q.Personal = lookupInt(r.Identity, "context_personal")
	q.Procedural = lookupInt(r.Identity, "context_procedural")
	q.RAG = lookupInt(r.Identity, "context_rag", "context_rag_chunks")
	q.Episodic = lookupInt(r.Identity, "context_episodic")
	q.Semantic = lookupInt(r.Identity, "context_semantic")
	return q
}

// lookupInt 在 map 中依序查找 keys，找到第一個可解析為 int 的回傳；否則 -1
func lookupInt(m map[string]interface{}, keys ...string) int {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case int:
			return x
		case int64:
			return int(x)
		case float64:
			return int(x)
		case string:
			s := strings.TrimSpace(x)
			if s == "" {
				continue
			}
			if n, err := parseNonNegativeInt(s); err == nil {
				return n
			}
		}
	}
	return -1
}

func parseNonNegativeInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not an integer: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// ToPersonalMemoryRequest 將解析結果轉成 MemoryService.CreatePersonal 的請求
// 若 Identity 為空則回傳 nil，表示無需寫入
func (r *ParseResult) ToPersonalMemoryRequest(agentID string) *models.CreatePersonalMemoryRequest {
	if len(r.Identity) == 0 && len(r.ExpressionDNA) == 0 {
		return nil
	}
	identity := r.Identity
	if identity == nil {
		identity = map[string]interface{}{}
	}
	if r.AgentName != "" {
		if _, set := identity["name"]; !set {
			identity["name"] = r.AgentName
		}
	}
	req := &models.CreatePersonalMemoryRequest{
		AgentID:  agentID,
		Identity: identity,
	}
	if len(r.ExpressionDNA) > 0 {
		req.ExpressionDNA = r.ExpressionDNA
	}
	return req
}

// ToInstruction 將解析結果渲染成 LLM system prompt（natural-language Markdown）。
//
// 輸出可直接餵給 google.golang.org/adk/agent/llmagent.Config.Instruction，
// 或任何接受 system prompt 字串的 LLM client。
//
// 結構：
//
//	You are {name}, {role}. Your domain is {domain}.
//
//	## About You
//	- key: value
//
//	## Communication Style
//	- tone: ...
//
//	## Mental Models
//
//	### Model Name
//	{content}
//
//	Heuristics:
//	- key: value
//
// 若 ParseResult 為空 → 回傳空字串（呼叫端可自行決定 fallback）。
// Map iteration 一律以 key 字典序排序，確保輸出確定性（利於測試與快取）。
func (r *ParseResult) ToInstruction() string {
	if r.IsEmpty() {
		return ""
	}

	var sb strings.Builder

	// ---- Opening：「You are {name}, {role}. Your domain is {domain}.」 ----
	name := r.lookupIdentity("name", "名稱", "名字")
	if name == "" {
		name = r.AgentName
	}
	role := r.lookupIdentity("role", "角色")
	domain := r.lookupIdentity("domain", "領域", "領域範圍")

	switch {
	case name != "" && role != "":
		fmt.Fprintf(&sb, "You are %s, %s.", name, role)
	case name != "":
		fmt.Fprintf(&sb, "You are %s.", name)
	case role != "":
		fmt.Fprintf(&sb, "You are a %s.", role)
	}
	if domain != "" && (name != "" || role != "") {
		fmt.Fprintf(&sb, " Your domain is %s.", domain)
	}
	if sb.Len() > 0 {
		sb.WriteString("\n\n")
	}

	// ---- About You：Identity 扣掉已用於 opening 的 keys ----
	const usedIdentityKeys = "name|role|domain|名稱|名字|角色|領域|領域範圍"
	if extras := filterKeys(r.Identity, strings.Split(usedIdentityKeys, "|")); len(extras) > 0 {
		writeKVSection(&sb, "## About You", r.Identity, extras)
	}

	// ---- Communication Style：完整 ExpressionDNA ----
	if len(r.ExpressionDNA) > 0 {
		writeKVSection(&sb, "## Communication Style", r.ExpressionDNA, sortedKeys(r.ExpressionDNA))
	}

	// ---- Mental Models：依 SKILL.md 順序保留（不重排）----
	if len(r.MentalModels) > 0 {
		sb.WriteString("## Mental Models\n\n")
		for _, m := range r.MentalModels {
			fmt.Fprintf(&sb, "### %s\n", m.Name)
			if m.Content != "" {
				sb.WriteString(m.Content)
				sb.WriteString("\n")
			}
			if len(m.Heuristics) > 0 {
				sb.WriteString("\nHeuristics:\n")
				for _, k := range sortedKeys(m.Heuristics) {
					fmt.Fprintf(&sb, "- %s: %s\n", k, formatValue(m.Heuristics[k]))
				}
			}
			sb.WriteString("\n")
		}
	}

	return strings.TrimRight(sb.String(), "\n") + "\n"
}

// lookupIdentity 從 Identity map 依序查找 alias，回傳第一個非空字串值
func (r *ParseResult) lookupIdentity(aliases ...string) string {
	for _, k := range aliases {
		if v, ok := r.Identity[k]; ok {
			if s, isStr := v.(string); isStr && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// writeKVSection 將 map 內容寫成「## Heading\n- key: value\n...」區塊
// keys 必須由呼叫端決定順序（確定性輸出 + 過濾用）
func writeKVSection(sb *strings.Builder, heading string, m map[string]interface{}, keys []string) {
	sb.WriteString(heading)
	sb.WriteString("\n")
	for _, k := range keys {
		fmt.Fprintf(sb, "- %s: %s\n", k, formatValue(m[k]))
	}
	sb.WriteString("\n")
}

// formatValue 將 interface{} value 渲染成顯示字串
//   - bool true（key 無值的標記）→ "yes"
//   - string → 原樣
//   - 其他 → fmt.Sprint
func formatValue(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		if x {
			return "yes"
		}
		return "no"
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// sortedKeys 回傳 map 的 key 字典序排序結果（確定性 iteration）
func sortedKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// filterKeys 回傳 m 中扣掉 exclude 後剩餘 key 的字典序排序
func filterKeys(m map[string]interface{}, exclude []string) []string {
	skip := make(map[string]struct{}, len(exclude))
	for _, k := range exclude {
		skip[k] = struct{}{}
	}
	out := make([]string, 0, len(m))
	for k := range m {
		if _, drop := skip[k]; drop {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ToProceduralMemoryRequests 將每個 MentalModel 轉成 ProceduralMemory 請求
func (r *ParseResult) ToProceduralMemoryRequests(agentID string) []models.CreateProceduralMemoryRequest {
	out := make([]models.CreateProceduralMemoryRequest, 0, len(r.MentalModels))
	for _, m := range r.MentalModels {
		req := models.CreateProceduralMemoryRequest{
			AgentID:   agentID,
			ModelName: m.Name,
			Content:   m.Content,
		}
		if len(m.Heuristics) > 0 {
			req.Heuristics = m.Heuristics
		}
		out = append(out, req)
	}
	return out
}

// ---- 正規表達式 ----

var (
	reH1         = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)
	reH2         = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)
	reH3         = regexp.MustCompile(`(?m)^###\s+(.+?)\s*$`)
	reHeuristics = regexp.MustCompile(`(?mi)^\*\*heuristics\*\*\s*$|^\*\*決策啟發式\*\*\s*$|^\*\*啟發式\*\*\s*$`)
	reBulletKV   = regexp.MustCompile(`^[-*]\s+([^:\n]+?)\s*:\s*(.*)$`)
	rePlainKV    = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_\s]{0,40}?)\s*:\s*(.+)$`)
)

// Parse 解析 SKILL.md 全文，回傳結構化 ParseResult
// 容錯設計：任何 section 缺失時對應欄位為零值，不回傳 error
func Parse(body string) *ParseResult {
	result := &ParseResult{
		Identity:      make(map[string]interface{}),
		ExpressionDNA: make(map[string]interface{}),
	}

	// 取 H1 作為 agent 名稱
	if m := reH1.FindStringSubmatch(body); len(m) >= 2 {
		result.AgentName = strings.TrimSpace(m[1])
	}

	// 將 body 切成 H2 section
	sections := splitByH2(body)
	for title, content := range sections {
		titleLower := strings.ToLower(strings.TrimSpace(title))
		switch {
		case titleLower == "identity" || titleLower == "身份卡" || titleLower == "身份":
			result.Identity = parseKVSection(content)

		case titleLower == "expression dna" || titleLower == "expressiondna" ||
			titleLower == "表達dna" || titleLower == "表達 dna" || titleLower == "dna":
			result.ExpressionDNA = parseKVSection(content)

		case strings.Contains(titleLower, "mental model") ||
			strings.Contains(titleLower, "心智模型") ||
			titleLower == "models" || titleLower == "模型":
			result.MentalModels = parseMentalModels(content)

		case titleLower == "model" || titleLower == "model settings" ||
			titleLower == "模型設定":
			result.ModelPref = parseModelSection(content)
		}
	}

	return result
}

// splitByH2 將 markdown body 依 ## 切成 map[sectionTitle]sectionBody
// 只取第一個出現的同名 section；H2 行本身不包含在 body 中
func splitByH2(body string) map[string]string {
	locs := reH2.FindAllStringIndex(body, -1)
	matches := reH2.FindAllStringSubmatch(body, -1)
	if len(locs) == 0 {
		return nil
	}

	out := make(map[string]string, len(locs))
	for i, loc := range locs {
		title := matches[i][1]
		start := loc[1] // 緊接在 ## 行後的位置
		var end int
		if i+1 < len(locs) {
			end = locs[i+1][0]
		} else {
			end = len(body)
		}
		// 避免重複 key 蓋掉前面的 section
		if _, exists := out[title]; !exists {
			out[title] = strings.TrimSpace(body[start:end])
		}
	}
	return out
}

// parseKVSection 從 section body 解析 key: value 對
// 支援兩種格式：
//  1. Bullet：`- key: value` 或 `* key: value`
//  2. Plain：`key: value`（key 必須是英數字開頭）
//
// value 若為空字串仍保留 key（值設為 true）
func parseKVSection(body string) map[string]interface{} {
	result := make(map[string]interface{})
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := reBulletKV.FindStringSubmatch(line); len(m) == 3 {
			k := normalizeKey(m[1])
			v := strings.TrimSpace(m[2])
			if v == "" {
				result[k] = true
			} else {
				result[k] = v
			}
			continue
		}
		if m := rePlainKV.FindStringSubmatch(line); len(m) == 3 {
			k := normalizeKey(m[1])
			v := strings.TrimSpace(m[2])
			if v != "" {
				result[k] = v
			}
		}
	}
	return result
}

// parseMentalModels 將 ## Mental Models section body 切成多個 MentalModel
// 每個 ### 開頭代表一個 model；`**Heuristics**` 之後的 bullet 列表解析為 heuristics
func parseMentalModels(body string) []MentalModel {
	locs := reH3.FindAllStringIndex(body, -1)
	matches := reH3.FindAllStringSubmatch(body, -1)
	if len(locs) == 0 {
		return nil
	}

	out := make([]MentalModel, 0, len(locs))
	for i, loc := range locs {
		name := strings.TrimSpace(matches[i][1])
		start := loc[1]
		var end int
		if i+1 < len(locs) {
			end = locs[i+1][0]
		} else {
			end = len(body)
		}
		modelBody := strings.TrimSpace(body[start:end])
		content, heuristics := parseMentalModelBody(modelBody)
		out = append(out, MentalModel{
			Name:       name,
			Content:    content,
			Heuristics: heuristics,
		})
	}
	return out
}

// parseMentalModelBody 將單一 ### 區塊拆分為 content 與 heuristics
// **Heuristics** 標記之前的文字為 content；之後的 bullet KV 為 heuristics
func parseMentalModelBody(body string) (content string, heuristics map[string]interface{}) {
	loc := reHeuristics.FindStringIndex(body)
	if loc == nil {
		return strings.TrimSpace(body), nil
	}
	content = strings.TrimSpace(body[:loc[0]])
	heuristicBody := strings.TrimSpace(body[loc[1]:])
	heuristics = parseKVSection(heuristicBody)
	return content, heuristics
}

// parseModelSection 解析 ## Model section，讀取 role/model 兩個 key
// （沿用跟 Identity/ExpressionDNA 相同的 parseKVSection）。
// 兩者皆空時回傳 nil，等同沒有這個 section。
func parseModelSection(body string) *ModelPreference {
	kv := parseKVSection(body)
	role, _ := kv["role"].(string)
	model, _ := kv["model"].(string)
	role = strings.TrimSpace(role)
	model = strings.TrimSpace(model)
	if role == "" && model == "" {
		return nil
	}
	return &ModelPreference{Role: role, Model: model}
}

// normalizeKey 將 key 轉為 snake_case 並去除多餘空白
func normalizeKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	// 空白 → 底線
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, "_")
	return s
}
