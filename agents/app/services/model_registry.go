// model_registry.go — 集中管理的「模型清單（models list）」與用途選擇器。
//
// 背景：本 agent 連到內網 Ollama（預設 192.168.50.104:11434），上面同時放了
// 多顆不同尺寸 / 能力的模型（chat / vision / embedding / 推理 / 工具呼叫 …）。
// 過去模型名稱散落在 config/llm.yaml（chat）、ingest.vision.model（視覺）、
// embed.DefaultModel（嵌入）三處，彼此不知道對方存在，也無法依「需求」挑模型。
//
// 本檔提供單一事實來源（single source of truth）：
//
//	catalog        — 所有已知模型 + 能力 / 尺寸 / 是否已載入 / 上下文長度
//	rolePreferences — 每個「用途（ModelRole）」的優先模型清單（依序 fallback）
//	Registry       — 查表 / 依用途解析 / 依能力過濾
//
// 用法：
//
//	reg := services.DefaultModelRegistry()
//	chat := reg.ModelFor(services.RoleChat)             // "qwen3.5:9b"
//	spec, _ := reg.Resolve(services.RoleVision)         // ModelSpec{...}
//	cfg := reg.OllamaConfigFor(services.RoleChatQuality) // 直接餵 NewOllamaClient
//
// 設計原則：
//   - Resolve 優先回「已載入」的偏好模型；都沒載入則回第一個偏好（呼叫端可決定 pull）
//   - 清單為程式內預設值；未來要外部化可由 config 覆寫（LoadModelCatalog，預留）
package services

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultOllamaHost 內網 Ollama 服務位址（與 config/llm.yaml 的預設一致）。
const DefaultOllamaHost = "http://192.168.50.104:11434"

// ModelCapability 模型能力位元旗標；一顆模型可同時具備多種能力。
type ModelCapability uint16

const (
	CapChat        ModelCapability = 1 << iota // 一般對話 / 生成
	CapTools                                    // 原生 tool calling（function calling）
	CapVision                                   // 影像輸入（OCR / 圖片理解）
	CapEmbedding                                // 文本向量嵌入
	CapThinking                                 // 顯式推理 / reasoning（chain-of-thought）
	CapLongContext                              // 長上下文（>= 64k tokens）
	CapUncensored                              // 未對齊 / 無內容過濾
)

// capNames 供 String() / 偵錯輸出。
var capNames = []struct {
	bit  ModelCapability
	name string
}{
	{CapChat, "chat"},
	{CapTools, "tools"},
	{CapVision, "vision"},
	{CapEmbedding, "embedding"},
	{CapThinking, "thinking"},
	{CapLongContext, "long-context"},
	{CapUncensored, "uncensored"},
}

// Has 是否具備指定能力（可用 OR 組合查多項：c.Has(CapChat|CapTools)）。
func (c ModelCapability) Has(want ModelCapability) bool { return c&want == want }

