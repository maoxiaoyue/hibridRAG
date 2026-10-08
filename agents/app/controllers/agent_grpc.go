package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"agent/app/models"
	"agent/app/services"
	ctxbuilder "agent/internal/agent/context"
	"agent/internal/agent/persona"
)

// SkillProvider abstracts SkillLoader for dependency injection and testing.
// Satisfied by *services.SkillLoader.
type SkillProvider interface {
	Get(agentID string) (*models.SkillPack, error)
	Load(agentID string, force bool) (pack *models.SkillPack, cached bool, reloaded []string, err error)
}

// EpisodicWriter abstracts the EpisodicMemory write path.
// Satisfied by *services.MemoryService.
// 用於封閉學習迴圈：每輪 inbound + outbound 寫入 episodic_memory。
type EpisodicWriter interface {
	CreateEpisodic(req models.CreateEpisodicMemoryRequest) (*models.EpisodicMemory, error)
}

// ModelResolver abstracts CompilingSkillLoader's model-preference lookup.
// Satisfied by *services.CompilingSkillLoader.
type ModelResolver interface {
	ModelResolutionFor(agentID string) (*services.ModelResolution, bool)
}

// CriticEvaluator abstracts the A3 Critic/Gatekeeper answer-quality check.
// Satisfied by *services.CriticGRPCClient. nil → revision loop disabled,
// process() returns the first generated answer unchanged (behavior before
// this dependency existed).
type CriticEvaluator interface {
	Evaluate(ctx context.Context, agentID, sessionID, question, answer string, sources []string, revision int) (*services.CriticEvaluation, error)
}

// AgentServer implements models.AgentServiceServer — the gRPC Inbound endpoint.
//
// End-to-end flow:
//
//	InboundReq → validate → assign message_id → ack immediately
//	goroutine: SkillPack → persona.Parse
//	         → ContextBuilder.Build (RAG + memory)
//	         → LLMClient.Chat → OutboundMsg → Dispatcher.Send
type AgentServer struct {
	models.UnimplementedAgentServiceServer

	// selfID は OutboundMsg.From に使用する本 agent の識別子
	selfID        string
	skills        SkillProvider          // SKILL.md ローダー
	llm           services.LLMClient     // nil → standalone mode（LLM 推理をスキップ）
	dispatcher    services.Dispatcher    // nil → no outbound dispatch（ログのみ）
	ctxBuilder    *ctxbuilder.Builder    // nil → 走 instruction-only 模式（不查 RAG / Episodic）
	episodic      EpisodicWriter         // nil → 不寫入 episodic_memory（封閉學習迴圈停用）
	pool          *services.WorkerPool   // nil → bare goroutine（無限流）
	tools         *services.ToolRegistry // nil 或 empty → 跳 tool loop，走純 LLM.Chat
	toolOpts      services.ToolLoopOptions
	sessionStore  *services.SessionStore  // nil → /api/chat 每輪視為單輪（不讀寫多輪歷史）
	modelResolver ModelResolver           // nil → 一律用 s.llm（persona 的 ## Model 覆寫未接線，或非 ollama 模式）
	llmPool       *services.LLMClientPool // nil → resolveLLM 一律回 s.llm
	critic        CriticEvaluator         // nil → process() 不評分、不進 revision loop
	criticMaxRev  int                     // 評分未通過時最多重新生成幾次；0 = 只評分不重生
	status        *services.ChatStatusTracker // nil → 不記錄階段（client 狀態輪詢功能未接線）
	tempAppender  TempAppender                // nil → 對話不沉澱進知識庫 vault/temp
	gpuMon        *services.GPUMonitor        // nil → 推理前不做 GPU 狀態確認（監控未接線）
}

// TempAppender 把一段文字寫進 agent_graph 的 vault/temp/（知識沉澱管線暫存入口）。
// 由 *services.GraphGRPCClient 滿足；nil 時對話結束不沉澱。
type TempAppender interface {
	AppendTemp(ctx context.Context, title, content, source, prefix string) (string, error)
}

