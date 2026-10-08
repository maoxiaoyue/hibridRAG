package config

import (
	"fmt"
	"os"

	hypconfig "github.com/maoxiaoyue/hypgo/pkg/config"
	"gopkg.in/yaml.v3"

	"agent_critic/internal/logger"
)

// Config 服務組態
type Config struct {
	Server   hypconfig.ServerConfig `yaml:"server"`
	Database DatabaseConfig         `yaml:"database"`
	Logger   logger.Config          `yaml:"logger"`
	GRPC     GRPCConfig             `yaml:"grpc"`
	Critic   CriticConfig           `yaml:"critic"`
}

// GRPCConfig gRPC server 設定 — 供 agents 服務（或 orchestrator）在研究產出
// 答案後呼叫 Evaluate，依評分決定是否進入 revision loop。
type GRPCConfig struct {
	// Addr gRPC server 監聽位址；未設定時使用 ":8511"
	Addr string `yaml:"addr"`
	// CAFile 內部 CA 憑證路徑（dev_pub_0.9 mTLS）；非空即要求並驗證呼叫端 client cert
	CAFile string `yaml:"ca_file"`
	// AllowedClientCNs mTLS 啟用時放行的呼叫端 CN 允許清單；空清單 = 拒絕全部（安全預設）
	AllowedClientCNs []string `yaml:"allowed_client_cns"`
	// TrustedIPs 信任的內網來源 IP/CIDR（dev_090 通道，如 "192.168.50.103"）——
	// 命中即免 client cert 放行；非空時 mTLS 握手降為 VerifyClientCertIfGiven
	TrustedIPs []string `yaml:"trusted_ips"`
}

// CriticConfig 評分規則設定
type CriticConfig struct {
	// PassThreshold overall 達此分數（含）視為通過；未設定時使用 0.7
	PassThreshold float64 `yaml:"pass_threshold"`
	// Weights 三個維度的加權；全部未設定時使用 0.4 / 0.3 / 0.3
	Weights CriticWeights `yaml:"weights"`
}

// CriticWeights overall = completeness*C + grounding*G + faithfulness*F（自動正規化）
type CriticWeights struct {
	Completeness float64 `yaml:"completeness"`
	Grounding    float64 `yaml:"grounding"`
	Faithfulness float64 `yaml:"faithfulness"`
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
