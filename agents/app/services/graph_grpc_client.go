// graph_grpc_client.go — gRPC client to the agent_graph microservice
// (A4 Knowledge Agent — Apache AGE knowledge graph + Obsidian sync).
//
// Same shape as memory_grpc_client.go / critic_grpc_client.go: dial once at
// startup, one method per RPC used by the graph_query tool. ExecuteCypher is
// intentionally NOT wrapped here — it's an admin/debug escape hatch on
// agent_graph's own REST/gRPC surface, not something to hand an LLM given a
// prompt-injected note could get it to run arbitrary graph-mutating Cypher.
package services

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	"agent/pb/graphpb"
)

// GraphEntity / GraphRelation / GraphContext trim the generated pb types down
// to what tool_builtin.go needs — decouples callers from the pb package.
type GraphEntity struct {
	Name        string  `json:"name"`
	Category    string  `json:"category,omitempty"`
	Description string  `json:"description,omitempty"`
	SourceFile  string  `json:"source_file,omitempty"`
	Score       float64 `json:"score,omitempty"`
}

type GraphRelation struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

type GraphContext struct {
	Center    GraphEntity     `json:"center"`
	Neighbors []GraphEntity   `json:"neighbors"`
	Relations []GraphRelation `json:"relations"`
}

type GraphHybridResult struct {
	Seeds   []GraphEntity  `json:"seeds"`
	Context []GraphContext `json:"context"`
}

// GraphGRPCClient talks to agent_graph's GraphService over gRPC.
type GraphGRPCClient struct {
	conn        *grpc.ClientConn
	client      graphpb.GraphServiceClient
	callTimeout time.Duration
}

// NewGraphGRPCClient dials agent_graph's gRPC server.
// TLS 行為由 GRPCClientTLS 決定（同 memory_grpc_client.go）。
func NewGraphGRPCClient(addr string, tlsOpt GRPCClientTLS, dialTimeout, callTimeout time.Duration) (*GraphGRPCClient, error) {
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	if callTimeout <= 0 {
		callTimeout = 5 * time.Second
	}

	creds, err := tlsOpt.Credentials()
	if err != nil {
		return nil, fmt.Errorf("graph grpc tls: %w", err)
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, addr, //nolint:staticcheck
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("graph grpc dial %s: %w", addr, err)
	}

	return &GraphGRPCClient{
		conn:        conn,
		client:      graphpb.NewGraphServiceClient(conn),
		callTimeout: callTimeout,
	}, nil
}

func (c *GraphGRPCClient) Close() error { return c.conn.Close() }

func entityFromProto(e *graphpb.Entity) GraphEntity {
	if e == nil {
		return GraphEntity{}
	}
	return GraphEntity{
		Name: e.GetName(), Category: e.GetCategory(), Description: e.GetDescription(),
		SourceFile: e.GetSourceFile(), Score: e.GetScore(),
	}
}

func relationFromProto(r *graphpb.Relation) GraphRelation {
	if r == nil {
		return GraphRelation{}
	}
	return GraphRelation{Source: r.GetSource(), Target: r.GetTarget(), Type: r.GetType()}
}

func contextFromProto(resp *graphpb.LocalContextResp) GraphContext {
	neighbors := make([]GraphEntity, 0, len(resp.GetNeighbors()))
	for _, n := range resp.GetNeighbors() {
		neighbors = append(neighbors, entityFromProto(n))
	}
	relations := make([]GraphRelation, 0, len(resp.GetRelations()))
	for _, r := range resp.GetRelations() {
		relations = append(relations, relationFromProto(r))
	}
	return GraphContext{Center: entityFromProto(resp.GetCenter()), Neighbors: neighbors, Relations: relations}
}

// SearchEntities substring-matches entity name/description.
func (c *GraphGRPCClient) SearchEntities(ctx context.Context, query string, limit int) ([]GraphEntity, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.SearchEntities(cctx, &graphpb.SearchEntitiesReq{Query: query, Limit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("graph grpc SearchEntities: %w", err)
	}
	out := make([]GraphEntity, 0, len(resp.GetEntities()))
	for _, e := range resp.GetEntities() {
		out = append(out, entityFromProto(e))
	}
	return out, nil
}

// GetLocalContext returns an entity's N-hop neighborhood + direct relations.
func (c *GraphGRPCClient) GetLocalContext(ctx context.Context, entityName string, hops, limit int) (GraphContext, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.GetLocalContext(cctx, &graphpb.LocalContextReq{
		EntityName: entityName, Hops: int32(hops), Limit: int32(limit),
	})
	if err != nil {
		return GraphContext{}, fmt.Errorf("graph grpc GetLocalContext: %w", err)
	}
	return contextFromProto(resp), nil
}

// HybridSearch embeds query, finds seed entities by vector similarity, and
// expands each into its local graph neighborhood.
func (c *GraphGRPCClient) HybridSearch(ctx context.Context, query string, topK, hops int) (GraphHybridResult, error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.HybridSearch(cctx, &graphpb.HybridSearchReq{
		Query: query, TopK: int32(topK), Hops: int32(hops),
	})
	if err != nil {
		return GraphHybridResult{}, fmt.Errorf("graph grpc HybridSearch: %w", err)
	}
	seeds := make([]GraphEntity, 0, len(resp.GetSeeds()))
	for _, e := range resp.GetSeeds() {
		seeds = append(seeds, entityFromProto(e))
	}
	ctxs := make([]GraphContext, 0, len(resp.GetContext()))
	for _, ctxResp := range resp.GetContext() {
		ctxs = append(ctxs, contextFromProto(ctxResp))
	}
	return GraphHybridResult{Seeds: seeds, Context: ctxs}, nil
}

// ProposeRelation submits a low-confidence relation candidate for human
// review — never merges into the graph directly (see agent_graph's sync
// worker for the same confidence-threshold rule applied during vault sync).
func (c *GraphGRPCClient) ProposeRelation(ctx context.Context, source, target, relType string, confidence float64, sourceFile, extractedBy string) (id, status string, err error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.ProposeRelation(cctx, &graphpb.ProposeRelationReq{
		SourceEntity: source, TargetEntity: target, RelationType: relType,
		Confidence: confidence, SourceFile: sourceFile, ExtractedBy: extractedBy,
	})
	if err != nil {
		return "", "", fmt.Errorf("graph grpc ProposeRelation: %w", err)
	}
	return resp.GetId(), resp.GetStatus(), nil
}

// AppendTemp 把一段文字寫進 agent_graph 的 vault/temp/（知識沉澱管線的暫存入口）。
// 對話結束後 best-effort 呼叫，讓問答沉澱進知識庫、之後被分類 worker 攝取進圖譜。
func (c *GraphGRPCClient) AppendTemp(ctx context.Context, title, content, source, prefix string) (path string, err error) {
	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.client.AppendTemp(cctx, &graphpb.AppendTempReq{
		Title: title, Content: content, Source: source, Prefix: prefix,
	})
	if err != nil {
		return "", fmt.Errorf("graph grpc AppendTemp: %w", err)
	}
	return resp.GetPath(), nil
}