// WithStatus 注入 chat 階段追蹤器（供 client 每秒輪詢「目前哪個 Agent 在處理」）。
// 不傳則不記錄階段；Set/Clear 皆容忍 nil，行為與改動前相同。
func (s *AgentServer) WithStatus(t *services.ChatStatusTracker) *AgentServer {
	s.status = t
	return s
}

// WithTempAppender 注入知識沉澱暫存寫入器（對話結束後把問答寫進 vault/temp）。
// 不傳則對話不沉澱進知識庫，行為與改動前相同。
func (s *AgentServer) WithTempAppender(a TempAppender) *AgentServer {
	s.tempAppender = a
	return s
}

// WithGPUMonitor 注入 Ollama/GPU 監控：Agent 1-4 推理管線開始前確認 GPU 狀態
//（快取過期才重取樣，超門檻記 warning 不擋請求）。不傳則不檢查，行為不變。
func (s *AgentServer) WithGPUMonitor(m *services.GPUMonitor) *AgentServer {
	s.gpuMon = m
	return s
}

// NewAgentServer returns a ready-to-register AgentServer.
// dispatcher, llm, and ctxBuilder may be nil for standalone / test mode.
func NewAgentServer(
	selfID string,
	skills SkillProvider,
	llm services.LLMClient,
	dispatcher services.Dispatcher,
) *AgentServer {
	return &AgentServer{
		selfID:     selfID,
		skills:     skills,
		llm:        llm,
		dispatcher: dispatcher,
	}
}

// WithContextBuilder 注入 ContextBuilder（RAG + Memory 組裝）。
// 不傳則 process() 走純 instruction（SKILL.md only）模式。
func (s *AgentServer) WithContextBuilder(b *ctxbuilder.Builder) *AgentServer {
	s.ctxBuilder = b
	return s
}

// WithEpisodicWriter 注入 EpisodicMemory 寫入器（封閉學習迴圈）。
// 不傳則不寫入 episodic_memory（process() 只做推理）。
func (s *AgentServer) WithEpisodicWriter(w EpisodicWriter) *AgentServer {
	s.episodic = w
	return s
}

// WithSessionStore 注入短期記憶（多輪對話歷史，Redis-backed）。
// 只有 ChatSync（/api/chat）使用；不傳則每輪視為單輪，行為與改動前相同。
func (s *AgentServer) WithSessionStore(store *services.SessionStore) *AgentServer {
	s.sessionStore = store
	return s
}

// WithWorkerPool 注入有界 worker pool（限流）。
// 不傳則 Inbound 用 bare goroutine 派工（無限流，僅適合單機 / dev）。
// 投遞失敗（佇列滿）時 Inbound 回 ack.Code=429。
func (s *AgentServer) WithWorkerPool(p *services.WorkerPool) *AgentServer {
	s.pool = p
	return s
}

// WithToolRegistry 注入 ToolRegistry + ToolLoopOptions。
// 條件式啟用：registry 非 nil 且至少有一個 tool，且 llm 滿足 ToolCallerLLM 時，
// process() 會用 RunToolLoop 取代純 LLM.Chat；其餘情況自動退回 LLM.Chat。
func (s *AgentServer) WithToolRegistry(reg *services.ToolRegistry, opts services.ToolLoopOptions) *AgentServer {
	s.tools = reg
	s.toolOpts = opts
	return s
}

// WithModelResolver 注入 persona ## Model 偏好的解析結果來源。
// 不傳（或 llmPool 也不傳）則 resolveLLM 一律回傳 s.llm，行為與改動前相同。
func (s *AgentServer) WithModelResolver(r ModelResolver) *AgentServer {
	s.modelResolver = r
	return s
}

// WithLLMPool 注入依 model 名稱快取 LLMClient 的池（僅 ollama 模式接線）。
func (s *AgentServer) WithLLMPool(p *services.LLMClientPool) *AgentServer {
	s.llmPool = p
	return s
}

// WithCritic 注入 A3 Critic/Gatekeeper 評分器 + revision 次數上限。
// evaluator 為 nil 時 process() 完全略過評分（maxRevisions 隨之無意義）。
func (s *AgentServer) WithCritic(evaluator CriticEvaluator, maxRevisions int) *AgentServer {
	s.critic = evaluator
	if maxRevisions < 0 {
		maxRevisions = 0
	}
	s.criticMaxRev = maxRevisions
	return s
}

