package config

import (
	"fmt"
	"os"

	hypconfig "github.com/maoxiaoyue/hypgo/pkg/config"
	hidbcassandra "github.com/maoxiaoyue/hypgo/pkg/hidb/cassandra"
	"gopkg.in/yaml.v3"

	"agent_memory/internal/logger"
)

// Config 服務組態
type Config struct {
	Server    hypconfig.ServerConfig `yaml:"server"`
	Database  DatabaseConfig         `yaml:"database"`
	Cassandra hidbcassandra.Config   `yaml:"cassandra"`
	Logger    logger.Config          `yaml:"logger"`
	GRPC      GRPCConfig             `yaml:"grpc"`
}

// GRPCConfig gRPC server 設定 — 供 agents 服務的 ContextBuilder/EpisodicWriter
// 等透過 gRPC 呼叫本服務（取代 agents 舊有的直接打 DB 做法）。
type GRPCConfig struct {
	// Addr gRPC server 監聽位址；未設定時使用 ":8510"
	Addr string `yaml:"addr"`
	// CAFile 內部 CA 憑證路徑（dev_pub_0.9 mTLS）；非空即要求並驗證呼叫端 client cert
	CAFile string `yaml:"ca_file"`
	// AllowedClientCNs mTLS 啟用時放行的呼叫端 CN 允許清單；空清單 = 拒絕全部（安全預設）
	AllowedClientCNs []string `yaml:"allowed_client_cns"`
	// TrustedIPs 信任的內網來源 IP/CIDR（dev_090 通道，如 "192.168.50.103"）——
	// 命中即免 client cert 放行；非空時 mTLS 握手降為 VerifyClientCertIfGiven
	TrustedIPs []string `yaml:"trusted_ips"`
}

// DatabaseConfig 實作 hypconfig.DatabaseConfigInterface，同時內嵌 Redis 設定
type DatabaseConfig struct {
	Driver       string      `yaml:"driver"`
	DSN          string      `yaml:"dsn"`
	MaxIdleConns int         `yaml:"max_idle_conns"`
	MaxOpenConns int         `yaml:"max_open_conns"`
	AutoMigrate  bool        `yaml:"auto_migrate"`
	Redis        RedisConfig `yaml:"redis"`
}

// RedisConfig 實作 hypconfig.RedisConfigInterface
type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// ── DatabaseConfigInterface ──────────────────────────────────────────────────

func (c *DatabaseConfig) GetDriver() string { return c.Driver }
func (c *DatabaseConfig) GetDSN() string    { return c.DSN }

func (c *DatabaseConfig) GetMaxIdleConns() int {
	if c.MaxIdleConns == 0 {
		return 10
	}
	return c.MaxIdleConns
}

func (c *DatabaseConfig) GetMaxOpenConns() int {
	if c.MaxOpenConns == 0 {
		return 100
	}
	return c.MaxOpenConns
}

func (c *DatabaseConfig) GetRedisConfig() hypconfig.RedisConfigInterface {
	return &c.Redis
}

// ── RedisConfigInterface ─────────────────────────────────────────────────────

func (r *RedisConfig) GetAddr() string     { return r.Addr }
func (r *RedisConfig) GetPassword() string { return r.Password }
func (r *RedisConfig) GetDB() int          { return r.DB }

// ── Loader ───────────────────────────────────────────────────────────────────

// Load 讀取 YAML 設定檔，支援 ${VAR} 環境變數展開
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
	return cfg, nil
}

// Hyp 組出 hypgo server.New 所需的 config
func (c *Config) Hyp() *hypconfig.Config {
	return &hypconfig.Config{
		Server: c.Server,
	}
}
