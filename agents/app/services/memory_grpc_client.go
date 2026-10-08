// memory_grpc_client.go — gRPC client to the agent_memory microservice.
//
// Replaces the direct-DB *MemoryService for ContextBuilder/EpisodicWriter/
// memory_read/memory_write tools/IngestRunner, so those consumers now go
// through agent_memory's own gRPC API instead of duplicating its schema
// and querying the same Postgres tables directly.
//
// Implements the same method set as *MemoryService (the subset actually
// consumed elsewhere in this codebase), so it's a drop-in replacement.
package services

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"agent/app/models"
	"agent/pb/memorypb"
)

// MemoryGRPCClient talks to agent_memory's MemoryService over gRPC.
type MemoryGRPCClient struct {
	conn        *grpc.ClientConn
	client      memorypb.MemoryServiceClient
	callTimeout time.Duration
}

// NewMemoryGRPCClient dials agent_memory's gRPC server.
// TLS 行為由 GRPCClientTLS 決定：dev 用 InsecureSkipVerify（自簽），
// dev_pub_0.9 部署帶內部 client cert（CN=agents）+ 內部 CA 完整驗證。
func NewMemoryGRPCClient(addr string, tlsOpt GRPCClientTLS, dialTimeout, callTimeout time.Duration) (*MemoryGRPCClient, error) {
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	if callTimeout <= 0 {
		callTimeout = 5 * time.Second
	}

	creds, err := tlsOpt.Credentials()
	if err != nil {
		return nil, fmt.Errorf("memory grpc tls: %w", err)
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, addr, //nolint:staticcheck
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("memory grpc dial %s: %w", addr, err)
	}

	return &MemoryGRPCClient{
		conn:        conn,
		client:      memorypb.NewMemoryServiceClient(conn),
		callTimeout: callTimeout,
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *MemoryGRPCClient) Close() error {
	return c.conn.Close()
}

func (c *MemoryGRPCClient) GetPersonalByAgent(ctx context.Context, agentID string) (*models.PersonalMemory, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.GetPersonalByAgent(cctx, &memorypb.GetPersonalByAgentReq{AgentId: agentID})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("memory grpc GetPersonalByAgent: %w", err)
	}
	return personalFromProto(resp), nil
}

func (c *MemoryGRPCClient) UpsertPersonal(ctx context.Context, req models.CreatePersonalMemoryRequest) (*models.PersonalMemory, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	identity, err := structpb.NewStruct(orEmpty(req.Identity))
	if err != nil {
		return nil, fmt.Errorf("memory grpc UpsertPersonal: encode identity: %w", err)
	}
	exprDNA, err := structpb.NewStruct(orEmpty(req.ExpressionDNA))
	if err != nil {
		return nil, fmt.Errorf("memory grpc UpsertPersonal: encode expression_dna: %w", err)
	}
	resp, err := c.client.UpsertPersonal(cctx, &memorypb.UpsertPersonalReq{
		AgentId:       req.AgentID,
		Identity:      identity,
		ExpressionDna: exprDNA,
	})
	if err != nil {
		return nil, fmt.Errorf("memory grpc UpsertPersonal: %w", err)
	}
	return personalFromProto(resp), nil
}

