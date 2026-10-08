// Package services — vectordb.go: Cassandra dense vector storage.
package services

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
)

// DenseChunk one chunk's dense vector record.
type DenseChunk struct {
	ChunkID   string
	AgentID   string
	DocID     string
	Text      string
	Model     string
	Embedding []float32
	CreatedAt time.Time
}

// SearchHit one ANN search result.
type SearchHit struct {
	ChunkID    string  `json:"chunk_id"`
	AgentID    string  `json:"agent_id"`
	DocID      string  `json:"doc_id"`
	Text       string  `json:"text"`
	Similarity float32 `json:"similarity"`
}

// VectorDB Cassandra-backed dense vector store.
type VectorDB struct {
	session *gocql.Session
}

func NewVectorDB(sess *gocql.Session) *VectorDB {
	return &VectorDB{session: sess}
}

// Upsert inserts (or replaces by chunk_id) a batch of dense chunks.
func (v *VectorDB) Upsert(ctx context.Context, chunks []DenseChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	stmt := `INSERT INTO chunks_dense
	         (chunk_id, agent_id, doc_id, text, model, embedding, created_at)
	         VALUES (?, ?, ?, ?, ?, ?, ?)`
	for _, ch := range chunks {
		if ch.Model == "" {
			ch.Model = "bge-m3"
		}
		if ch.CreatedAt.IsZero() {
			ch.CreatedAt = time.Now().UTC()
		}
		if len(ch.Embedding) == 0 {
			return fmt.Errorf("vectordb: empty embedding for chunk %s", ch.ChunkID)
		}
		if err := v.session.Query(stmt,
			ch.ChunkID, ch.AgentID, ch.DocID, ch.Text, ch.Model, CQLVector(ch.Embedding), ch.CreatedAt,
		).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("vectordb: upsert %s: %w", ch.ChunkID, err)
		}
	}
	return nil
}

// SearchANN performs vector ANN search filtered by agent_id, returning top-K.
//
// Cassandra 5.0 SAI vector index syntax:
//
//	SELECT chunk_id, agent_id, doc_id, text
//	FROM chunks_dense
//	WHERE agent_id = ?
//	ORDER BY embedding ANN OF ?
//	LIMIT ?;
func (v *VectorDB) SearchANN(ctx context.Context, agentID string, query []float32, topK int) ([]SearchHit, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("vectordb: empty query vector")
	}
	if topK <= 0 {
		topK = 5
	}

	stmt := `SELECT chunk_id, agent_id, doc_id, text
	         FROM chunks_dense
	         WHERE agent_id = ?
	         ORDER BY embedding ANN OF ?
	         LIMIT ?`
	iter := v.session.Query(stmt, agentID, CQLVector(query), topK).WithContext(ctx).Iter()

	out := make([]SearchHit, 0, topK)
	var chunkID, aID, docID, text string
	for iter.Scan(&chunkID, &aID, &docID, &text) {
		out = append(out, SearchHit{
			ChunkID: chunkID,
			AgentID: aID,
			DocID:   docID,
			Text:    text,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("vectordb: ANN query: %w", err)
	}
	return out, nil
}

// DeleteByDoc deletes all chunks for an (agent_id, doc_id) pair.
func (v *VectorDB) DeleteByDoc(ctx context.Context, agentID, docID string) (int, error) {
	selStmt := `SELECT chunk_id FROM chunks_dense WHERE agent_id = ? AND doc_id = ? ALLOW FILTERING`
	iter := v.session.Query(selStmt, agentID, docID).WithContext(ctx).Iter()
	var ids []string
	var id string
	for iter.Scan(&id) {
		ids = append(ids, id)
	}
	if err := iter.Close(); err != nil {
		return 0, fmt.Errorf("vectordb: scan chunk_ids: %w", err)
	}
	delStmt := `DELETE FROM chunks_dense WHERE chunk_id = ?`
	for _, cid := range ids {
		if err := v.session.Query(delStmt, cid).WithContext(ctx).Exec(); err != nil {
			return 0, fmt.Errorf("vectordb: delete %s: %w", cid, err)
		}
	}
	return len(ids), nil
}

// CountByAgent reports how many chunks an agent owns (best-effort; may be slow).
func (v *VectorDB) CountByAgent(ctx context.Context, agentID string) (int, error) {
	var n int
	if err := v.session.Query(
		`SELECT count(*) FROM chunks_dense WHERE agent_id = ?`, agentID,
	).WithContext(ctx).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
