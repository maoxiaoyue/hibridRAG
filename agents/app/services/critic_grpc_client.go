// critic_grpc_client.go — gRPC client to the agent_critic microservice
// (A3 Critic / Gatekeeper — answer quality evaluation).
//
// Same shape as memory_grpc_client.go: dial once at startup, one method per
// RPC, gRPC status codes translated to sentinel errors where relevant.
package services

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	"agent/pb/criticpb"
)

// CriticEvaluation is the trimmed result consumers of this package need —
// decouples callers (controllers package) from the generated pb type.
type CriticEvaluation struct {
	ID           string
	Completeness float64
	Grounding    float64
	Faithfulness float64
	Overall      float64
	Passed       bool
	Feedback     []string
}

// CriticGRPCClient talks to agent_critic's CriticService over gRPC.
type CriticGRPCClient struct {
	conn        *grpc.ClientConn
	client      criticpb.CriticServiceClient
	callTimeout time.Duration
}

// NewCriticGRPCClient dials agent_critic's gRPC server.
// TLS 行為由 GRPCClientTLS 決定（同 memory_grpc_client.go）。
func NewCriticGRPCClient(addr string, tlsOpt GRPCClientTLS, dialTimeout, callTimeout time.Duration) (*CriticGRPCClient, error) {
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	if callTimeout <= 0 {
		callTimeout = 5 * time.Second
	}

	creds, err := tlsOpt.Credentials()
	if err != nil {
		return nil, fmt.Errorf("critic grpc tls: %w", err)
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, addr, //nolint:staticcheck
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("critic grpc dial %s: %w", addr, err)
	}

	return &CriticGRPCClient{
		conn:        conn,
		client:      criticpb.NewCriticServiceClient(conn),
		callTimeout: callTimeout,
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *CriticGRPCClient) Close() error {
	return c.conn.Close()
}

// Evaluate scores an answer's quality against question + sources.
// revision is the caller's revision counter (0 = first attempt).
func (c *CriticGRPCClient) Evaluate(
	ctx context.Context,
	agentID, sessionID, question, answer string,
	sources []string,
	revision int,
) (*CriticEvaluation, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.Evaluate(cctx, &criticpb.EvaluateReq{
		AgentId:   agentID,
		SessionId: sessionID,
		Question:  question,
		Answer:    answer,
		Sources:   sources,
		Revision:  int32(revision),
	})
	if err != nil {
		return nil, fmt.Errorf("critic grpc Evaluate: %w", err)
	}
	return evaluationFromProto(resp), nil
}

func evaluationFromProto(e *criticpb.Evaluation) *CriticEvaluation {
	return &CriticEvaluation{
		ID:           e.GetId(),
		Completeness: e.GetCompleteness(),
		Grounding:    e.GetGrounding(),
		Faithfulness: e.GetFaithfulness(),
		Overall:      e.GetOverall(),
		Passed:       e.GetPassed(),
		Feedback:     e.GetFeedback(),
	}
}
