// graph_client.go — 對 agent_graph 的 gRPC client（本服務唯一的下游）。
// 涵蓋人工輸入需要的四個操作：SearchEntities（重複偵測）、UpsertEntity、
// UpsertRelation、IngestText（PG 版本文件輸入）。
package services

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	"graphic_input/internal/mtls"
	"graphic_input/pb/graphpb"
)

// GraphTLS agent_graph 連線的 TLS 選項。
type GraphTLS struct {
	InsecureSkipVerify bool
	ClientCertFile     string
	ClientKeyFile      string
	CAFile             string
}

// GraphClient agent_graph 的 gRPC client。
type GraphClient struct {
	conn        *grpc.ClientConn
	client      graphpb.GraphServiceClient
	callTimeout time.Duration
}

// NewGraphClient 撥號 agent_graph（阻塞式，本服務對它是硬依賴——連不上直接失敗）。
// TLS：ca_file 非空用內部 CA 完整驗證；否則 InsecureSkipVerify（dev 自簽，
// 語意同 mtls.ClientCredentials）。
func NewGraphClient(addr string, tlsOpt GraphTLS, dialTimeout, callTimeout time.Duration) (*GraphClient, error) {
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	if callTimeout <= 0 {
		callTimeout = 120 * time.Second
	}

	tc, terr := mtls.ClientCredentials(tlsOpt.ClientCertFile, tlsOpt.ClientKeyFile, tlsOpt.CAFile)
	if terr != nil {
		return nil, fmt.Errorf("graph tls: %w", terr)
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	conn, derr := grpc.DialContext(dialCtx, addr, //nolint:staticcheck
		grpc.WithTransportCredentials(tc),
		grpc.WithBlock(),
	)
	if derr != nil {
		return nil, fmt.Errorf("graph grpc dial %s: %w", addr, derr)
	}
	return &GraphClient{
		conn:        conn,
		client:      graphpb.NewGraphServiceClient(conn),
		callTimeout: callTimeout,
	}, nil
}

func (c *GraphClient) Close() error { return c.conn.Close() }

func (c *GraphClient) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, c.callTimeout)
}

// SearchEntities 重複偵測用（substring match）。
func (c *GraphClient) SearchEntities(parent context.Context, query string, limit int) ([]SimilarEntity, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	resp, err := c.client.SearchEntities(ctx, &graphpb.SearchEntitiesReq{Query: query, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]SimilarEntity, 0, len(resp.GetEntities()))
	for _, e := range resp.GetEntities() {
		out = append(out, SimilarEntity{
			Name: e.GetName(), Category: e.GetCategory(), Description: e.GetDescription(),
		})
	}
	return out, nil
}

// SimilarEntity 重複偵測回傳的精簡實體。
type SimilarEntity struct {
	Name        string
	Category    string
	Description string
}

// UpsertEntity 直接建立/更新實體；回傳 embedding 是否同步成功。
func (c *GraphClient) UpsertEntity(parent context.Context, name, category, description, sourceFile string) (bool, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	resp, err := c.client.UpsertEntity(ctx, &graphpb.UpsertEntityWriteReq{
		Entity: &graphpb.Entity{Name: name, Category: category, Description: description, SourceFile: sourceFile},
	})
	if err != nil {
		return false, err
	}
	return resp.GetEmbeddingSynced(), nil
}

// UpsertRelation 直接 MERGE 一條關係。
func (c *GraphClient) UpsertRelation(parent context.Context, source, target, relType string) error {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	_, err := c.client.UpsertRelation(ctx, &graphpb.UpsertRelationWriteReq{
		Relation: &graphpb.Relation{Source: source, Target: target, Type: relType},
	})
	return err
}

// IngestStats IngestText 的抽取統計。
type IngestStats struct {
	Entities          int
	RelationsMerged   int
	RelationsProposed int
	Embeddings        int
}

// IngestText PG 版本文件輸入：LLM 抽取 + embedding 直寫。
func (c *GraphClient) IngestText(parent context.Context, source, text, actor string) (IngestStats, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	resp, err := c.client.IngestText(ctx, &graphpb.IngestTextReq{Source: source, Text: text, Actor: actor})
	if err != nil {
		return IngestStats{}, err
	}
	return IngestStats{
		Entities:          int(resp.GetEntities()),
		RelationsMerged:   int(resp.GetRelationsMerged()),
		RelationsProposed: int(resp.GetRelationsProposed()),
		Embeddings:        int(resp.GetEmbeddings()),
	}, nil
}
