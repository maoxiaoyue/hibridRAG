// Package vectordb 封裝 Cassandra 5.0 + SAI 作為 RAG dense 向量儲存
//
// 對應架構圖 ④ 存儲層：
//   - chunks_dense(chunk_id text PK, agent_id, doc_id, model, embedding vector<float,1024>, created_at)
//   - SAI index on embedding (similarity_function: COSINE)
//
// 對外 API：
//   - New(cfg) 建立 session（底層走 hidb/cassandra）
//   - Upsert(ctx, []DenseChunk) 批次寫入
//   - SearchANN(ctx, agentID, queryVec, topK) 查 top-K 最相似
//   - Close() 關閉 session
package vectordb

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	hidbcassandra "github.com/maoxiaoyue/hypgo/pkg/hidb/cassandra"
)

// Config Cassandra 連線設定
type Config struct {
	Hosts       []string      // e.g. ["192.168.50.201:9042"]
	Keyspace    string        // 預設 "agent"
	Username    string        // 可空
	Password    string        // 可空
	Consistency string        // e.g. "LOCAL_ONE"（預設）
	Timeout     time.Duration // 預設 5s
	NumConns    int           // 每 host 連線數，預設 2
}

// DenseChunk 單筆 dense 向量寫入記錄
type DenseChunk struct {
	ChunkID   string
	AgentID   string
	DocID     string
	Model     string // 預設 "bge-m3"
	Embedding []float32
	CreatedAt time.Time
}

// SearchHit ANN 查詢結果
type SearchHit struct {
	ChunkID    string  `json:"chunk_id"`
	AgentID    string  `json:"agent_id"`
	DocID      string  `json:"doc_id"`
	Text       string  `json:"text"`       // populated by HTTP client; empty from Cassandra client
	Similarity float32 `json:"similarity"` // 若驅動未回傳則為 0；ranking 由 Cassandra 保證
}

// Client Cassandra session 封裝（底層為 hidb/cassandra）
type Client struct {
	db       *hidbcassandra.CassandraDB
	keyspace string
}

// New 建立 Cassandra client，透過 hidb/cassandra 管理底層 session。
func New(cfg Config) (*Client, error) {
	if len(cfg.Hosts) == 0 {
		return nil, fmt.Errorf("vectordb: hosts required")
	}
	if cfg.Keyspace == "" {
		cfg.Keyspace = "agent"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.NumConns == 0 {
		cfg.NumConns = 2
	}

	db, err := hidbcassandra.New(hidbcassandra.Config{
		Hosts:          cfg.Hosts,
		Keyspace:       cfg.Keyspace,
		Username:       cfg.Username,
		Password:       cfg.Password,
		Consistency:    cfg.Consistency,
		Timeout:        cfg.Timeout,
		ConnectTimeout: cfg.Timeout,
		NumConns:       cfg.NumConns,
		ProtoVersion:   4,
	})
	if err != nil {
		return nil, fmt.Errorf("vectordb: %w", err)
	}
	return &Client{db: db, keyspace: cfg.Keyspace}, nil
}

func (c *Client) session() *gocql.Session { return c.db.Session() }

// Ping 簡易連線健檢
func (c *Client) Ping(ctx context.Context) error {
	return c.db.Ping(ctx)
}

// Close 關閉 session
func (c *Client) Close() {
	if c.db != nil {
		_ = c.db.Close()
	}
}

// Upsert 批次寫入 dense chunks。單筆 insert；若需 BATCH 可另加。
func (c *Client) Upsert(ctx context.Context, chunks []DenseChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	stmt := `INSERT INTO chunks_dense (chunk_id, agent_id, doc_id, model, embedding, created_at)
	         VALUES (?, ?, ?, ?, ?, ?)`
	sess := c.session()
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
		if err := sess.Query(stmt,
			ch.ChunkID, ch.AgentID, ch.DocID, ch.Model, ch.Embedding, ch.CreatedAt,
		).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("vectordb: upsert %s: %w", ch.ChunkID, err)
		}
	}
	return nil
}

// SearchANN 依 agent_id + query 向量取 top-K 相似 chunk_id
//
// 語法（Cassandra 5.0 SAI）：
//
//	SELECT chunk_id, agent_id, doc_id
//	FROM chunks_dense
//	WHERE agent_id = ?
//	ORDER BY embedding ANN OF ?
//	LIMIT ?;
func (c *Client) SearchANN(ctx context.Context, agentID string, query []float32, topK int) ([]SearchHit, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("vectordb: empty query vector")
	}
	if topK <= 0 {
		topK = 20
	}
	stmt := `SELECT chunk_id, agent_id, doc_id
	         FROM chunks_dense
	         WHERE agent_id = ?
	         ORDER BY embedding ANN OF ?
	         LIMIT ?`
	iter := c.session().Query(stmt, agentID, query, topK).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]SearchHit, 0, topK)
	var (
		chunkID, aID, docID string
	)
	for iter.Scan(&chunkID, &aID, &docID) {
		out = append(out, SearchHit{
			ChunkID: chunkID,
			AgentID: aID,
			DocID:   docID,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("vectordb: ANN query: %w", err)
	}
	return out, nil
}

// DeleteByDoc 以 doc_id 刪除（給 re-ingest / 清理用）
// 注意：SAI 以 agent_id / doc_id 為 index，DELETE 需先查 chunk_id 再逐筆刪
func (c *Client) DeleteByDoc(ctx context.Context, agentID, docID string) (int, error) {
	sess := c.session()
	selStmt := `SELECT chunk_id FROM chunks_dense WHERE agent_id = ? AND doc_id = ?`
	iter := sess.Query(selStmt, agentID, docID).WithContext(ctx).Iter()
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
		if err := sess.Query(delStmt, cid).WithContext(ctx).Exec(); err != nil {
			return 0, fmt.Errorf("vectordb: delete %s: %w", cid, err)
		}
	}
	return len(ids), nil
}