func (c ModelCapability) String() string {
	var parts []string
	for _, n := range capNames {
		if c.Has(n.bit) {
			parts = append(parts, n.name)
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "|")
}

// ModelSpec 單一模型的中繼資料。
type ModelSpec struct {
	Name         string          // Ollama tag，例 "qwen3.5:9b"
	Host         string          // Ollama base URL；空 → DefaultOllamaHost
	Capabilities ModelCapability // 能力旗標
	SizeGB       float64         // 磁碟大小（GB）；僅供排程 / 觀測，非精確
	ContextK     int             // 上下文長度（K tokens），例 8 / 32 / 64 / 128
	Loaded       bool            // 是否已在 Ollama 載入（false = 需先 pull / 載入）
	Notes        string          // 備註（用途建議 / 限制）
}

// ResolvedHost 回傳實際 host（補上預設）。
func (m ModelSpec) ResolvedHost() string {
	if m.Host == "" {
		return DefaultOllamaHost
	}
	return m.Host
}

// ModelRole 「用途」識別字串；呼叫端依任務語意選 role，而非直接寫死模型名。
type ModelRole string

const (
	RoleChat             ModelRole = "chat"              // 一般對話（平衡品質 / 速度）
	RoleChatFast         ModelRole = "chat_fast"         // 低延遲 / 輕量（小模型）
	RoleChatQuality      ModelRole = "chat_quality"      // 高品質 / 複雜推理（大模型）
	RoleChatLong         ModelRole = "chat_long"         // 長文件 / 長上下文
	RoleToolCalling      ModelRole = "tool_calling"      // 需要穩定 function calling
	RoleReasoning        ModelRole = "reasoning"         // 顯式思考 / 規劃
	RoleVision           ModelRole = "vision"            // 圖片 OCR / 理解
	RoleVisionUncensored ModelRole = "vision_uncensored" // 無過濾視覺
	RoleEmbedding        ModelRole = "embedding"         // 向量嵌入
	RoleChunking         ModelRole = "chunking"          // 語意切塊（低溫、快、便宜）
	RoleUncensored       ModelRole = "uncensored"        // 無過濾對話
)

// ── 預設模型清單（依 192.168.50.104:11434 現況 + 未載入兩顆） ──────────────────

func defaultCatalog() []ModelSpec {
	return []ModelSpec{
		// ── Gemma 4 系列（chat 主力） ──
		{Name: "gemma4:12b", Capabilities: CapChat | CapTools, SizeGB: 7.6, ContextK: 32, Loaded: true,
			Notes: "平衡型 chat 主力；中等資源、回應品質佳"},
		{Name: "gemma4:e4b", Capabilities: CapChat, SizeGB: 9.6, ContextK: 32, Loaded: true,
			Notes: "輕量快速；切塊 / 簡單對話 / 高吞吐"},
		{Name: "gemma4-64k:latest", Capabilities: CapChat | CapLongContext, SizeGB: 9.6, ContextK: 64, Loaded: true,
			Notes: "長上下文版；長文件摘要 / RAG 大 context"},

		// ── Qwen3.5 系列（tool calling / 高品質） ──
		{Name: "qwen3.5:9b", Capabilities: CapChat | CapTools, SizeGB: 6.6, ContextK: 32, Loaded: true,
			Notes: "tool calling 首選；agent 工具迴圈穩定"},
		{Name: "qwen3.5-9b:latest", Capabilities: CapChat | CapTools, SizeGB: 6.6, ContextK: 32, Loaded: true,
			Notes: "qwen3.5:9b 的 latest 別名（同級備援）"},
		{Name: "qwen3.5:35b", Capabilities: CapChat | CapTools, SizeGB: 23, ContextK: 32, Loaded: true,
			Notes: "高品質 / 複雜推理；資源需求高"},
		{Name: "qwen3.5-35b:latest", Capabilities: CapChat | CapTools, SizeGB: 23, ContextK: 32, Loaded: true,
			Notes: "qwen3.5:35b 的 latest 別名（高品質備援）"},
		{Name: "qwen3-14b-un:latest", Capabilities: CapChat | CapUncensored, SizeGB: 9.0, ContextK: 32, Loaded: true,
			Notes: "未對齊對話；不做內容過濾的場景"},

		// ── Vision（圖片理解 / OCR） ──
		{Name: "qwen3-vl:4b", Capabilities: CapVision | CapChat, SizeGB: 3.3, ContextK: 32, Loaded: true,
			Notes: "視覺首選；OCR / 圖片描述（ingest upload 預設）"},
		{Name: "qwen3-vl-4b:latest", Capabilities: CapVision | CapChat, SizeGB: 3.3, ContextK: 32, Loaded: true,
			Notes: "qwen3-vl:4b 的 latest 別名（視覺備援）"},
		{Name: "minicpm-v:latest", Capabilities: CapVision | CapChat, SizeGB: 5.5, ContextK: 8, Loaded: true,
			Notes: "視覺備援；對部分版面 / 表格較佳"},

		// ── Embedding ──
		{Name: "bge-m3:latest", Capabilities: CapEmbedding, SizeGB: 1.2, ContextK: 8, Loaded: true,
			Notes: "BGE-M3 嵌入（dense 1024d）；RAG 唯一嵌入模型"},

		// ── 尚未載入（需先 pull / load 才可用） ──
		{Name: "Mellum2-12B-A2.5B-Thinking.Q4_K_M", Capabilities: CapChat | CapThinking, SizeGB: 8.0, ContextK: 32, Loaded: false,
			Notes: "MoE thinking 模型；規劃 / 多步推理（用前需載入）"},
		{Name: "mmproj-Qwen3.5-9B-Uncensored-HauhauCS-Aggressive-BF16", Capabilities: CapVision | CapChat | CapUncensored, SizeGB: 9.0, ContextK: 32, Loaded: false,
			Notes: "未對齊視覺模型；無過濾 OCR / 圖片理解（用前需載入）"},
	}
}

// rolePreferences 每個用途的偏好模型，依序 fallback（前面優先）。
// Resolve 會跳過「未載入」的，挑第一顆已載入者；全未載入則回第一順位。
func rolePreferences() map[ModelRole][]string {
	return map[ModelRole][]string{
		RoleChat:        {"gemma4:12b", "qwen3.5:9b", "gemma4:e4b"},
		RoleChatFast:    {"gemma4:e4b", "qwen3-vl:4b", "qwen3.5:9b"},
		RoleChatQuality: {"qwen3.5:35b", "qwen3.5-35b:latest", "gemma4:12b"},
		RoleChatLong:    {"gemma4-64k:latest", "qwen3.5:35b", "gemma4:12b"},
		RoleToolCalling: {"qwen3.5:9b", "qwen3.5-9b:latest", "qwen3.5:35b", "gemma4:12b"},
		RoleReasoning:   {"Mellum2-12B-A2.5B-Thinking.Q4_K_M", "qwen3.5:35b", "qwen3.5:9b"},
		RoleVision:      {"qwen3-vl:4b", "qwen3-vl-4b:latest", "minicpm-v:latest"},
		RoleVisionUncensored: {
			"mmproj-Qwen3.5-9B-Uncensored-HauhauCS-Aggressive-BF16", "qwen3-vl:4b", "minicpm-v:latest",
		},
		RoleEmbedding:  {"bge-m3:latest"},
		RoleChunking:   {"gemma4:e4b", "qwen3.5:9b", "gemma4:12b"},
		RoleUncensored: {"qwen3-14b-un:latest", "qwen3.5:9b"},
	}
}

// ── Registry ──────────────────────────────────────────────────────────────

// ModelRegistry 模型清單 + 用途解析器。建立後唯讀，併發安全。
type ModelRegistry struct {
	byName map[string]ModelSpec
	order  []string // 維持 catalog 宣告順序，供 List() 穩定輸出
	prefs  map[ModelRole][]string
}

// DefaultModelRegistry 以程式內預設清單建立 registry。
func DefaultModelRegistry() *ModelRegistry {
	return NewModelRegistry(defaultCatalog(), rolePreferences())
}

// NewModelRegistry 由自訂清單 + 用途偏好建立 registry（測試 / config 覆寫用）。
func NewModelRegistry(catalog []ModelSpec, prefs map[ModelRole][]string) *ModelRegistry {
	r := &ModelRegistry{
		byName: make(map[string]ModelSpec, len(catalog)),
		order:  make([]string, 0, len(catalog)),
		prefs:  prefs,
	}
	for _, m := range catalog {
		if m.Host == "" {
			m.Host = DefaultOllamaHost
		}
		if _, dup := r.byName[m.Name]; !dup {
			r.order = append(r.order, m.Name)
		}
		r.byName[m.Name] = m
	}
	return r
}

// Get 依模型名取 spec。
func (r *ModelRegistry) Get(name string) (ModelSpec, bool) {
	m, ok := r.byName[name]
	return m, ok
}

// Resolve 依用途回最適模型：優先「已載入」的偏好，皆未載入則回第一順位。
// 用途未定義或偏好清單為空 → error。
func (r *ModelRegistry) Resolve(role ModelRole) (ModelSpec, error) {
	prefs := r.prefs[role]
	if len(prefs) == 0 {
		return ModelSpec{}, fmt.Errorf("model registry: no preference for role %q", role)
	}
	var first *ModelSpec
	for _, name := range prefs {
		m, ok := r.byName[name]
		if !ok {
			continue
		}
		if first == nil {
			mm := m
			first = &mm
		}
		if m.Loaded {
			return m, nil
		}
	}
	if first != nil {
		return *first, nil // 全未載入；回第一順位（呼叫端可決定是否 pull）
	}
	return ModelSpec{}, fmt.Errorf("model registry: no known model for role %q (prefs=%v)", role, prefs)
}

// ModelFor 同 Resolve 但只回模型名；無解時回空字串（方便當作 fallback 預設）。
func (r *ModelRegistry) ModelFor(role ModelRole) string {
	m, err := r.Resolve(role)
	if err != nil {
		return ""
	}
	return m.Name
}

// MustModelFor 同 ModelFor，但無解時回傳 fallback（呼叫端硬保底用）。
func (r *ModelRegistry) MustModelFor(role ModelRole, fallback string) string {
	if name := r.ModelFor(role); name != "" {
		return name
	}
	return fallback
}

// List 回所有模型（依 catalog 宣告順序）。
func (r *ModelRegistry) List() []ModelSpec {
	out := make([]ModelSpec, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.byName[name])
	}
	return out
}

