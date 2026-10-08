// Package context 組裝 RAG 推理所需的 system prompt context。
//
// ContextBuilder 是 Agent 內部模組（非獨立 microservice），對應 RAG_agent 架構 ⑥：
//
//	PersonalMemory（anchor，always on）
//	+ ProceduralMemory top-N
//	+ RAG chunks top-M
//	+ EpisodicMemory top-K
//	──────────────────────────────────
//	→ system prompt（餵給 LLM Gateway）
//
// 配額來源優先級（高到低）：
//  1. SKILL.md Identity 中的 context_personal / context_procedural / context_rag / context_episodic
//  2. ContextBuilder 建構時傳入的 Defaults（從 config.context.*）
//  3. 內建預設（Personal=1, Procedural=3, RAG=5, Episodic=2）
package context

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"agent/app/models"
	"agent/app/services"
	"agent/internal/agent/persona"
	"agent/internal/agent/vectordb"
)

// ── 依賴介面（方便注入測試 mock） ────────────────────────────────────────────

// MemoryReader 提供 ContextBuilder 所需的記憶讀取能力。
// 由 *services.MemoryService 滿足；未來拆出 agent_memory 服務後改 gRPC client。
type MemoryReader interface {
	GetPersonalByAgent(ctx context.Context, agentID string) (*models.PersonalMemory, error)
	ListProceduralByAgent(ctx context.Context, agentID string, limit int) ([]models.ProceduralMemory, error)
	ListEpisodicByAgent(ctx context.Context, agentID string, limit int) ([]models.EpisodicMemory, error)
}

// RAGSearcher 提供向量檢索能力。
// 由 *vectordb.HTTPClient 滿足（HTTP → agent_embedding /api/search）；
// 未來 Vector Search 服務拆出後改 gRPC client，介面不變。
type RAGSearcher interface {
	SearchByQuery(ctx context.Context, agentID, query string, topK int) ([]vectordb.SearchHit, error)
}

// SemanticSearcher 提供 semantic memory（LongTermMemory）ANN 檢索能力。
// 由 *services.SemanticMemory 滿足（embed query → agent_memory gRPC SearchLongTerm）。
// nil 表示未設定（embedding.provider 空白）→ Build 跳過這個來源。
type SemanticSearcher interface {
	Search(ctx context.Context, agentID, query string, topK int) ([]models.LongTermMemoryHit, error)
}

// ── Quotas / Defaults ───────────────────────────────────────────────────────

// Quotas 各類記憶來源的數量配額。
// 任一 < 0 表示未設定（會被 fallback 規則覆蓋）。
type Quotas struct {
	Personal   int
	Procedural int
	RAG        int
	Episodic   int
	Semantic   int
	// MaxChars 粗略字元上限 — 0 表示不限。先於 token 預算套用。
	MaxChars int
	// MaxTokens token 預算上限 — 0 表示不限；以 TokenCounter 估算後截斷。
	MaxTokens int
}

// builtin 內建預設（最後 fallback）
var builtin = Quotas{Personal: 1, Procedural: 3, RAG: 5, Episodic: 2, Semantic: 3, MaxChars: 16000, MaxTokens: 4000}

// Resolve 將 persona override + caller defaults + builtin 三層融合
// 任一層為負值就退到下一層。
func (d Quotas) Resolve(override persona.ContextQuotas) Quotas {
	pickInt := func(a, b, c int) int {
		if a >= 0 {
			return a
		}
		if b >= 0 {
			return b
		}
		return c
	}
	return Quotas{
		Personal:   pickInt(override.Personal, d.Personal, builtin.Personal),
		Procedural: pickInt(override.Procedural, d.Procedural, builtin.Procedural),
		RAG:        pickInt(override.RAG, d.RAG, builtin.RAG),
		Episodic:   pickInt(override.Episodic, d.Episodic, builtin.Episodic),
		Semantic:   pickInt(override.Semantic, d.Semantic, builtin.Semantic),
		MaxChars:   pickInt(-1, d.MaxChars, builtin.MaxChars),
		MaxTokens:  pickInt(-1, d.MaxTokens, builtin.MaxTokens),
	}
}

