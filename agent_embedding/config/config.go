package config

import (
	"fmt"
	"os"
	"time"

	hypconfig "github.com/maoxiaoyue/hypgo/pkg/config"
	"gopkg.in/yaml.v3"

	"agent_embedding/internal/logger"
	"agent_embedding/internal/postgres"
)

type Config struct {
	Server    hypconfig.ServerConfig `yaml:"server"`
	Embedding EmbeddingConfig        `yaml:"embedding"`
	Cassandra CassandraConfig        `yaml:"cassandra"`
	Redis     RedisConfig            `yaml:"redis"`
	Postgres  postgres.Config        `yaml:"postgres"`
	Hybrid    HybridConfig           `yaml:"hybrid"`
	EventBus  EventBusConfig         `yaml:"eventbus"`
	Logger    logger.Config          `yaml:"logger"`
}

// HybridConfig HybridSearcher 預設值（可被請求參數覆蓋）
type HybridConfig struct {
	Enabled    bool    `yaml:"enabled"`     // false 時 /api/search/hybrid 回 503
	DenseTopN  int     `yaml:"dense_top_n"` // 預設 20
	SparseTopN int     `yaml:"sparse_top_n"`// 預設 20
	FinalTopK  int     `yaml:"final_top_k"` // 預設 5
	RRFK       int     `yaml:"rrf_k"`       // 預設 60
	MinScore   float64 `yaml:"min_score"`   // 預設 0（不過濾）
	// SparseStrategy 控制 sparse 搜尋路徑：
	//   "fts"    使用 PG full-text search（to_tsvector，預設）
	//   "splade" 使用 chunks_sparse 的 BGE-M3 sparse 向量點積
	//   "auto"   優先 splade，無資料時 fallback 到 fts
	SparseStrategy string `yaml:"sparse_strategy"`
}

// EventBusConfig 對應架構 v2 的 ⚡ Event Bus 元件（Redis Stream）
type EventBusConfig struct {
	Enabled        bool          `yaml:"enabled"`
	StreamUpserted string        `yaml:"stream_upserted"` // 預設 "embedding.upserted"
	StreamSearched string        `yaml:"stream_searched"` // 預設 "embedding.searched"
	StreamDeleted  string        `yaml:"stream_deleted"`  // 預設 "embedding.deleted"
	MaxLen         int64         `yaml:"max_len"`         // 0 = 不限；建議 10000
	PublishTimeout time.Duration `yaml:"publish_timeout"` // 預設 1s
}

type EmbeddingConfig struct {
	Backend      string        `yaml:"backend"`        // ollama | tei
	BaseURL      string        `yaml:"base_url"`
	Model        string        `yaml:"model"`
	Dimension    int           `yaml:"dimension"`
	Timeout      time.Duration `yaml:"timeout"`
	MaxBatchSize int           `yaml:"max_batch_size"`
}

type CassandraConfig struct {
	Hosts       []string      `yaml:"hosts"`
	Keyspace    string        `yaml:"keyspace"`
	Username    string        `yaml:"username"`
	Password    string        `yaml:"password"`
	Consistency string        `yaml:"consistency"`
	Timeout     time.Duration `yaml:"timeout"`
	NumConns    int           `yaml:"num_conns"`
	AutoInit    bool          `yaml:"auto_init"`
}

type RedisConfig struct {
	Addr     string        `yaml:"addr"`
	Password string        `yaml:"password"`
	DB       int           `yaml:"db"`
	CacheTTL time.Duration `yaml:"cache_ttl"`
	Enabled  bool          `yaml:"enabled"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	expanded := os.ExpandEnv(string(raw))

	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// defaults
	if cfg.Embedding.Backend == "" {
		cfg.Embedding.Backend = "ollama"
	}
	if cfg.Embedding.Model == "" {
		cfg.Embedding.Model = "bge-m3"
	}
	if cfg.Embedding.Dimension == 0 {
		cfg.Embedding.Dimension = 1024
	}
	if cfg.Embedding.Timeout == 0 {
		cfg.Embedding.Timeout = 60 * time.Second
	}
	if cfg.Embedding.MaxBatchSize == 0 {
		cfg.Embedding.MaxBatchSize = 32
	}
	if cfg.Cassandra.Keyspace == "" {
		cfg.Cassandra.Keyspace = "mcp"
	}
	if cfg.Cassandra.Timeout == 0 {
		cfg.Cassandra.Timeout = 5 * time.Second
	}
	if cfg.Cassandra.NumConns == 0 {
		cfg.Cassandra.NumConns = 2
	}
	if cfg.Redis.CacheTTL == 0 {
		cfg.Redis.CacheTTL = 24 * time.Hour
	}
	// Hybrid defaults
	if cfg.Hybrid.DenseTopN == 0 {
		cfg.Hybrid.DenseTopN = 20
	}
	if cfg.Hybrid.SparseTopN == 0 {
		cfg.Hybrid.SparseTopN = 20
	}
	if cfg.Hybrid.FinalTopK == 0 {
		cfg.Hybrid.FinalTopK = 5
	}
	if cfg.Hybrid.RRFK == 0 {
		cfg.Hybrid.RRFK = 60
	}
	if cfg.Hybrid.SparseStrategy == "" {
		cfg.Hybrid.SparseStrategy = "fts"
	}
	// Postgres defaults
	if cfg.Postgres.MaxOpenConns == 0 {
		cfg.Postgres.MaxOpenConns = 10
	}
	if cfg.Postgres.MaxIdleConns == 0 {
		cfg.Postgres.MaxIdleConns = 2
	}
	if cfg.Postgres.ConnMaxLifetime == 0 {
		cfg.Postgres.ConnMaxLifetime = 30 * time.Minute
	}

	// EventBus defaults
	if cfg.EventBus.StreamUpserted == "" {
		cfg.EventBus.StreamUpserted = "embedding.upserted"
	}
	if cfg.EventBus.StreamSearched == "" {
		cfg.EventBus.StreamSearched = "embedding.searched"
	}
	if cfg.EventBus.StreamDeleted == "" {
		cfg.EventBus.StreamDeleted = "embedding.deleted"
	}
	if cfg.EventBus.PublishTimeout == 0 {
		cfg.EventBus.PublishTimeout = 1 * time.Second
	}
	return cfg, nil
}

func (c *Config) Hyp() *hypconfig.Config {
	return &hypconfig.Config{Server: c.Server}
}