// ListByCapability 回具備指定能力（且已載入）的模型，依尺寸由小到大排序。
// 傳 false 給 loadedOnly 可含未載入者。
func (r *ModelRegistry) ListByCapability(cap ModelCapability, loadedOnly bool) []ModelSpec {
	var out []ModelSpec
	for _, name := range r.order {
		m := r.byName[name]
		if !m.Capabilities.Has(cap) {
			continue
		}
		if loadedOnly && !m.Loaded {
			continue
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SizeGB < out[j].SizeGB })
	return out
}

// Roles 回所有已定義用途（排序後），供 CLI / 文件列出。
func (r *ModelRegistry) Roles() []ModelRole {
	out := make([]ModelRole, 0, len(r.prefs))
	for role := range r.prefs {
		out = append(out, role)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ── 與既有 client 介接的便利建構子 ─────────────────────────────────────────

// OllamaConfigFor 依用途組出 OllamaConfig，可直接餵 NewOllamaClient。
// timeoutSec / maxTokens <=0 → 交給 NewOllamaClient 套用其預設。
func (r *ModelRegistry) OllamaConfigFor(role ModelRole, timeoutSec, maxTokens int) (OllamaConfig, error) {
	m, err := r.Resolve(role)
	if err != nil {
		return OllamaConfig{}, err
	}
	return OllamaConfig{
		URL:       m.ResolvedHost(),
		Model:     m.Name,
		Timeout:   timeoutSec,
		MaxTokens: maxTokens,
	}, nil
}

// OllamaClientFor 依用途直接建立一個 OllamaClient（chat / tool calling 用途）。
func (r *ModelRegistry) OllamaClientFor(role ModelRole, timeoutSec, maxTokens int) (*OllamaClient, error) {
	cfg, err := r.OllamaConfigFor(role, timeoutSec, maxTokens)
	if err != nil {
		return nil, err
	}
	return NewOllamaClient(cfg), nil
}

// ── Config 套用：把 registry 灌進既有的 OllamaConfig / VisionConfig ──────

// ApplyToOllamaConfig 依 cfg.Role 補齊 cfg.URL / cfg.Model。
// 規則：cfg.Model 非空 → 不動；cfg.Model 空且 cfg.Role 非空 → 由 registry 解析。
// cfg.URL 空也會用 registry 給的 host 補上。defaultRole 在 cfg.Role 為空時生效（傳 "" 表示不補）。
func (r *ModelRegistry) ApplyToOllamaConfig(cfg *OllamaConfig, defaultRole ModelRole) error {
	if cfg == nil {
		return fmt.Errorf("model registry: nil OllamaConfig")
	}
	if cfg.Model != "" {
		if cfg.URL == "" {
			cfg.URL = DefaultOllamaHost
		}
		return nil
	}
	role := ModelRole(cfg.Role)
	if role == "" {
		role = defaultRole
	}
	if role == "" {
		return nil // 沒給 role 也沒給 default → 留給 NewOllamaClient 套硬編碼 fallback
	}
	m, err := r.Resolve(role)
	if err != nil {
		return err
	}
	cfg.Model = m.Name
	if cfg.URL == "" {
		cfg.URL = m.ResolvedHost()
	}
	return nil
}

// VisionExtractorFor 依視覺用途建立 VisionExtractor（預設 RoleVision）。
// role 傳空 → RoleVision；prompt 空 → 用內建 OCR 提示。
func (r *ModelRegistry) VisionExtractorFor(role ModelRole, prompt string, timeoutSec int) (*VisionExtractor, error) {
	if role == "" {
		role = RoleVision
	}
	m, err := r.Resolve(role)
	if err != nil {
		return nil, err
	}
	if !m.Capabilities.Has(CapVision) {
		return nil, fmt.Errorf("model registry: role %q resolved to non-vision model %q", role, m.Name)
	}
	return NewVisionExtractor(m.ResolvedHost(), m.Name, prompt, timeoutSec), nil
}
