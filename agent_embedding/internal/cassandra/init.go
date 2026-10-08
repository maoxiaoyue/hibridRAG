// Package cassandra wraps hypgo/pkg/hidb/cassandra session lifecycle for the embedding service.
package cassandra

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gocql/gocql"
	hidbcassandra "github.com/maoxiaoyue/hypgo/pkg/hidb/cassandra"
)

// Config Cassandra connection options (mirrors agent_embedding config).
type Config struct {
	Hosts       []string
	Keyspace    string
	Username    string
	Password    string
	Consistency string
	Timeout     time.Duration
	NumConns    int
}

var instance *hidbcassandra.CassandraDB

// Init creates the gocql session via hidb/cassandra.
func Init(cfg Config) (*gocql.Session, error) {
	if len(cfg.Hosts) == 0 {
		return nil, fmt.Errorf("cassandra: hosts required")
	}
	if cfg.Keyspace == "" {
		cfg.Keyspace = "mcp"
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
		return nil, fmt.Errorf("cassandra: %w", err)
	}
	instance = db
	return db.Session(), nil
}

// Get returns the active session.
func Get() *gocql.Session {
	if instance == nil {
		return nil
	}
	return instance.Session()
}

// Database returns the underlying hidb-managed wrapper.
func Database() *hidbcassandra.CassandraDB { return instance }

// Close terminates the session.
func Close() {
	if instance != nil {
		_ = instance.Close()
	}
}

// Ping verifies connectivity.
func Ping(ctx context.Context) error {
	if instance == nil {
		return fmt.Errorf("cassandra: not initialized")
	}
	return instance.Ping(ctx)
}

// EnsureSchema creates the chunks_dense table and SAI ANN index if absent.
// Vector dimension is BGE-M3 (1024). Idempotent.
func EnsureSchema(ctx context.Context, dimension int) error {
	if instance == nil {
		return fmt.Errorf("cassandra: not initialized")
	}
	if dimension <= 0 {
		dimension = 1024
	}
	session := instance.Session()

	createTable := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS chunks_dense (
			chunk_id   text PRIMARY KEY,
			agent_id   text,
			doc_id     text,
			text       text,
			model      text,
			embedding  vector<float, %d>,
			created_at timestamp
		)`, dimension)
	if err := session.Query(createTable).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("create chunks_dense: %w", err)
	}

	// Best-effort ADD COLUMN for tables created by older versions of this service.
	// Cassandra has no IF NOT EXISTS for ADD COLUMN, so we ignore "already exists" errors.
	for _, col := range []string{
		`ALTER TABLE chunks_dense ADD text text`,
		`ALTER TABLE chunks_dense ADD model text`,
		`ALTER TABLE chunks_dense ADD created_at timestamp`,
	} {
		if err := session.Query(col).WithContext(ctx).Exec(); err != nil {
			if !strings.Contains(err.Error(), "already exists") &&
				!strings.Contains(err.Error(), "Invalid column name") &&
				!strings.Contains(err.Error(), "conflicts with an existing column") {
				return fmt.Errorf("alter chunks_dense: %w", err)
			}
		}
	}

	// SAI indexes — tolerate existing index on the same column (Cassandra
	// allows only one SAI per column; an existing index is fine for our needs).
	indexes := []struct {
		name string
		stmt string
	}{
		{"chunks_dense_agent_idx", `CREATE CUSTOM INDEX IF NOT EXISTS chunks_dense_agent_idx ON chunks_dense(agent_id) USING 'StorageAttachedIndex'`},
		{"chunks_dense_doc_idx", `CREATE CUSTOM INDEX IF NOT EXISTS chunks_dense_doc_idx ON chunks_dense(doc_id) USING 'StorageAttachedIndex'`},
		{"chunks_dense_vec_idx", `CREATE CUSTOM INDEX IF NOT EXISTS chunks_dense_vec_idx ON chunks_dense(embedding) USING 'StorageAttachedIndex' WITH OPTIONS = {'similarity_function': 'cosine'}`},
	}
	for _, idx := range indexes {
		if err := session.Query(idx.stmt).WithContext(ctx).Exec(); err != nil {
			if strings.Contains(err.Error(), "Cannot create more than one storage-attached index") ||
				strings.Contains(err.Error(), "already exists") {
				continue // existing SAI on column is acceptable
			}
			return fmt.Errorf("create %s: %w", idx.name, err)
		}
	}

	// chunks_sparse: 2026-05-08 決議放棄 sparse 向量路徑（Ollama BGE-M3
	// backend 不出 sparse），此處不再建表。

	return nil
}