// ── Builder ────────────────────────────────────────────────────────────────

// Builder 將 Memory + RAG 結果組裝為 LLM system context。
type Builder struct {
	memory   MemoryReader
	rag      RAGSearcher
	semantic SemanticSearcher // nil → 不查 semantic memory
	defaults Quotas
	tokens   TokenCounter // nil → 不做 token 預算（仍會做字元預算）
}

// New 建立 Builder。memory 必填；rag 可為 nil（純記憶模式，不查 RAG）。
// TokenCounter 預設為 HeuristicCounter（CJK-aware）；可用 WithTokenCounter 覆寫。
func New(memory MemoryReader, rag RAGSearcher, defaults Quotas) *Builder {
	if memory == nil {
		panic("context.New: memory must not be nil")
	}
	return &Builder{
		memory:   memory,
		rag:      rag,
		defaults: defaults,
		tokens:   NewHeuristicCounter(),
	}
}

// WithTokenCounter 注入自訂 TokenCounter（如 tiktoken-go）。
// 傳 nil 等於停用 token 預算（只保留字元預算）。
func (b *Builder) WithTokenCounter(tc TokenCounter) *Builder {
	b.tokens = tc
	return b
}

// WithSemanticSearcher 注入 semantic memory（LongTermMemory ANN）檢索器。
// 不傳（或傳 nil）則跳過這個來源，行為等同尚未接上 semantic memory。
func (b *Builder) WithSemanticSearcher(s SemanticSearcher) *Builder {
	b.semantic = s
	return b
}

// BuildResult Build 回傳結果，方便 caller 紀錄/觀測來源計數
type BuildResult struct {
	// SystemPrompt 完整組裝好的 system prompt 文字（不含 user message）
	SystemPrompt string
	// 各來源實際取到的數量（方便觀測）
	Counts SourceCounts
}

// SourceCounts 各來源命中數
type SourceCounts struct {
	Personal   int
	Procedural int
	RAG        int
	Episodic   int
	Semantic   int
	// EstTokens 最終 system prompt 的估算 token 數（套完預算後）
	EstTokens int
}

// Build 是 TODO2 ⑥ 規格的標準入口：
//
//	Build(ctx, agentID, query) ([]Message, error)
//
// 回傳 []services.ChatMessage，第 0 條是組裝好的 system prompt
// （PersonalMemory anchor + ProceduralMemory + RAG + EpisodicMemory），
// 可直接餵給 services.LLMClient.Chat。
//
// 此 entry 不接 SKILL.md persona（適合外部 caller / 測試）；若需要結合
// SKILL.md 渲染的 instruction，請改用 BuildWithPersona。
func (b *Builder) Build(ctx context.Context, agentID, query string) ([]services.ChatMessage, error) {
	r, err := b.BuildWithPersona(ctx, agentID, query, nil, "")
	if err != nil {
		return nil, err
	}
	if r == nil || strings.TrimSpace(r.SystemPrompt) == "" {
		return nil, nil
	}
	return []services.ChatMessage{
		{Role: services.RoleSystem, Content: r.SystemPrompt},
	}, nil
}

