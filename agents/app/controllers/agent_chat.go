// agent_chat.go — 同步對話端點 POST /api/chat 的 handler 與核心迴圈。
//
// 與 agent_grpc.go 的 Inbound（fire-and-forget、回覆 dispatch 到下游 gRPC）不同，
// 本端點同步回傳 agent 回覆，並支援「client 端工具回合制」：
//
//	首次:  client → {agent_id, messages, client_tools}
//	       server 跑 persona + ContextBuilder 組 system prompt → LLM ChatWithTools
//	         - server 內建工具（memory/rag/web_search）就地執行
//	         - LLM 呼叫到 client 工具 → 暫停，回 {status:"tool_calls", continuation_id, tool_calls}
//	接續:  client 本地執行工具後 → {continuation_id, tool_results}
//	       server 接續迴圈 → 直到無工具呼叫 → {status:"final", content}
//
// 復用 AgentServer 既有依賴（skills / llm / ctxBuilder / episodic / tools），
// 以及同 package 的 loadPersona / protoRoleToChat / truncate。
//
// continuation 狀態存在記憶體 map（單機）。production（K8s 多副本）應改 Redis（已有 hidb）。
package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	hypctx "github.com/maoxiaoyue/hypgo/pkg/context"

	"agent/app/models"
	"agent/app/services"
)

// ── Chat 執行器注入（仿 SetIngestRunner） ──────────────────────────────────

var (
	chatServerMu sync.RWMutex
	chatServer   *AgentServer
)

// SetChatServer 由 main.go 注入已組好依賴的 AgentServer（與 gRPC 共用同一個）。
func SetChatServer(s *AgentServer) {
	chatServerMu.Lock()
	defer chatServerMu.Unlock()
	chatServer = s
}

func getChatServer() *AgentServer {
	chatServerMu.RLock()
	defer chatServerMu.RUnlock()
	return chatServer
}

// ── HTTP handler ──────────────────────────────────────────────────────────

// Chat POST /api/chat — 同步對話 + client 端工具回合制。
func Chat(ctx *hypctx.Context) {
	srv := getChatServer()
	if srv == nil {
		ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, H{"error": "chat not configured"})
		return
	}
	var req models.ChatTurnRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, H{"error": "invalid request", "details": err.Error()})
		return
	}
	resp, code, err := srv.ChatSync(ctx.Request.Context(), req)
	if err != nil {
		ctx.AbortWithStatusJSON(code, H{"error": err.Error()})
		return
	}
	ctx.JSON(code, resp)
}

// ── continuation store（記憶體 + 惰性 TTL） ─────────────────────────────────

const chatContTTL = 30 * time.Minute

type chatCont struct {
	id          string
	agentID     string
	sessionID   string
	traceID     string
	lastUser    string
	temperature float32
	messages    []services.ChatMessage
	clientStubs []services.Tool
	clientNames map[string]bool
	createdAt   time.Time
}

var (
	chatContMu  sync.Mutex
	chatContMap = map[string]*chatCont{}
)

func chatContStore(c *chatCont) {
	chatContMu.Lock()
	defer chatContMu.Unlock()
	c.createdAt = time.Now()
	chatContMap[c.id] = c
	// 順手清掉過期項（惰性 GC，避免被遺棄的 continuation 累積）
	for id, cc := range chatContMap {
		if time.Since(cc.createdAt) > chatContTTL {
			delete(chatContMap, id)
		}
	}
}

func chatContLoad(id string) (*chatCont, bool) {
	chatContMu.Lock()
	defer chatContMu.Unlock()
	c, ok := chatContMap[id]
	if !ok {
		return nil, false
	}
	if time.Since(c.createdAt) > chatContTTL {
		delete(chatContMap, id)
		return nil, false
	}
	return c, true
}

func chatContDelete(id string) {
	if id == "" {
		return
	}
	chatContMu.Lock()
	delete(chatContMap, id)
	chatContMu.Unlock()
}

// ── 核心 ──────────────────────────────────────────────────────────────────