// resolveLLM 回傳這個 request（依 agentID）該用的 LLMClient。modelResolver
// 或 llmPool 為 nil（功能未接線，或非 ollama 模式）、查無 agentID 的解析結果、
// 或解析結果的 Model 為空，都直接 fallback 回 s.llm——完全比照改動前的行為。
func (s *AgentServer) resolveLLM(agentID string) services.LLMClient {
	if s.modelResolver == nil || s.llmPool == nil {
		return s.llm
	}
	res, ok := s.modelResolver.ModelResolutionFor(agentID)
	if !ok || res == nil || res.Model == "" {
		return s.llm
	}
	return s.llmPool.Get(res.Model)
}

// Inbound は上游 agent から InboundReq を受け取る gRPC Unary RPC。
//
// 処理:
//  1. 基本バリデーション（nil チェック、空メッセージ、agent_id）
//  2. message_id を採番
//  3. InboundAck を即時返却（fire-and-forget）
//  4. バックグラウンド goroutine で推理 → Outbound dispatch
func (s *AgentServer) Inbound(ctx context.Context, req *models.InboundReq) (*models.InboundAck, error) {
	// ── 基本バリデーション ────────────────────────────────────────────────
	if req == nil || req.Payload == nil {
		return &models.InboundAck{
			TraceId:  safeTraceID(req),
			Accepted: false,
			Code:     400,
			Error:    "nil payload",
		}, nil
	}
	if len(req.Payload.Messages) == 0 {
		return &models.InboundAck{
			TraceId:  req.TraceId,
			Accepted: false,
			Code:     400,
			Error:    "empty messages",
		}, nil
	}
	if req.Payload.AgentId == "" {
		return &models.InboundAck{
			TraceId:  req.TraceId,
			Accepted: false,
			Code:     400,
			Error:    "missing agent_id in payload",
		}, nil
	}

	msgID := newMsgID()

	// Trace propagation：metadata header 若有 trace_id，覆蓋 payload 缺值。
	if req.TraceId == "" {
		if tid := services.TraceIDFromContext(ctx); tid != "" {
			req.TraceId = tid
		} else {
			req.TraceId = services.NewTraceID()
		}
	}

	// 派工：有 pool 走限流；無 pool 退到 bare goroutine。
	// 為了讓推理在 gRPC 連線斷後仍能完成，用 Background 為基底並補回 trace_id。
	bg := services.InjectTraceID(context.Background(), req.TraceId)
	job := func() { s.process(bg, req, msgID) }

	if s.pool != nil {
		if err := s.pool.TrySubmit(job); err != nil {
			log.Printf("[agent-grpc][%s] worker pool full (queued=%d inflight=%d): %v",
				req.TraceId, s.pool.Queued(), s.pool.Inflight(), err)
			return &models.InboundAck{
				TraceId:  req.TraceId,
				Accepted: false,
				Code:     429,
				Error:    "agent busy: " + err.Error(),
			}, nil
		}
	} else {
		go job()
	}

	return &models.InboundAck{
		TraceId:   req.TraceId,
		MessageId: msgID,
		Accepted:  true,
		Code:      0,
	}, nil
}