// BuildWithPersona 組裝 context（接收已 parse 的 persona + 渲染好的 instruction）。
//
// 流程：
//  1. 解析 persona.ParseResult.ContextQuotas() → 與 defaults 融合
//  2. 並發查詢 4 個來源（goroutine + WaitGroup）
//  3. 依優先級組裝 system prompt：
//     persona instruction
//     + ## Knowledge（RAG）
//     + ## Recent Conversations（Episodic）
//
// instruction 通常已包含 PersonalMemory + ProceduralMemory（由 persona.ToInstruction 渲染），
// 所以這裡只額外注入 RAG + Episodic（避免重複）。
//
// 但如果 instruction 為空（無 SKILL.md），會 fallback 從資料庫補上 PersonalMemory。
func (b *Builder) BuildWithPersona(
	ctx context.Context,
	agentID, query string,
	parsed *persona.ParseResult,
	instruction string,
) (*BuildResult, error) {
	if agentID == "" {
		return nil, errors.New("context.Build: agentID required")
	}

	// 1. 解析 quotas
	override := persona.ContextQuotas{Personal: -1, Procedural: -1, RAG: -1, Episodic: -1}
	if parsed != nil {
		override = parsed.ContextQuotas()
	}
	q := b.defaults.Resolve(override)

	// 2. 並發拉取五路（personal / procedural / rag / episodic / semantic）
	type result struct {
		personal   *models.PersonalMemory
		procedural []models.ProceduralMemory
		hits       []vectordb.SearchHit
		episodic   []models.EpisodicMemory
		semantic   []models.LongTermMemoryHit
		errs       []error
	}
	r := result{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	addErr := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		r.errs = append(r.errs, err)
		mu.Unlock()
	}

	// PersonalMemory（只在 instruction 為空時補）
	if q.Personal > 0 && strings.TrimSpace(instruction) == "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pm, err := b.memory.GetPersonalByAgent(ctx, agentID)
			if err != nil && !errors.Is(err, services.ErrNotFound) {
				addErr(fmt.Errorf("personal: %w", err))
				return
			}
			r.personal = pm
		}()
	}

	// ProceduralMemory（只在 instruction 為空時補；instruction 已含 mental models）
	if q.Procedural > 0 && strings.TrimSpace(instruction) == "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := b.memory.ListProceduralByAgent(ctx, agentID, q.Procedural)
			if err != nil {
				addErr(fmt.Errorf("procedural: %w", err))
				return
			}
			r.procedural = list
		}()
	}

	// RAG chunks（總是查；query 空則跳過）
	if q.RAG > 0 && strings.TrimSpace(query) != "" && b.rag != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hits, err := b.rag.SearchByQuery(ctx, agentID, query, q.RAG)
			if err != nil {
				addErr(fmt.Errorf("rag: %w", err))
				return
			}
			r.hits = hits
		}()
	}

	// EpisodicMemory（總是查）
	if q.Episodic > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := b.memory.ListEpisodicByAgent(ctx, agentID, q.Episodic)
			if err != nil {
				addErr(fmt.Errorf("episodic: %w", err))
				return
			}
			r.episodic = list
		}()
	}

	// Semantic memory（LongTermMemory ANN；總是查，query 空則跳過，跟 RAG 同邏輯）
	if q.Semantic > 0 && strings.TrimSpace(query) != "" && b.semantic != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hits, err := b.semantic.Search(ctx, agentID, query, q.Semantic)
			if err != nil {
				addErr(fmt.Errorf("semantic: %w", err))
				return
			}
			r.semantic = hits
		}()
	}

	wg.Wait()

	// errors 不阻斷主流程，只記錄；caller 透過 SourceCounts 觀察
	// （部分來源失敗時，其他來源仍可組裝出有用 context）

	// 3. 組裝 system prompt
	var sb strings.Builder

	// 3a. persona instruction（已含 Personal + Procedural）
	if strings.TrimSpace(instruction) != "" {
		sb.WriteString(strings.TrimRight(instruction, "\n"))
		sb.WriteString("\n\n")
	} else {
		// fallback：用 DB 取的 PersonalMemory + Procedural 組
		if r.personal != nil {
			renderPersonalFallback(&sb, r.personal)
		}
		if len(r.procedural) > 0 {
			renderProceduralFallback(&sb, r.procedural)
		}
	}

	// 3b. RAG knowledge
	if len(r.hits) > 0 {
		renderRAG(&sb, r.hits)
	}

	// 3c. Semantic memory（distilled facts，跟 RAG 不同來源：agent 自己學到的知識）
	if len(r.semantic) > 0 {
		renderSemantic(&sb, r.semantic)
	}

	// 3d. EpisodicMemory（最近相似對話）
	if len(r.episodic) > 0 {
		renderEpisodic(&sb, r.episodic)
	}

	out := strings.TrimRight(sb.String(), "\n") + "\n"

	// 3d. 字元預算（粗略上限，先套用）
	if q.MaxChars > 0 {
		out = truncateRunes(out, q.MaxChars)
	}
	// 3e. token 預算（用 TokenCounter 估算後截斷）
	if q.MaxTokens > 0 && b.tokens != nil {
		out = b.tokens.Truncate(out, q.MaxTokens)
	}

	counts := SourceCounts{
		Procedural: len(r.procedural),
		RAG:        len(r.hits),
		Episodic:   len(r.episodic),
		Semantic:   len(r.semantic),
	}
	if r.personal != nil {
		counts.Personal = 1
	}
	if b.tokens != nil {
		counts.EstTokens = b.tokens.Count(out)
	}

	return &BuildResult{
		SystemPrompt: out,
		Counts:       counts,
	}, nil
}