// ChatSync 同步推理。回傳 (resp, httpStatus, err)。
func (s *AgentServer) ChatSync(ctx context.Context, req models.ChatTurnRequest) (*models.ChatTurnResponse, int, error) {
	if req.AgentID == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("missing agent_id")
	}

	// ── 接續模式：把 client 工具結果接回 messages，續跑迴圈 ──
	if req.ContinuationID != "" {
		cont, ok := chatContLoad(req.ContinuationID)
		if !ok {
			return nil, http.StatusNotFound, fmt.Errorf("continuation not found or expired")
		}
		for _, tr := range req.ToolResults {
			cont.messages = append(cont.messages, toolResultMessage(tr.Name, tr.Content, tr.IsError))
		}
		return s.runChatLoop(ctx, cont)
	}

	// ── 首次模式：組 system prompt + 初始 messages ──
	if len(req.Messages) == 0 {
		return nil, http.StatusBadRequest, fmt.Errorf("messages required")
	}
	traceID := services.NewTraceID()
	parsed, instruction := s.loadPersona(traceID, req.AgentID)

	query := lastUserContentFromTurns(req.Messages)

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = "sess_" + traceID
	}
	// Agent 1：檢索 / 組 context（client 每秒輪詢時看得到目前處理中的階段）。
	s.status.Set(sessionID, services.StageContextBuilder)

	systemPrompt := instruction
	if s.ctxBuilder != nil {
		bctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		result, err := s.ctxBuilder.BuildWithPersona(bctx, req.AgentID, query, parsed, instruction)
		cancel()
		if err != nil {
			log.Printf("[chat][%s] context build error: %v — fallback to instruction-only", traceID, err)
		} else if result != nil {
			systemPrompt = result.SystemPrompt
		}
	}

	var msgs []services.ChatMessage
	if systemPrompt != "" {
		msgs = append(msgs, services.ChatMessage{Role: services.RoleSystem, Content: systemPrompt})
	}
	// 短期記憶：把這個 session 之前的對話輪次接進來（client 每輪只送最新一則訊息，
	// 不會重送歷史 — 見 agent_client/app/services/chat_service.go）。
	if s.sessionStore != nil {
		prior, err := s.sessionStore.Load(ctx, req.AgentID, sessionID)
		if err != nil {
			log.Printf("[chat][%s] session store load error: %v (non-fatal, continuing without history)", traceID, err)
		}
		msgs = append(msgs, prior...)
	}
	for _, m := range req.Messages {
		msgs = append(msgs, services.ChatMessage{Role: protoRoleToChat(m.Role), Content: m.Content})
	}

	stubs, names := buildClientTools(req.ClientTools)
	cont := &chatCont{
		agentID:     req.AgentID,
		sessionID:   sessionID,
		traceID:     traceID,
		lastUser:    query,
		temperature: req.Temperature,
		messages:    msgs,
		clientStubs: stubs,
		clientNames: names,
	}
	return s.runChatLoop(ctx, cont)
}

