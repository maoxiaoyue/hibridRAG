package cassandra

import (
	"context"
	"fmt"
	"strings"

	hidbcassandra "github.com/maoxiaoyue/hypgo/pkg/hidb/cassandra"
)

var instance *hidbcassandra.CassandraDB

// Init 以 hidb 的 cassandra plugin 建立 session（singleton）
//
// 用於存放長期記憶的向量化片段（long_term_memory 表）。
// 連線後可透過 Get().Session() 取得原生 *gocql.Session，
// 進行 SAI ANN 查詢、批次寫入等向量操作。
func Init(cfg hidbcassandra.Config) (*hidbcassandra.CassandraDB, error) {
	db, err := hidbcassandra.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("cassandra init: %w", err)
	}
	instance = db
	return db, nil
}

// Get 回傳已初始化的 CassandraDB；呼叫前需先 Init
func Get() *hidbcassandra.CassandraDB {
	return instance
}

// Close 關閉 session
func Close() {
	if instance != nil {
		instance.Close()
	}
}

// EnsureSchema 建立 long_term_memory 表 + SAI 索引（含 ANN 用的 embedding 向量索引）。
// 對應 migrations/cassandra/001_create_long_term_memory.up.cql；冪等（IF NOT EXISTS）。
// 在每次成功連上 Cassandra 後呼叫——這個連線本身就是 best-effort（見 main.go 的
// `if len(cfg.Cassandra.Hosts) > 0` 判斷），沒有必要再疊一層 config flag。
func EnsureSchema(ctx context.Context) error {
	if instance == nil {
		return fmt.Errorf("cassandra: not initialized")
	}
	session := instance.Session()

	createTable := `
		CREATE TABLE IF NOT EXISTS long_term_memory (
			memory_id     text PRIMARY KEY,
			agent_id      text,
			category      text,
			source        text,
			content       text,
			summary       text,
			metadata      map<text, text>,
			embedding     vector<float, 1024>,
			importance    float,
			created_at    timestamp,
			last_accessed timestamp,
			access_count  int
		)`
	if err := session.Query(createTable).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("create long_term_memory: %w", err)
	}

	indexes := []struct {
		name string
		stmt string
	}{
		{"idx_long_term_memory_embedding", `CREATE CUSTOM INDEX IF NOT EXISTS idx_long_term_memory_embedding ON long_term_memory(embedding) USING 'StorageAttachedIndex' WITH OPTIONS = {'similarity_function': 'COSINE'}`},
		{"idx_long_term_memory_agent", `CREATE CUSTOM INDEX IF NOT EXISTS idx_long_term_memory_agent ON long_term_memory(agent_id) USING 'StorageAttachedIndex'`},
		{"idx_long_term_memory_category", `CREATE CUSTOM INDEX IF NOT EXISTS idx_long_term_memory_category ON long_term_memory(category) USING 'StorageAttachedIndex'`},
	}
	for _, idx := range indexes {
		if err := session.Query(idx.stmt).WithContext(ctx).Exec(); err != nil {
			if strings.Contains(err.Error(), "Cannot create more than one storage-attached index") ||
				strings.Contains(err.Error(), "already exists") {
				continue // 既有 SAI on 同欄位可接受
			}
			return fmt.Errorf("create %s: %w", idx.name, err)
		}
	}
	return nil
}
