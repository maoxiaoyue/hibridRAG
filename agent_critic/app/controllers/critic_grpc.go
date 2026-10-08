// critic_grpc.go — gRPC 版 CriticService，供 agents 服務（或 orchestrator）
// 在研究產出答案後呼叫 Evaluate，依 passed / feedback 決定是否進入 revision loop。
package controllers

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"agent_critic/app/models"
	"agent_critic/app/services"
	"agent_critic/pb/criticpb"
)

// CriticGRPCServer 實作 criticpb.CriticServiceServer
type CriticGRPCServer struct {
	criticpb.UnimplementedCriticServiceServer
	svc *services.CriticService
}

func NewCriticGRPCServer(svc *services.CriticService) *CriticGRPCServer {
	return &CriticGRPCServer{svc: svc}
}

func (s *CriticGRPCServer) Evaluate(ctx context.Context, req *criticpb.EvaluateReq) (*criticpb.Evaluation, error) {
	if req.GetAgentId() == "" || req.GetQuestion() == "" || req.GetAnswer() == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id, question, answer are required")
	}
	m, err := s.svc.Evaluate(ctx, models.EvaluateRequest{
		AgentID:   req.GetAgentId(),
		SessionID: req.GetSessionId(),
		Question:  req.GetQuestion(),
		Answer:    req.GetAnswer(),
		Sources:   req.GetSources(),
		Revision:  int(req.GetRevision()),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return evaluationToProto(m), nil
}

func (s *CriticGRPCServer) GetEvaluation(ctx context.Context, req *criticpb.GetEvaluationReq) (*criticpb.Evaluation, error) {
	m, err := s.svc.Get(ctx, req.GetId())
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "evaluation not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return evaluationToProto(m), nil
}

func (s *CriticGRPCServer) ListBySession(ctx context.Context, req *criticpb.ListBySessionReq) (*criticpb.ListBySessionResp, error) {
	if req.GetSessionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	list, err := s.svc.ListBySession(ctx, req.GetSessionId(), int(req.GetLimit()))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	items := make([]*criticpb.Evaluation, 0, len(list))
	for i := range list {
		items = append(items, evaluationToProto(&list[i]))
	}
	return &criticpb.ListBySessionResp{Items: items}, nil
}

func evaluationToProto(m *models.CriticEvaluation) *criticpb.Evaluation {
	return &criticpb.Evaluation{
		Id:           m.ID,
		AgentId:      m.AgentID,
		SessionId:    m.SessionID,
		Revision:     int32(m.Revision),
		Completeness: m.Completeness,
		Grounding:    m.Grounding,
		Faithfulness: m.Faithfulness,
		Overall:      m.Overall,
		Passed:       m.Passed,
		Feedback:     m.Feedback,
		CreatedAt:    m.CreatedAt.Format(time.RFC3339),
	}
}