// process はバックグラウンド goroutine で実行される推理→返信フロー。
//
// 流程：
//  1. 載入 SkillPack → persona.Parse → ToInstruction（system prompt 基底）
//  2. 用最新 user message 作 query → ContextBuilder 並發查 RAG + Episodic
//  3. instruction + RAG + Episodic 組成最終 system prompt → LLM Chat
//  4. Outbound dispatch
func (s *AgentServer) process(ctx context.Context, req *models.InboundReq, msgID string) {
	agentID := req.Payload.AgentId
	traceID := req.TraceId

	log.Printf("[agent-grpc][%s] start: agent=%s from=%s msg=%s", traceID, agentID, req.From, msgID)

	// Agent 1-4 管線開始前確認 Ollama/GPU 狀態（同 /api/chat 路徑；未接線 no-op）。
	s.gpuMon.CheckBeforeInference(ctx)

	// ── 1. SkillPack → persona instruction ────────────────────────────────
	parsed, instruction := s.loadPersona(traceID, agentID)

	// ── 2. ContextBuilder（若注入了）→ 加上 RAG + Episodic ───────────────
	query := lastUserContent(req.Payload.Messages)
	systemPrompt := instruction
	if s.ctxBuilder != nil {
		buildCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		result, err := s.ctxBuilder.BuildWithPersona(buildCtx, agentID, query, parsed, instruction)
		cancel()
		if err != nil {
			log.Printf("[agent-grpc][%s] context build error: %v — fallback to instruction-only",
				traceID, err)
		} else if result != nil {
			systemPrompt = result.SystemPrompt
			log.Printf("[agent-grpc][%s] context built: personal=%d procedural=%d rag=%d episodic=%d (system=%d chars, ~%d tokens)",
				traceID,
				result.Counts.Personal, result.Counts.Procedural,
				result.Counts.RAG, result.Counts.Episodic,
				len(systemPrompt), result.Counts.EstTokens)
		}
	}

	// ── 3. LLM 推理 ───────────────────────────────────────────────────────
	// resolveLLM 依 agentID 挑選 persona 的 ## Model 偏好指定的 client；
	// 未接線或該 persona 沒有覆寫時回傳全域預設 s.llm（行為與改動前相同）。
	llm := s.resolveLLM(agentID)
	if llm == nil {
		log.Printf("[agent-grpc][%s] no LLM configured; skipping inference", traceID)
		s.sendReply(ctx, req, "[standalone] no LLM configured")
		return
	}

	msgs := buildChatMessages(systemPrompt, req.Payload.Messages)

	var temperature float32
	if meta := req.Payload.Metadata; meta != nil {
		temperature = float32(meta.Temperature)
	}

	chatCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	// 內建工具（memory_read/write、rag_query）依此 ctx 值以 request 的 persona
	// 為記憶/RAG scope，而不是 config agent.id（否則 persona B 會寫進本體的記憶）。
	chatCtx = services.WithToolAgentID(chatCtx, agentID)

	// generate 跑一輪推理（tool loop 優先，否則純 Chat），供初次產出與 critic
	// revision loop 重用同一條路徑。usedToolLoop 只影響錯誤訊息文字。
	var lastUsage services.TokenUsage
	var lastFinish string
	usedToolLoop := false
	generate := func(msgs []services.ChatMessage) (string, error) {
		if toolLLM := s.tools; toolLLM != nil && len(toolLLM.List()) > 0 {
			if tc, ok := llm.(services.ToolCallerLLM); ok {
				usedToolLoop = true
				toolList := toolLLM.List()
				log.Printf("[agent-grpc][%s] tool loop enabled: tools=%d", traceID, len(toolList))
				content, _, traces, err := services.RunToolLoop(
					chatCtx, tc, toolLLM, msgs, toolList, s.toolOpts,
				)
				for _, tr := range traces {
					log.Printf("[agent-grpc][%s] tool call iter=%d name=%s ok=%v err=%q",
						traceID, tr.Iter, tr.Name, tr.OK, tr.Err)
				}
				lastFinish = "tool_loop"
				return content, err
			}
		}
		usedToolLoop = false
		resp, err := llm.Chat(chatCtx, services.ChatRequest{
			Messages:    msgs,
			Temperature: temperature,
		})
		if err != nil {
			return "", err
		}
		lastUsage = resp.Usage
		lastFinish = resp.FinishReason
		log.Printf("[agent-grpc][%s] LLM done: finish=%s prompt=%d completion=%d",
			traceID, resp.FinishReason, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		return resp.Content, nil
	}

	content, err := generate(msgs)
	if err != nil {
		errLabel := "LLM inference failed"
		if usedToolLoop {
			errLabel = "tool loop failed"
		}
		log.Printf("[agent-grpc][%s] %s: %v", traceID, errLabel, err)
		s.sendReply(ctx, req, fmt.Sprintf("[error] %s: %v", errLabel, err))
		return
	}

	// ── 4. Critic 評分 + 有限次數 revision loop（s.critic 為 nil 時原樣通過）──
	content = s.reviseWithCritic(chatCtx, traceID, agentID, req.Payload.SessionId, query, systemPrompt, content, msgs, generate)

	// ── 5. Episodic write-back（封閉學習迴圈；best-effort，不阻斷 outbound） ──
	s.writeEpisodic(traceID, req, content, lastUsage.TotalTokens, lastFinish, msgID)

	// ── 6. Outbound dispatch ──────────────────────────────────────────────
	s.sendReply(ctx, req, content)
}

// reviseWithCritic 送目前答案給 A3 Critic 評分；未通過且還有 revision 額度時，
// 把 feedback 附成新一則 user turn 重新產生答案，最多 s.criticMaxRev 次。
// s.critic 為 nil、評分/重生失敗都是 best-effort——直接回傳當下最好的答案，
// 不會讓整個請求失敗（Critic 是品質加分項，不是推理鏈的硬依賴）。
func (s *AgentServer) reviseWithCritic(
	ctx context.Context,
	traceID, agentID, sessionID, question, sources, content string,
	msgs []services.ChatMessage,
	generate func([]services.ChatMessage) (string, error),
) string {
	if s.critic == nil {
		return content
	}
	for revision := 0; ; revision++ {
		eval, err := s.critic.Evaluate(ctx, agentID, sessionID, question, content, []string{sources}, revision)
		if err != nil {
			log.Printf("[agent-grpc][%s] critic evaluate failed (revision=%d): %v — accepting answer as-is",
				traceID, revision, err)
			return content
		}
		log.Printf("[agent-grpc][%s] critic: revision=%d overall=%.2f passed=%v",
			traceID, revision, eval.Overall, eval.Passed)
		if eval.Passed || revision >= s.criticMaxRev {
			return content
		}
		msgs = append(msgs, services.ChatMessage{
			Role: services.RoleUser,
			Content: fmt.Sprintf(
				"[critic feedback] Your previous answer did not fully pass quality review: %s. Please revise your answer accordingly.",
				strings.Join(eval.Feedback, "; ")),
		})
		revised, genErr := generate(msgs)
		if genErr != nil {
			log.Printf("[agent-grpc][%s] revision generate failed (revision=%d): %v — keeping previous answer",
				traceID, revision+1, genErr)
			return content
		}
		content = revised
	}
}

// writeEpisodic 把本輪 user input + assistant output 寫入 episodic_memory。
// 失敗只記 log，不影響 outbound dispatch。
func (s *AgentServer) writeEpisodic(
	traceID string,
	req *models.InboundReq,
	assistantContent string,
	totalTokens int,
	finishReason string,
	msgID string,
) {
	if s.episodic == nil {
		return
	}
	sessionID := req.Payload.SessionId
	if sessionID == "" {
		// 無 session_id 時用 trace_id 當代用（仍能查回）
		sessionID = "trace_" + traceID
	}
	userMsg := lastUserContent(req.Payload.Messages)

	turns := map[string]interface{}{
		"trace_id":      traceID,
		"message_id":    msgID,
		"from":          req.From,
		"agent_id":      req.Payload.AgentId,
		"user_id":       req.Payload.UserId,
		"user_input":    userMsg,
		"assistant":     assistantContent,
		"finish_reason": finishReason,
		"total_tokens":  totalTokens,
		"occurred_at":   time.Now().UTC().Format(time.RFC3339Nano),
	}

	_, err := s.episodic.CreateEpisodic(models.CreateEpisodicMemoryRequest{
		AgentID:   req.Payload.AgentId,
		SessionID: sessionID,
		Turns:     turns,
		Summary:   truncate(strings.TrimSpace(userMsg), 200),
	})
	if err != nil {
		log.Printf("[agent-grpc][%s] episodic write error: %v (non-fatal)", traceID, err)
		return
	}
	log.Printf("[agent-grpc][%s] episodic recorded: session=%s tokens=%d", traceID, sessionID, totalTokens)
}

// lastUserContent 取出 messages 中最後一條 role=user 的 content（作為 RAG query）。
// 找不到時回傳空字串，ContextBuilder 會跳過 RAG 查詢。
func lastUserContent(msgs []*models.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m == nil {
			continue
		}
		if strings.EqualFold(m.Role, "user") {
			return strings.TrimSpace(m.Content)
		}
	}
	return ""
}