// runChatLoop 跑 LLM 迴圈：server 工具就地執行，client 工具則暫停回傳。
func (s *AgentServer) runChatLoop(
	ctx context.Context,
	cont *chatCont,
) (*models.ChatTurnResponse, int, error) {
	// 內建工具（execServerTool 執行的 memory_read/write、rag_query）依此 ctx 值
	// 以本輪 persona 為記憶/RAG scope，而不是 config agent.id。
	ctx = services.WithToolAgentID(ctx, cont.agentID)

	// Agent 2：LLM 生成 / 工具迴圈。離開（終局或暫停等 client 工具）時清掉階段，
	// client 的輪詢會轉為 inactive → 顯示自己的本地階段。
	s.status.Set(cont.sessionID, services.StageToolLoop)
	defer s.status.Clear(cont.sessionID)

	// Agent 1-4 管線開始前確認 Ollama/GPU 狀態（快取過期才重取樣；超門檻只記
	// warning 不擋請求——GPU 忙不該讓對話直接失敗）。gpuMon 未接線時 no-op。
	s.gpuMon.CheckBeforeInference(ctx)

	// resolveLLM 依 cont.agentID 挑選 persona 的 ## Model 偏好指定的 client；
	// 未接線或該 persona 沒有覆寫時回傳全域預設 s.llm（行為與改動前相同）。
	llm := s.resolveLLM(cont.agentID)
	if llm == nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("no LLM configured")
	}
	tcLLM, llmSupportsTools := llm.(services.ToolCallerLLM)

	// LLM 不支援 tool calling → 退回純 Chat（忽略 client_tools，單輪終局）。
	if !llmSupportsTools {
		resp, err := llm.Chat(ctx, services.ChatRequest{Messages: cont.messages, Temperature: cont.temperature})
		if err != nil {
			return nil, http.StatusBadGateway, fmt.Errorf("llm chat: %w", err)
		}
		s.writeChatEpisodic(cont, resp.Content, resp.Usage)
		s.saveSessionTurn(ctx, cont, resp.Content)
		s.appendChatToVaultTemp(cont, resp.Content)
		chatContDelete(cont.id)
		return &models.ChatTurnResponse{
			Status: "final", Content: resp.Content, SessionID: cont.sessionID,
			Usage: usagePtr(resp.Usage),
		}, http.StatusOK, nil
	}

	// 工具列表 = server 內建工具 + client 工具 stub（schema-only）。
	var serverTools []services.Tool
	if s.tools != nil {
		serverTools = s.tools.List()
	}
	allTools := make([]services.Tool, 0, len(serverTools)+len(cont.clientStubs))
	allTools = append(allTools, serverTools...)
	allTools = append(allTools, cont.clientStubs...)

	maxIter := s.toolOpts.MaxIterations
	if maxIter <= 0 {
		maxIter = 8
	}

	var lastUsage services.TokenUsage
	for iter := 0; iter < maxIter; iter++ {
		s.status.Set(cont.sessionID, services.StageToolLoop) // 每輪回到 ToolLoop（覆蓋前一輪的 Graph）
		resp, err := tcLLM.ChatWithTools(ctx,
			services.ChatRequest{Messages: cont.messages, Temperature: cont.temperature}, allTools)
		if err != nil {
			return nil, http.StatusBadGateway, fmt.Errorf("llm chat iter=%d: %w", iter, err)
		}
		lastUsage = resp.Usage

		// 終局：無工具呼叫。
		if len(resp.ToolCalls) == 0 {
			cont.messages = append(cont.messages, services.ChatMessage{Role: services.RoleAssistant, Content: resp.Content})
			s.writeChatEpisodic(cont, resp.Content, resp.Usage)
			s.saveSessionTurn(ctx, cont, resp.Content)
			s.appendChatToVaultTemp(cont, resp.Content)
			chatContDelete(cont.id)
			return &models.ChatTurnResponse{
				Status: "final", Content: resp.Content, SessionID: cont.sessionID,
				Usage: usagePtr(resp.Usage),
			}, http.StatusOK, nil
		}

		// 把 assistant 的 tool_calls 無損編碼進 messages（resume 時還原成原生 tool_calls，
		// 否則模型會忽略 tool result 甚至幻覺——已實測 gemma4）。
		cont.messages = append(cont.messages, services.ChatMessage{
			Role:    services.RoleAssistant,
			Content: services.EncodeAssistantToolCalls(resp.ToolCalls),
		})

		// 分流：server 工具就地跑；client 工具收集為 pending。
		var pendingClient []models.ClientToolCall
		for _, call := range resp.ToolCalls {
			if cont.clientNames[call.Name] {
				pendingClient = append(pendingClient, models.ClientToolCall{ID: call.ID, Name: call.Name, Args: call.Args})
				continue
			}
			if call.Name == "graph_query" {
				s.status.Set(cont.sessionID, services.StageGraph) // Agent 4：知識圖譜查詢
			}
			cont.messages = append(cont.messages, s.execServerTool(ctx, call))
		}

		// 有 client 工具待執行 → 持久化狀態並暫停回傳。
		if len(pendingClient) > 0 {
			if cont.id == "" {
				cont.id = newContID()
			}
			chatContStore(cont)
			return &models.ChatTurnResponse{
				Status: "tool_calls", SessionID: cont.sessionID,
				ContinuationID: cont.id, ToolCalls: pendingClient,
				Usage: usagePtr(lastUsage),
			}, http.StatusOK, nil
		}
		// 全是 server 工具 → 結果已附加，續跑下一輪。
	}

	return nil, http.StatusGatewayTimeout, fmt.Errorf("tool loop exceeded max iterations (%d)", maxIter)
}

// execServerTool 在 server 端就地執行一個內建工具，回傳 tool_result 訊息。
func (s *AgentServer) execServerTool(ctx context.Context, call services.ToolCall) services.ChatMessage {
	if s.tools == nil {
		return toolResultErr(call.Name, "no server tools")
	}
	tool, ok := s.tools.Get(call.Name)
	if !ok {
		return toolResultErr(call.Name, "unknown_tool")
	}
	tctx := ctx
	if s.toolOpts.ToolTimeoutSec > 0 {
		var cancel context.CancelFunc
		tctx, cancel = context.WithTimeout(ctx, time.Duration(s.toolOpts.ToolTimeoutSec)*time.Second)
		defer cancel()
	}
	res, err := tool.Call(tctx, call.Args)
	if err != nil {
		return toolResultErr(call.Name, err.Error())
	}
	return toolResultMessage(call.Name, res.Content, res.IsError)
}

// writeChatEpisodic 把本輪寫入 episodic_memory（封閉學習迴圈；best-effort）。
func (s *AgentServer) writeChatEpisodic(c *chatCont, assistant string, usage services.TokenUsage) {
	if s.episodic == nil {
		return
	}
	turns := map[string]interface{}{
		"trace_id":     c.traceID,
		"session_id":   c.sessionID,
		"agent_id":     c.agentID,
		"user_input":   c.lastUser,
		"assistant":    assistant,
		"total_tokens": usage.TotalTokens,
		"source":       "chat_sync",
		"occurred_at":  time.Now().UTC().Format(time.RFC3339Nano),
	}
	_, err := s.episodic.CreateEpisodic(models.CreateEpisodicMemoryRequest{
		AgentID:   c.agentID,
		SessionID: c.sessionID,
		Turns:     turns,
		Summary:   truncate(strings.TrimSpace(c.lastUser), 200),
	})
	if err != nil {
		log.Printf("[chat][%s] episodic write error: %v (non-fatal)", c.traceID, err)
	}
}

