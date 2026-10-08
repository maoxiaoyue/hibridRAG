// kuzu_schema.go — one-shot DDL bootstrapper for the Kùzu-backed graph.
//
// Kùzu is schema-first: every node/rel label must exist as a typed table
// before you can MERGE into it. This mirrors what EnsureAGESchema does for
// AGE (which needs `LOAD 'age'` + `create_graph`) — same purpose, different
// mechanics.
package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/kuzudb/go-kuzu"
)

// EnsureKuzuSchema creates the Entity node table, the RELATES_TO rel table,
// and the EntityEmbedding node table if they don't already exist. Safe to
// run on every startup.
//
// embeddingDim sets the FLOAT[N] column width on EntityEmbedding; the vector
// index (created separately by EnsureKuzuVectorSchema) uses the same width
// implicitly. Passing 0 falls back to 1024 (BGE-M3, the only model here).
//
// Why a separate embedding table? Kùzu forbids SET on any property that
// participates in a vector index ("Cannot set property … because it is used
// in one or more indexes"). Keeping the vector on its own node lets us
// re-embed by DELETE+CREATE without ever touching the graph structure —
// same split pgvector used with entity_embeddings.
func EnsureKuzuSchema(ctx context.Context, c *KuzuClient, embeddingDim int) error {
	if embeddingDim <= 0 {
		embeddingDim = 1024
	}
	if !c.enabled {
		return ErrGraphDisabled
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	conn, err := kuzu.OpenConnection(c.db)
	if err != nil {
		return fmt.Errorf("open connection: %w", err)
	}
	defer conn.Close()

	stmts := []string{
		`CREATE NODE TABLE IF NOT EXISTS Entity(
			name STRING,
			category STRING,
			description STRING,
			source_file STRING,
			updated_at TIMESTAMP,
			PRIMARY KEY(name)
		)`,
		`CREATE REL TABLE IF NOT EXISTS RELATES_TO(
			FROM Entity TO Entity,
			type STRING
		)`,
		fmt.Sprintf(`CREATE NODE TABLE IF NOT EXISTS EntityEmbedding(
			name STRING,
			embedding FLOAT[%d],
			updated_at TIMESTAMP,
			PRIMARY KEY(name)
		)`, embeddingDim),
	}
	for _, s := range stmts {
		result, err := conn.Query(s)
		if err != nil {
			return fmt.Errorf("kuzu schema: %s: %w", firstLine(s), err)
		}
		result.Close()
	}
	return nil
}

// EnsureKuzuVectorSchema installs the vector extension and creates the HNSW
// index on Entity.embedding. Idempotent: swallows "already installed" and
// "already exists" so it's safe to run at every startup. Must be called
// AFTER EnsureKuzuSchema (the index needs the column to exist).
//
// The index dimension is derived from the column type declared by
// EnsureKuzuSchema — nothing to configure here.
func EnsureKuzuVectorSchema(ctx context.Context, c *KuzuClient, indexName string) error {
	if !c.enabled {
		return ErrVectorDisabled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := kuzu.OpenConnection(c.db)
	if err != nil {
		return fmt.Errorf("open connection: %w", err)
	}
	defer conn.Close()

	// INSTALL is a no-op if the extension is already downloaded; LOAD is a
	// no-op if it's already loaded in this session. Both intentionally treated
	// as best-effort — the CREATE_VECTOR_INDEX below will surface any real
	// failure with a clearer message.
	for _, s := range []string{`INSTALL vector`, `LOAD vector`} {
		if r, err := conn.Query(s); err == nil {
			r.Close()
		}
	}

	createIdx := fmt.Sprintf(
		`CALL CREATE_VECTOR_INDEX('EntityEmbedding', '%s', 'embedding')`, indexName)
	r, err := conn.Query(createIdx)
	if err != nil {
		// Kùzu returns a generic error message with "already exists"
		// substring when the index name is in use; treat as success.
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return fmt.Errorf("create vector index: %w", err)
	}
	r.Close()
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
