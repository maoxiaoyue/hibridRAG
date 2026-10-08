package appconfig

import (
	"fmt"
	"os"

	hypconfig "github.com/maoxiaoyue/hypgo/pkg/config"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    hypconfig.ServerConfig `yaml:"server"`
	GRPC      GRPCConfig             `yaml:"grpc"`
	Registry  RegistryConfig         `yaml:"registry"`
	Gateway   GatewayConfig          `yaml:"gateway"`
	A2AClient A2AClientConfig        `yaml:"a2a_client"`
	Internal  InternalConfig         `yaml:"internal"` // dev_pub_0.9：對內轉發目標 + mTLS 呼叫端憑證
	Auth      AuthConfig             `yaml:"auth"`     // dev_pub_0.9：ClientGateway 的 API Key 認證
	Database  DatabaseConfig         `yaml:"database"` // dev_pub_0.9：api_keys 表所在 PG
	Logger    LoggerConfig           `yaml:"logger"`
}

// InternalConfig dev_pub_0.9 gateway 對內部服務的連線設定。
type InternalConfig struct {
	// AgentsGRPCAddr agents 服務 gRPC 位址（例 "localhost:9091"）；ClientGateway 的
	// Chat/UploadIngest/GetIngestJob 轉發目標。留空 → 對應 RPC 回 Unavailable。
	AgentsGRPCAddr string `yaml:"agents_grpc_addr"`
	// GraphGRPCAddr agent_graph 服務 gRPC 位址（例 "localhost:8512"）；Graph* 轉發目標。
	GraphGRPCAddr string `yaml:"graph_grpc_addr"`
	// EmbeddingBaseURL agent_embedding REST base URL（例 "https://localhost:8502"）；
	// VerifySearch 轉發目標（embedding 不加 gRPC，內部 HTTPS 呼叫）。
	EmbeddingBaseURL string `yaml:"embedding_base_url"`
	// AgentsHTTPBase agents 服務 REST base URL（例 "https://localhost:18080"）；
	// ChatStatus 轉發目標（對話階段輪詢走內部 HTTP，不占 gRPC）。留空 → ChatStatus 回空狀態。
	AgentsHTTPBase string `yaml:"agents_http_base"`
	// ClientCertFile / ClientKeyFile 內部 mTLS 呼叫端憑證（CN=agent_gateway）
	ClientCertFile string `yaml:"client_cert_file"`
	ClientKeyFile  string `yaml:"client_key_file"`
	// CAFile 內部 CA；非空時完整驗證下游 server 憑證，空 → InsecureSkipVerify（dev）
	CAFile string `yaml:"ca_file"`
	// CallTimeoutSec 對內單次 RPC/HTTP 呼叫逾時（秒）；<=0 → 120（Chat 推理可能較久）
	CallTimeoutSec int `yaml:"call_timeout_sec"`
}

// AuthConfig ClientGateway 的 API Key 認證設定。
type AuthConfig struct {
	// Enabled 公網部署必開；false = ClientGateway 不驗 key（僅限本機開發）
	Enabled bool `yaml:"enabled"`
	// TrustedIPs 信任的內網來源 IP/CIDR（dev_090 通道，如 "192.168.50.103"）——
	// 命中即免 API Key。只該搭配「防火牆已擋外網」使用。
	TrustedIPs []string `yaml:"trusted_ips"`
	// RateLimit 每個來源 IP 的 token-bucket 限流；未設定時預設 60 rps / 120 burst。
	RateLimit RateLimitConfig `yaml:"rate_limit"`
}

// RateLimitConfig gateway 對外 gRPC 每 IP 的 token-bucket 限流。
type RateLimitConfig struct {
	// Enabled true = 掛限流 interceptor；未設定時預設 true（外部部署預期打開）。
	// 用 *bool 讓「沒寫 = 開」跟「明確寫 false = 關」可分辨。
	Enabled *bool `yaml:"enabled"`
	// PerIPRPS 每 IP 每秒 sustained 請求數；<=0 → 60。
	PerIPRPS float64 `yaml:"per_ip_rps"`
	// PerIPBurst 每 IP 桶容量（也是允許的最大 burst）；<=0 → 120。
	PerIPBurst int `yaml:"per_ip_burst"`
}

// DatabaseConfig 實作 hypconfig 介面（同 agent_registry 慣例），存 api_keys 表。
type DatabaseConfig struct {
	Driver       string `yaml:"driver"`
	DSN          string `yaml:"dsn"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	AutoMigrate  bool   `yaml:"auto_migrate"`
}

func (c *DatabaseConfig) GetDriver() string { return c.Driver }
func (c *DatabaseConfig) GetDSN() string    { return c.DSN }
func (c *DatabaseConfig) GetMaxIdleConns() int {
	if c.MaxIdleConns <= 0 {
		return 4
	}
	return c.MaxIdleConns
}
func (c *DatabaseConfig) GetMaxOpenConns() int {
	if c.MaxOpenConns <= 0 {
		return 16
	}
	return c.MaxOpenConns
}
func (c *DatabaseConfig) GetRedisConfig() hypconfig.RedisConfigInterface { return nil }

// A2AClientConfig TLS options for outbound A2A HTTP requests.
type A2AClientConfig struct {
	// InsecureSkipVerify disables TLS certificate verification (dev only).
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	// CAFile path to a PEM CA certificate to verify downstream agent TLS certs.
	CAFile             string `yaml:"ca_file"`
}

type RegistryConfig struct {
	URL string `yaml:"url"`
}

type GatewayConfig struct {
	// Self agent-card metadata
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
	PublicURL   string `yaml:"public_url"`
	// Timeout for downstream calls (seconds)
	DownstreamTimeoutSec int `yaml:"downstream_timeout_sec"`
	// gRPC dial timeout (seconds)
	GRPCDialTimeoutSec int `yaml:"grpc_dial_timeout_sec"`
}

type GRPCConfig struct {
	Addr string `yaml:"addr"` // e.g. :9090
}

type LoggerConfig struct {
	Level  string `yaml:"level"`
	Output string `yaml:"output"`
	Colors bool   `yaml:"colors"`
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
	if cfg.Gateway.DownstreamTimeoutSec == 0 {
		cfg.Gateway.DownstreamTimeoutSec = 30
	}
	if cfg.Gateway.GRPCDialTimeoutSec == 0 {
		cfg.Gateway.GRPCDialTimeoutSec = 5
	}
	if cfg.Gateway.Name == "" {
		cfg.Gateway.Name = "A2A Gateway"
	}
	if cfg.Gateway.Version == "" {
		cfg.Gateway.Version = "1.0.0"
	}
	if cfg.GRPC.Addr == "" {
		cfg.GRPC.Addr = ":9090"
	}
	if cfg.Internal.CallTimeoutSec <= 0 {
		cfg.Internal.CallTimeoutSec = 120
	}
	return cfg, nil
}

func (c *Config) Hyp() *hypconfig.Config {
	return &hypconfig.Config{
		Server: c.Server,
	}
}