// ── render helpers ─────────────────────────────────────────────────────────

func renderPersonalFallback(sb *strings.Builder, pm *models.PersonalMemory) {
	sb.WriteString("## About You\n")
	for k, v := range pm.Identity {
		fmt.Fprintf(sb, "- %s: %v\n", k, v)
	}
	if len(pm.ExpressionDNA) > 0 {
		sb.WriteString("\n## Communication Style\n")
		for k, v := range pm.ExpressionDNA {
			fmt.Fprintf(sb, "- %s: %v\n", k, v)
		}
	}
	sb.WriteString("\n")
}

func renderProceduralFallback(sb *strings.Builder, list []models.ProceduralMemory) {
	sb.WriteString("## Mental Models\n\n")
	for _, m := range list {
		fmt.Fprintf(sb, "### %s\n", m.ModelName)
		if m.Content != "" {
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
		if len(m.Heuristics) > 0 {
			sb.WriteString("\nHeuristics:\n")
			for k, v := range m.Heuristics {
				fmt.Fprintf(sb, "- %s: %v\n", k, v)
			}
		}
		sb.WriteString("\n")
	}
}

func renderRAG(sb *strings.Builder, hits []vectordb.SearchHit) {
	sb.WriteString("## Knowledge (Retrieved References)\n\n")
	sb.WriteString("以下片段由語義搜尋從文件庫取得，按相關性排序。回答時可引用、不要捏造：\n\n")
	for i, h := range hits {
		fmt.Fprintf(sb, "### [%d] %s", i+1, h.DocID)
		if h.ChunkID != "" {
			fmt.Fprintf(sb, "  (chunk=%s)", h.ChunkID)
		}
		sb.WriteString("\n")
		if t := strings.TrimSpace(h.Text); t != "" {
			sb.WriteString(t)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
}

func renderSemantic(sb *strings.Builder, hits []models.LongTermMemoryHit) {
	sb.WriteString("## Things You Know (Semantic Memory)\n\n")
	sb.WriteString("以下是你過去累積、自己寫下的知識/事實，按相關性排序。可信度視為自己過去的結論：\n\n")
	for i, h := range hits {
		fmt.Fprintf(sb, "### [%d] %s", i+1, h.Category)
		if h.Source != "" {
			fmt.Fprintf(sb, "  (source=%s)", h.Source)
		}
		sb.WriteString("\n")
		if h.Summary != "" {
			sb.WriteString(h.Summary)
			sb.WriteString("\n")
		} else if t := strings.TrimSpace(h.Content); t != "" {
			sb.WriteString(t)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
}

func renderEpisodic(sb *strings.Builder, list []models.EpisodicMemory) {
	sb.WriteString("## Recent Conversations (Episodic Memory)\n\n")
	sb.WriteString("以下為近期對話片段，可參考但不要重複過去回應：\n\n")
	for i, ep := range list {
		fmt.Fprintf(sb, "### Conversation %d (session=%s)\n", i+1, ep.SessionID)
		if ep.Summary != "" {
			sb.WriteString("Summary: ")
			sb.WriteString(ep.Summary)
			sb.WriteString("\n")
		}
		// Turns 是 jsonb，避免一次塞入過多原文，僅標示
		if len(ep.Turns) > 0 {
			fmt.Fprintf(sb, "Turns: %d entries\n", len(ep.Turns))
		}
		sb.WriteString("\n")
	}
}

// truncateRunes 以 rune（字元）為單位截斷，避免破壞 UTF-8 字元
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n[... context truncated to " + fmt.Sprintf("%d", max) + " chars ...]\n"
}