// loadPersona は agentID に対応する SkillPack を取得し、
// persona.Parse 結果（quotas 抽出に使用）と ToInstruction 文字列を返す。
// SkillPack が存在しない場合は (nil, "") を返す（エラーにしない）。
func (s *AgentServer) loadPersona(traceID, agentID string) (*persona.ParseResult, string) {
	pack, err := s.skills.Get(agentID)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			// キャッシュミス → ディスクから読み込む
			var loadErr error
			pack, _, _, loadErr = s.skills.Load(agentID, false)
			if loadErr != nil {
				log.Printf("[agent-grpc][%s] skill load failed (agent=%s): %v — proceeding without system prompt",
					traceID, agentID, loadErr)
				return nil, ""
			}
		} else {
			log.Printf("[agent-grpc][%s] skill get error: %v", traceID, err)
			return nil, ""
		}
	}
	if pack == nil || pack.Body == "" {
		return nil, ""
	}
	result := persona.Parse(pack.Body)
	instruction := result.ToInstruction()
	if instruction != "" {
		log.Printf("[agent-grpc][%s] instruction loaded for agent=%s (%d chars)", traceID, agentID, len(instruction))
	}
	return result, instruction
}

// sendReply は content を OutboundMsg に包み、Dispatcher 経由で req.From に送る。
// dispatcher が nil または req.From が空の場合はログのみ（standalone mode）。
func (s *AgentServer) sendReply(ctx context.Context, req *models.InboundReq, content string) {
	if s.dispatcher == nil || req.From == "" {
		log.Printf("[agent-grpc][%s] reply (standalone, from=%q): %s", req.TraceId, req.From, truncate(content, 120))
		return
	}

	outMsg := &models.OutboundMsg{
		TraceId:   req.TraceId,
		From:      s.selfID,
		To:        req.From,
		Direction: "reply",
		Payload: &models.AgentReq{
			SessionId: req.Payload.SessionId,
			UserId:    req.Payload.UserId,
			AgentId:   req.Payload.AgentId,
			Messages: []*models.Message{
				{Role: "assistant", Content: content},
			},
		},
	}

	ack, err := s.dispatcher.Send(ctx, outMsg)
	if err != nil {
		log.Printf("[agent-grpc][%s] dispatch error → %s: %v", req.TraceId, req.From, err)
		return
	}
	log.Printf("[agent-grpc][%s] dispatched → %s accepted=%v code=%d",
		req.TraceId, req.From, ack.Accepted, ack.Code)
}

