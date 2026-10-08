// kuzu_vector_store.go — entity embedding storage and ANN search using
// Kùzu's built-in HNSW vector index, replacing pgvector when the Kùzu backend
// is selected.
//
// Embeddings live on a *separate* EntityEmbedding node table (keyed by the
// same name column as Entity), not on Entity itself. Kùzu forbids SET on a
// column that participates in a vector index, so re-embedding an entity has
// to be delete-and-reinsert. Keeping the vector on a separate node lets us
// do that without ever touching the graph structure or its relations.
//
// Cypher-level parameterisation of vector literals isn't supported by
// Kùzu v0.11 (only scalars/strings bind through Prepare/Execute), so this
// module builds the vector literal as a bounded numeric string and
// interpolates it into the query text. Safe because every element is a
// `%g`-formatted float — no user-controlled string ever enters the query.
package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kuzudb/go-kuzu"
)

// KuzuVectorStore backs VectorBackend with the vector index over Entity.embedding.
// enabled=false makes every operation return ErrVectorDisabled.
type KuzuVectorStore struct {
	client    *KuzuClient // reuses the shared *kuzu.Database
	indexName string      // matches the one created by EnsureKuzuVectorSchema
	enabled   bool
}

// NewKuzuVectorStore wraps a KuzuClient. Call EnsureKuzuVectorSchema separately
// to install the extension + create the HNSW index before use.
func NewKuzuVectorStore(client *KuzuClient, enabled bool) *KuzuVectorStore {
	return &KuzuVectorStore{
		client:    client,
		indexName: "entity_embedding_idx",
		enabled:   enabled,
	}
}

func (s *KuzuVectorStore) Enabled() bool { return s.enabled }

// vecLiteral renders "[0.1,0.2,...]" — the Kùzu literal form for FLOAT[N].
// Uses -1 precision so single-precision floats round-trip exactly.
func vecLiteral(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Upsert stores or replaces the embedding for one entity name. Kùzu forbids
// SET on the indexed vector column, so this is DELETE-then-CREATE against
// the EntityEmbedding node table — atomic for the caller, and safe because
// EntityEmbedding has no relations to detach.
func (s *KuzuVectorStore) Upsert(ctx context.Context, entityName string, embedding []float32) error {
	if !s.enabled {
		return ErrVectorDisabled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := kuzu.OpenConnection(s.client.db)
	if err != nil {
		return fmt.Errorf("open connection: %w", err)
	}
	defer conn.Close()

	// 1) delete any existing embedding row for this name (no-op if absent)
	delStmt, err := conn.Prepare(`MATCH (e:EntityEmbedding {name: $name}) DELETE e`)
	if err != nil {
		return fmt.Errorf("prepare delete embedding: %w", err)
	}
	defer delStmt.Close()
	if r, err := conn.Execute(delStmt, map[string]any{"name": entityName}); err != nil {
		return fmt.Errorf("delete embedding: %w", err)
	} else {
		r.Close()
	}

	// 2) create the fresh row. Vector literal is inlined (safe: all-numeric).
	// updated_at is bound as a Go time.Time so we don't depend on Kùzu's
	// timestamp function signature (which varies across versions).
	createCypher := fmt.Sprintf(`
		CREATE (e:EntityEmbedding {
			name: $name,
			embedding: CAST(%s, 'FLOAT[%d]'),
			updated_at: $updated_at
		})
	`, vecLiteral(embedding), len(embedding))
	createStmt, err := conn.Prepare(createCypher)
	if err != nil {
		return fmt.Errorf("prepare create embedding: %w", err)
	}
	defer createStmt.Close()
	if r, err := conn.Execute(createStmt, map[string]any{
		"name":       entityName,
		"updated_at": time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("create embedding: %w", err)
	} else {
		r.Close()
	}
	return nil
}

// SearchSimilar returns the topK entities closest (cosine) to the query
// vector. Score = 1 - distance so 1.0 = identical (same convention as the
// pgvector-backed VectorStore).
func (s *KuzuVectorStore) SearchSimilar(ctx context.Context, embedding []float32, topK int) ([]VectorHit, error) {
	if !s.enabled {
		return nil, ErrVectorDisabled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if topK <= 0 {
		topK = 5
	}

	conn, err := kuzu.OpenConnection(s.client.db)
	if err != nil {
		return nil, fmt.Errorf("open connection: %w", err)
	}
	defer conn.Close()

	// QUERY_VECTOR_INDEX yields (node, distance). Vector literal inlined for
	// the same reason as Upsert. node here is an EntityEmbedding — its `name`
	// column joins back to Entity.name if the caller needs full node data.
	cypher := fmt.Sprintf(`
		CALL QUERY_VECTOR_INDEX('EntityEmbedding', '%s',
			CAST(%s, 'FLOAT[%d]'), %d)
		RETURN node.name, distance
		ORDER BY distance
	`, s.indexName, vecLiteral(embedding), len(embedding), topK)

	result, err := conn.Query(cypher)
	if err != nil {
		return nil, fmt.Errorf("vector index query: %w", err)
	}
	defer result.Close()

	var out []VectorHit
	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("next tuple: %w", err)
		}
		nameVal, err := tuple.GetValue(0)
		if err != nil {
			tuple.Close()
			return nil, fmt.Errorf("get name: %w", err)
		}
		distVal, err := tuple.GetValue(1)
		if err != nil {
			tuple.Close()
			return nil, fmt.Errorf("get distance: %w", err)
		}
		tuple.Close()

		name, _ := nameVal.(string)
		dist := toFloat64(distVal)
		out = append(out, VectorHit{
			EntityName: name,
			Score:      1 - dist, // cosine similarity
		})
	}
	return out, nil
}

// toFloat64 collapses Kùzu's numeric result variants (float32/float64/int).
func toFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	default:
		return 0
	}
}
