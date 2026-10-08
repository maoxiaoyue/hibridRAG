// graph_backend.go — 圖譜後端契約。歷史上曾有 AGE-on-Postgres 與 embedded Kùzu
// 兩條實作（介面即為此而生）；AGE/pgvector 路徑已於 2026-07-08 移除——該 PG 從未真正
// 裝過 AGE，圖資料只存在於 Kùzu。現在唯一實作是 KuzuClient + KuzuVectorStore；
// 介面保留，GraphFacade 與 sync worker 都只認契約，未來要換後端仍是一行接線的事。
package services

import (
	"context"

	"agent_graph/app/models"
)

// GraphBackend is the property-graph store contract: entity/relation
// upsert + search + N-hop traversal + full dump (vault 鏡像/degree 計算用)
// + a Cypher escape hatch.
type GraphBackend interface {
	Enabled() bool
	UpsertEntity(ctx context.Context, e models.EntityResult) error
	UpsertRelation(ctx context.Context, source, target, relType string) error
	SearchEntities(ctx context.Context, query string, limit int) ([]models.EntityResult, error)
	GetLocalContext(ctx context.Context, name string, hops, limit int) (*models.LocalContextResponse, error)
	ListEntities(ctx context.Context) ([]models.EntityResult, error)
	ListRelations(ctx context.Context) ([]models.RelationResult, error)
	ExecuteCypher(ctx context.Context, cypher string, params map[string]any, cols []string) ([]map[string]any, error)
}

// VectorBackend is the entity-embedding store contract: name-keyed
// upsert + ANN nearest-neighbour lookup.
type VectorBackend interface {
	Enabled() bool
	Upsert(ctx context.Context, entityName string, embedding []float32) error
	SearchSimilar(ctx context.Context, embedding []float32, topK int) ([]VectorHit, error)
}

// VectorHit one ANN search result: entity name + cosine similarity (1 = identical).
type VectorHit struct {
	EntityName string
	Score      float64
}

// Compile-time assertions: every concrete backend must satisfy the interface.
var (
	_ GraphBackend  = (*KuzuClient)(nil)
	_ VectorBackend = (*KuzuVectorStore)(nil)
)