// ── helpers ──────────────────────────────────────────────────────────────────

// newMsgID 生成 "msg_<16 hex chars>" の一意なメッセージ ID。
func newMsgID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "msg_" + hex.EncodeToString(b)
}

// safeTraceID は nil InboundReq でも安全に trace_id を返す。
func safeTraceID(req *models.InboundReq) string {
	if req == nil {
		return ""
	}
	return req.TraceId
}

// buildChatMessages は system prompt + proto Messages を services.ChatMessage スライスに変換する。
func buildChatMessages(instruction string, protoMsgs []*models.Message) []services.ChatMessage {
	var out []services.ChatMessage
	if instruction != "" {
		out = append(out, services.ChatMessage{
			Role:    services.RoleSystem,
			Content: instruction,
		})
	}
	for _, m := range protoMsgs {
		out = append(out, services.ChatMessage{
			Role:    protoRoleToChat(m.Role),
			Content: m.Content,
		})
	}
	return out
}

// protoRoleToChat は proto Message の role 文字列を services.ChatRole に変換する。
func protoRoleToChat(role string) services.ChatRole {
	switch role {
	case "system":
		return services.RoleSystem
	case "assistant":
		return services.RoleAssistant
	default: // "user" + その他
		return services.RoleUser
	}
}

// truncate はログ出力用に文字列を maxLen 文字に切り詰める。
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "…"
}
