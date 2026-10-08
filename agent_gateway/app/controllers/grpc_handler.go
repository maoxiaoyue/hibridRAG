package controllers

import (
	"context"
	"fmt"
	"log"

	"agent_gatway/app/models"
	"agent_gatway/app/services"
)

// GRPCGatewayServer implements models.AgentServiceServer.
// It receives InboundReq from upstream callers, resolves the downstream
// agent via the registry, and forwards the request via gRPC.
type GRPCGatewayServer struct {
	models.UnimplementedAgentServiceServer
	registry  *services.RegistryClient
	forwarder *services.GRPCForwarder
}

func NewGRPCGatewayServer(registry *services.RegistryClient, forwarder *services.GRPCForwarder) *GRPCGatewayServer {
	return &GRPCGatewayServer{
		registry:  registry,
		forwarder: forwarder,
	}
}

// Inbound is the gRPC entry point for upstream callers.
//
// Routing priority:
//  1. payload.agent_id → GET /registry/agents/:id
//  2. If not found by ID, search by skill using payload.agent_id as hint
func (s *GRPCGatewayServer) Inbound(ctx context.Context, req *models.InboundReq) (*models.InboundAck, error) {
	if req == nil || req.Payload == nil {
		return errorAck("", 400, "nil payload"), nil
	}

	traceID := req.TraceId
	agentID := req.Payload.AgentId

	if agentID == "" {
		return errorAck(traceID, 400, "missing agent_id in payload"), nil
	}
	if len(req.Payload.Messages) == 0 {
		return errorAck(traceID, 400, "empty messages"), nil
	}

	// ── 1. Resolve downstream agent ──────────────────────────────────────────
	endpoint, err := s.resolveGRPCEndpoint(ctx, agentID)
	if err != nil {
		log.Printf("[grpc-gw][%s] resolve error for agent=%s: %v", traceID, agentID, err)
		return errorAck(traceID, 502, fmt.Sprintf("agent not reachable: %v", err)), nil
	}
	if endpoint == "" {
		log.Printf("[grpc-gw][%s] no gRPC endpoint for agent=%s", traceID, agentID)
		return errorAck(traceID, 502, fmt.Sprintf("agent %s has no gRPC endpoint registered", agentID)), nil
	}

	// ── 2. Forward ───────────────────────────────────────────────────────────
	log.Printf("[grpc-gw][%s] forwarding to agent=%s endpoint=%s", traceID, agentID, endpoint)
	ack, err := s.forwarder.Forward(ctx, endpoint, req)
	if err != nil {
		log.Printf("[grpc-gw][%s] forward error: %v", traceID, err)
		return errorAck(traceID, 502, fmt.Sprintf("downstream error: %v", err)), nil
	}

	log.Printf("[grpc-gw][%s] forwarded: accepted=%v code=%d", traceID, ack.Accepted, ack.Code)
	return ack, nil
}

// resolveGRPCEndpoint looks up the agent's gRPC endpoint in the registry.
// It first tries by agent ID, then falls back to skill search.
func (s *GRPCGatewayServer) resolveGRPCEndpoint(ctx context.Context, agentID string) (string, error) {
	agent, err := s.registry.GetAgent(ctx, agentID)
	if err != nil {
		return "", fmt.Errorf("registry lookup: %w", err)
	}
	if agent != nil && agent.GRPCEndpoint != "" {
		return agent.GRPCEndpoint, nil
	}

	// Fallback: search by skill hint using agentID as keyword
	if agent == nil {
		found, err := s.registry.SearchBySkill(ctx, agentID)
		if err != nil {
			return "", fmt.Errorf("registry search: %w", err)
		}
		if found != nil {
			return found.GRPCEndpoint, nil
		}
	}

	return "", nil
}

func errorAck(traceID string, code int32, msg string) *models.InboundAck {
	return &models.InboundAck{
		TraceId:  traceID,
		Accepted: false,
		Code:     code,
		Error:    msg,
	}
}
