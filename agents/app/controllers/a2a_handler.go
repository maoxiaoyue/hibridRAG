package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	hypctx "github.com/maoxiaoyue/hypgo/pkg/context"

	"agent/app/models"
	"agent/app/services"
	"agent/config"
	"agent/internal/agent/persona"
)

var (
	a2aOnce    sync.Once
	a2aSrv     *AgentServer
	a2aCfg     *config.Config
	a2aBuilder *services.AgentCardBuilder
)

// InitA2A wires A2A controller dependencies; called once from main.
func InitA2A(srv *AgentServer, cfg *config.Config) {
	a2aOnce.Do(func() {
		a2aSrv = srv
		a2aCfg = cfg
		agentID, publicURL := resolveA2ADefaults(cfg)
		a2aBuilder = services.NewAgentCardBuilder(agentID, "0.1.0", publicURL+"/")
	})
}

// A2ACardBuilder 回傳 InitA2A 建好的 builder，供 main.go 的 registry 自動註冊
// 共用——確保註冊進 agent_registry 的卡片與 /.well-known/agent-card.json 一致。
// InitA2A 尚未呼叫時回 nil。
func A2ACardBuilder() *services.AgentCardBuilder { return a2aBuilder }

// resolveA2ADefaults 算出這個 process 的預設 agentID + 對外 URL（原本內嵌在
// A2ACardHandler 裡逐 request 重算的邏輯，搬到 InitA2A 只算一次）。
func resolveA2ADefaults(cfg *config.Config) (agentID, publicURL string) {
	agentID = "agent-main"
	if cfg == nil {
		return agentID, publicURL
	}
	if cfg.Agent.ID != "" {
		agentID = cfg.Agent.ID
	}
	proto := "https"
	addr := cfg.Server.Addr
	if addr != "" && !strings.HasPrefix(addr, ":") {
		publicURL = proto + "://" + addr
	} else if addr != "" {
		publicURL = proto + "://localhost" + addr
	}
	return agentID, publicURL
}

// A2ACardHandler GET /.well-known/agent-card.json
//
// Agent Card 內容動態組自目前載入的 SkillPack（SKILL.md）——只用 Get()（cache
// 讀取），刻意不呼叫 Load()：這是高頻、無認證的 A2A 探索端點，不該觸發磁碟 I/O。
// 沒有可用的 SkillPack 時（cache miss、或這個 agentID 從未載入過任何 skill）
// AgentCardBuilder 會 fallback 回原本寫死的單一 "chat" skill，行為不變。
func A2ACardHandler(ctx *hypctx.Context) {
	agentID := "agent-main"
	if a2aCfg != nil && a2aCfg.Agent.ID != "" {
		agentID = a2aCfg.Agent.ID
	}

	var parsed *persona.ParseResult
	if a2aSrv != nil && a2aSrv.skills != nil {
		if pack, err := a2aSrv.skills.Get(agentID); err == nil && pack != nil {
			parsed = persona.Parse(pack.Body)
		}
	}

	builder := a2aBuilder
	if builder == nil {
		// InitA2A 尚未呼叫（不應發生於正常啟動流程，這裡只是保底避免 nil 解參考）
		builder = services.NewAgentCardBuilder(agentID, "0.1.0", "")
	}
	card := builder.Build(agentID, parsed)
	ctx.JSON(http.StatusOK, card)
}

// A2AHandler POST / — JSON-RPC 2.0 entry point
func A2AHandler(ctx *hypctx.Context) {
	var req models.A2ARequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeA2AErr(ctx, nil, models.A2AErrParse, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		writeA2AErr(ctx, req.ID, models.A2AErrInvalidRequest, `jsonrpc must be "2.0"`)
		return
	}

	switch req.Method {
	case "message/send":
		handleMessageSend(ctx, &req)
	case "tasks/get", "tasks/cancel", "tasks/subscribe":
		writeA2AErr(ctx, req.ID, models.A2AErrMethodNotFound, req.Method+" not yet implemented")
	default:
		writeA2AErr(ctx, req.ID, models.A2AErrMethodNotFound, "unknown method: "+req.Method)
	}
}

func handleMessageSend(ctx *hypctx.Context, req *models.A2ARequest) {
	var params models.MessageSendParams
	if req.Params != nil {
		raw, _ := json.Marshal(req.Params)
		if err := json.Unmarshal(raw, &params); err != nil {
			writeA2AErr(ctx, req.ID, models.A2AErrInvalidParams, "invalid params: "+err.Error())
			return
		}
	}

	// Determine agent_id: use x-skill hint, fall back to this agent's own ID
	agentID := "agent-main"
	if a2aCfg != nil && a2aCfg.Agent.ID != "" {
		agentID = a2aCfg.Agent.ID
	}
	if params.SkillHint != "" {
		agentID = params.SkillHint
	}

	// Extract text content from message
	content := extractContent(params.Message)
	if content == "" {
		writeA2AErr(ctx, req.ID, models.A2AErrInvalidParams, "message has no text content")
		return
	}

	// Map A2A role → InboundReq role (default to "user")
	role := params.Message.Role
	if role == "" {
		role = "user"
	}

	// task_id becomes trace_id; generate one if absent
	taskID := params.TaskID
	if taskID == "" {
		taskID = newA2ATaskID()
	}

	// Build InboundReq and delegate to AgentServer
	inboundReq := &models.InboundReq{
		TraceId: taskID,
		From:    "a2a-gateway",
		Payload: &models.AgentReq{
			SessionId: params.SessionID,
			AgentId:   agentID,
			Messages:  []*models.Message{{Role: role, Content: content}},
		},
	}

	if a2aSrv == nil {
		writeA2AErr(ctx, req.ID, models.A2AErrInternal, "agent server not initialized")
		return
	}

	ack, err := a2aSrv.Inbound(context.Background(), inboundReq)
	if err != nil {
		writeA2AErr(ctx, req.ID, models.A2AErrInternal, "inbound error: "+err.Error())
		return
	}
	if !ack.Accepted {
		writeA2AErr(ctx, req.ID, models.A2AErrInvalidParams, ack.Error)
		return
	}

	ctx.JSON(http.StatusOK, models.A2AResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: models.MessageSendResult{
			TaskID:    taskID,
			MessageID: ack.MessageId,
			Status:    "submitted",
		},
	})
}

// extractContent pulls plain text from an A2AMessage (parts or content field).
func extractContent(msg models.A2AMessage) string {
	if msg.Content != "" {
		return msg.Content
	}
	var sb strings.Builder
	for _, p := range msg.Parts {
		if p.Type == "text" || p.Type == "" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

func writeA2AErr(ctx *hypctx.Context, id interface{}, code int, message string) {
	ctx.JSON(http.StatusOK, models.A2AResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &models.A2AError{Code: code, Message: message},
	})
}

func newA2ATaskID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "task_" + hex.EncodeToString(b)
}