// appendChatToVaultTemp 把本輪問答沉澱進知識庫 vault/temp/（best-effort，非同步，
// 不阻塞回應）。之後由 agent_graph 的分類 worker 挑走、搬進分類目錄並攝取進圖譜。
// tempAppender 為 nil（未接線）時完全跳過，行為與改動前相同。
func (s *AgentServer) appendChatToVaultTemp(c *chatCont, assistant string) {
	if s.tempAppender == nil {
		return
	}
	user := strings.TrimSpace(c.lastUser)
	if user == "" || strings.TrimSpace(assistant) == "" {
		return // 沒有實質問答就不沉澱
	}
	title := truncate(user, 60)
	body := fmt.Sprintf("## Question\n\n%s\n\n## Answer\n\n%s", user, assistant)
	prefix := time.Now().Format("20060102")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := s.tempAppender.AppendTemp(ctx, title, body, "chat", prefix); err != nil {
			log.Printf("[chat][%s] vault temp append error: %v (non-fatal)", c.traceID, err)
		}
	}()
}

// saveSessionTurn 把本輪 user/assistant 訊息接到短期記憶（session history）；
// best-effort，跟 writeChatEpisodic 同模式。只存 role+content，不含工具呼叫細節
// （那是 chatCont 的單輪暫存狀態，不屬於跨輪對話歷史）。
func (s *AgentServer) saveSessionTurn(ctx context.Context, c *chatCont, assistant string) {
	if s.sessionStore == nil {
		return
	}
	err := s.sessionStore.Append(ctx, c.agentID, c.sessionID,
		services.ChatMessage{Role: services.RoleUser, Content: c.lastUser},
		services.ChatMessage{Role: services.RoleAssistant, Content: assistant},
	)
	if err != nil {
		log.Printf("[chat][%s] session store append error: %v (non-fatal)", c.traceID, err)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

// clientToolStub 是 client 工具的 schema-only 佔位實作（server 端永不執行）。
type clientToolStub struct {
	name   string
	desc   string
	schema services.ToolSchema
}

func (c clientToolStub) Name() string                { return c.name }
func (c clientToolStub) Description() string         { return c.desc }
func (c clientToolStub) Schema() services.ToolSchema { return c.schema }
func (c clientToolStub) Call(context.Context, map[string]any) (services.ToolResult, error) {
	return services.ToolResult{}, fmt.Errorf("client tool %q must be executed by the client", c.name)
}

// buildClientTools 把 client 宣告的工具 spec 轉成 stub + 名稱集合。
func buildClientTools(specs []models.ClientToolSpec) ([]services.Tool, map[string]bool) {
	stubs := make([]services.Tool, 0, len(specs))
	names := make(map[string]bool, len(specs))
	for _, sp := range specs {
		if sp.Name == "" {
			continue
		}
		var schema services.ToolSchema
		if len(sp.Schema) > 0 {
			if err := json.Unmarshal(sp.Schema, &schema); err != nil {
				log.Printf("[chat] client tool %q schema parse error: %v — using empty object schema", sp.Name, err)
			}
		}
		if schema.Type == "" {
			schema.Type = "object"
		}
		stubs = append(stubs, clientToolStub{name: sp.Name, desc: sp.Description, schema: schema})
		names[sp.Name] = true
	}
	return stubs, names
}

// toolResultMessage 把工具結果包成 RunToolLoop 通用形式（Ollama client 會轉回 role=tool）。
func toolResultMessage(name, content string, isErr bool) services.ChatMessage {
	tag := "ok"
	if isErr {
		tag = "error"
	}
	return services.ChatMessage{
		Role:    services.RoleUser,
		Content: fmt.Sprintf("[tool_result name=%s status=%s]\n%s", name, tag, content),
	}
}

func toolResultErr(name, errMsg string) services.ChatMessage {
	return services.ChatMessage{
		Role:    services.RoleUser,
		Content: fmt.Sprintf("[tool_result name=%s error=%q]", name, errMsg),
	}
}

// lastUserContentFromTurns 取最後一條 role=user 的 content（作 RAG query / episodic summary）。
func lastUserContentFromTurns(msgs []models.ChatTurnMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.EqualFold(msgs[i].Role, "user") {
			return strings.TrimSpace(msgs[i].Content)
		}
	}
	return ""
}

func usagePtr(u services.TokenUsage) *models.ChatUsage {
	return &models.ChatUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
}

// newContID 生成 "cont_<16 hex>" continuation id。
func newContID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "cont_" + hex.EncodeToString(b)
}
