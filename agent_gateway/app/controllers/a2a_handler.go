package controllers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/maoxiaoyue/hypgo/pkg/context"

	appconfig "agent_gatway/app/config"
	"agent_gatway/app/models"
	"agent_gatway/app/services"
)

var (
	once       sync.Once
	agentCard  models.AgentCard
	regClient  *services.RegistryClient
	a2aClient  *services.A2AClient
)

// Init wires the controller dependencies; called once from main
func Init(cfg *appconfig.Config) {
	once.Do(func() {
		agentCard = services.BuildAgentCard(cfg)
		regClient = services.NewRegistryClient(cfg.Registry.URL)
		a2aClient = services.NewA2AClient(cfg.Gateway.DownstreamTimeoutSec, services.A2AClientOptions{
			InsecureSkipVerify: cfg.A2AClient.InsecureSkipVerify,
			CAFile:             cfg.A2AClient.CAFile,
		})
	})
}

// AgentCardHandler GET /.well-known/agent-card.json
func AgentCardHandler(ctx *context.Context) {
	ctx.JSON(http.StatusOK, agentCard)
}

// A2ADispatcher POST /
// Accepts JSON-RPC 2.0, routes to downstream agent via registry
func A2ADispatcher(ctx *context.Context) {
	var req models.JSONRPCRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		writeRPCError(ctx, nil, models.ErrCodeParseError, "parse error: "+err.Error())
		return
	}

	if req.JSONRPC != "2.0" {
		writeRPCError(ctx, req.ID, models.ErrCodeInvalidRequest, "jsonrpc must be \"2.0\"")
		return
	}

	switch req.Method {
	case "message/send":
		handleMessageSend(ctx, &req)
	case "tasks/get", "tasks/cancel":
		writeRPCError(ctx, req.ID, models.ErrCodeMethodNotFound, req.Method+" not supported at gateway level")
	default:
		writeRPCError(ctx, req.ID, models.ErrCodeMethodNotFound, "unknown method: "+req.Method)
	}
}

func handleMessageSend(ctx *context.Context, req *models.JSONRPCRequest) {
	// Decode params to extract skill hint
	var params models.MessageSendParams
	if req.Params != nil {
		raw, _ := json.Marshal(req.Params)
		_ = json.Unmarshal(raw, &params)
	}

	// Discover agent via registry
	agent, err := regClient.SearchBySkill(ctx.Request.Context(), params.SkillHint)
	if err != nil {
		writeRPCError(ctx, req.ID, models.ErrCodeInternal, "registry error: "+err.Error())
		return
	}
	if agent == nil {
		msg := "no agent registered"
		if params.SkillHint != "" {
			msg = "no agent found for skill: " + params.SkillHint
		}
		writeRPCError(ctx, req.ID, models.ErrCodeNoAgent, msg)
		return
	}

	// Inject task_id if not set
	if params.TaskID == "" {
		params.TaskID = newTaskID()
		raw, _ := json.Marshal(params)
		var p interface{}
		_ = json.Unmarshal(raw, &p)
		req.Params = p
	}

	// Forward to downstream agent
	agentURL := agent.URL
	if agentURL == "" {
		writeRPCError(ctx, req.ID, models.ErrCodeNoAgent, "agent has no URL")
		return
	}

	resp, err := a2aClient.Forward(ctx.Request.Context(), agentURL, req)
	if err != nil {
		writeRPCError(ctx, req.ID, models.ErrCodeAgentError, "agent error: "+err.Error())
		return
	}

	ctx.JSON(http.StatusOK, resp)
}

func writeRPCError(ctx *context.Context, id interface{}, code int, message string) {
	ctx.JSON(http.StatusOK, models.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &models.JSONRPCError{
			Code:    code,
			Message: message,
		},
	})
}

func newTaskID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "task_" + hex.EncodeToString(b)
}