func (c *MemoryGRPCClient) ListProceduralByAgent(ctx context.Context, agentID string, limit int) ([]models.ProceduralMemory, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.ListProceduralByAgent(cctx, &memorypb.ListProceduralByAgentReq{
		AgentId: agentID,
		Limit:   int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("memory grpc ListProceduralByAgent: %w", err)
	}
	out := make([]models.ProceduralMemory, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		out = append(out, *proceduralFromProto(it))
	}
	return out, nil
}

func (c *MemoryGRPCClient) UpsertProcedural(ctx context.Context, req models.CreateProceduralMemoryRequest) (*models.ProceduralMemory, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	heuristics, err := structpb.NewStruct(orEmpty(req.Heuristics))
	if err != nil {
		return nil, fmt.Errorf("memory grpc UpsertProcedural: encode heuristics: %w", err)
	}
	resp, err := c.client.UpsertProcedural(cctx, &memorypb.UpsertProceduralReq{
		AgentId:    req.AgentID,
		ModelName:  req.ModelName,
		Content:    req.Content,
		Heuristics: heuristics,
	})
	if err != nil {
		return nil, fmt.Errorf("memory grpc UpsertProcedural: %w", err)
	}
	return proceduralFromProto(resp), nil
}

func (c *MemoryGRPCClient) ListEpisodicByAgent(ctx context.Context, agentID string, limit int) ([]models.EpisodicMemory, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.ListEpisodicByAgent(cctx, &memorypb.ListEpisodicByAgentReq{
		AgentId: agentID,
		Limit:   int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("memory grpc ListEpisodicByAgent: %w", err)
	}
	out := make([]models.EpisodicMemory, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		out = append(out, *episodicFromProto(it))
	}
	return out, nil
}

// CreateEpisodic satisfies the EpisodicWriter interface (agent_grpc.go), which
// has no context parameter — best-effort, non-fatal on the caller's side.
func (c *MemoryGRPCClient) CreateEpisodic(req models.CreateEpisodicMemoryRequest) (*models.EpisodicMemory, error) {
	cctx, cancel := context.WithTimeout(context.Background(), c.callTimeout)
	defer cancel()
	turns, err := structpb.NewStruct(orEmpty(req.Turns))
	if err != nil {
		return nil, fmt.Errorf("memory grpc CreateEpisodic: encode turns: %w", err)
	}
	resp, err := c.client.CreateEpisodic(cctx, &memorypb.CreateEpisodicReq{
		AgentId:   req.AgentID,
		SessionId: req.SessionID,
		Turns:     turns,
		Summary:   req.Summary,
	})
	if err != nil {
		return nil, fmt.Errorf("memory grpc CreateEpisodic: %w", err)
	}
	return episodicFromProto(resp), nil
}

// ── Semantic memory (LongTermMemory) ─────────────────────────────────────────

// CreateSemantic persists a distilled fact. req.Embedding must already be
// computed (BGE-M3, 1024 dims) — this client does not own an embedding backend.
func (c *MemoryGRPCClient) CreateSemantic(ctx context.Context, req models.CreateLongTermMemoryRequest) (*models.LongTermMemory, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.CreateLongTerm(cctx, &memorypb.CreateLongTermReq{
		AgentId:    req.AgentID,
		Category:   req.Category,
		Source:     req.Source,
		Content:    req.Content,
		Summary:    req.Summary,
		Metadata:   req.Metadata,
		Embedding:  req.Embedding,
		Importance: req.Importance,
	})
	if err != nil {
		return nil, fmt.Errorf("memory grpc CreateSemantic: %w", err)
	}
	return longTermFromProto(resp), nil
}

// SearchSemantic finds the top-K most similar facts. req.QueryVector must
// already be computed (BGE-M3, 1024 dims).
func (c *MemoryGRPCClient) SearchSemantic(ctx context.Context, req models.SearchLongTermMemoryRequest) ([]models.LongTermMemoryHit, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.SearchLongTerm(cctx, &memorypb.SearchLongTermReq{
		AgentId:     req.AgentID,
		QueryVector: req.QueryVector,
		TopK:        int32(req.TopK),
		Category:    req.Category,
	})
	if err != nil {
		return nil, fmt.Errorf("memory grpc SearchSemantic: %w", err)
	}
	out := make([]models.LongTermMemoryHit, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		lt := longTermFromProto(it)
		out = append(out, models.LongTermMemoryHit{
			MemoryID: lt.MemoryID, AgentID: lt.AgentID, Category: lt.Category, Source: lt.Source,
			Content: lt.Content, Summary: lt.Summary, Metadata: lt.Metadata, Importance: lt.Importance,
		})
	}
	return out, nil
}

// ── Shared memory（跨 agent KV pool）───────────────────────────────────────────

func (c *MemoryGRPCClient) GetShared(ctx context.Context, poolID, key string) (*models.SharedMemory, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.GetShared(cctx, &memorypb.GetSharedReq{PoolId: poolID, Key: key})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("memory grpc GetShared: %w", err)
	}
	return sharedFromProto(resp), nil
}

func (c *MemoryGRPCClient) UpsertShared(ctx context.Context, req models.UpsertSharedMemoryRequest) (*models.SharedMemory, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	value, err := structpb.NewStruct(orEmpty(req.Value))
	if err != nil {
		return nil, fmt.Errorf("memory grpc UpsertShared: encode value: %w", err)
	}
	resp, err := c.client.UpsertShared(cctx, &memorypb.UpsertSharedReq{
		PoolId:    req.PoolID,
		Key:       req.Key,
		Value:     value,
		WrittenBy: req.WrittenBy,
	})
	if err != nil {
		return nil, fmt.Errorf("memory grpc UpsertShared: %w", err)
	}
	return sharedFromProto(resp), nil
}

func (c *MemoryGRPCClient) ListShared(ctx context.Context, poolID string, limit int) ([]models.SharedMemory, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.ListShared(cctx, &memorypb.ListSharedReq{PoolId: poolID, Limit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("memory grpc ListShared: %w", err)
	}
	out := make([]models.SharedMemory, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		out = append(out, *sharedFromProto(it))
	}
	return out, nil
}

// ── proto <-> model 轉換 ──────────────────────────────────────────────────────

func personalFromProto(p *memorypb.PersonalMemory) *models.PersonalMemory {
	return &models.PersonalMemory{
		ID:            p.GetId(),
		AgentID:       p.GetAgentId(),
		Identity:      p.GetIdentity().AsMap(),
		ExpressionDNA: p.GetExpressionDna().AsMap(),
		CreatedAt:     parseTime(p.GetCreatedAt()),
		UpdatedAt:     parseTime(p.GetUpdatedAt()),
	}
}

func proceduralFromProto(p *memorypb.ProceduralMemory) *models.ProceduralMemory {
	return &models.ProceduralMemory{
		ID:         p.GetId(),
		AgentID:    p.GetAgentId(),
		ModelName:  p.GetModelName(),
		Content:    p.GetContent(),
		Heuristics: p.GetHeuristics().AsMap(),
		CreatedAt:  parseTime(p.GetCreatedAt()),
		UpdatedAt:  parseTime(p.GetUpdatedAt()),
	}
}

func episodicFromProto(p *memorypb.EpisodicMemory) *models.EpisodicMemory {
	return &models.EpisodicMemory{
		ID:         p.GetId(),
		AgentID:    p.GetAgentId(),
		SessionID:  p.GetSessionId(),
		Turns:      p.GetTurns().AsMap(),
		Summary:    p.GetSummary(),
		OccurredAt: parseTime(p.GetOccurredAt()),
		CreatedAt:  parseTime(p.GetCreatedAt()),
	}
}

func longTermFromProto(p *memorypb.LongTermMemory) *models.LongTermMemory {
	return &models.LongTermMemory{
		MemoryID:   p.GetMemoryId(),
		AgentID:    p.GetAgentId(),
		Category:   p.GetCategory(),
		Source:     p.GetSource(),
		Content:    p.GetContent(),
		Summary:    p.GetSummary(),
		Metadata:   p.GetMetadata(),
		Importance: p.GetImportance(),
		CreatedAt:  parseTime(p.GetCreatedAt()),
	}
}

func sharedFromProto(p *memorypb.SharedMemory) *models.SharedMemory {
	return &models.SharedMemory{
		PoolID:    p.GetPoolId(),
		Key:       p.GetKey(),
		Value:     p.GetValue().AsMap(),
		WrittenBy: p.GetWrittenBy(),
		CreatedAt: parseTime(p.GetCreatedAt()),
		UpdatedAt: parseTime(p.GetUpdatedAt()),
	}
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func orEmpty(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return map[string]interface{}{}
	}
	return m
}
