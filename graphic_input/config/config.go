// Package config — graphic_input 服務組態。
package config

import (
	"fmt"
	"os"

	hypconfig "github.com/maoxiaoyue/hypgo/pkg/config"
	"gopkg.in/yaml.v3"

	"graphic_input/internal/logger"
)

// Config 服務組態。
type Config struct {
	Server   hypconfig.ServerConfig `yaml:"server"`
	Database DatabaseConfig         `yaml:"database"`
	Logger   logger.Config          `yaml:"logger"`
	GRPC     GRPCConfig             `yaml:"grpc"`
	Graph    GraphConfig            `yaml:"graph"`
	Vault    VaultConfig            `yaml:"vault"`
	Vision   VisionConfig           `yaml:"vision"`
}

// GRPCConfig 本服務的 gRPC server 設定（內網呼叫端用；client 分頁走 REST）。
type GRPCConfig struct {
	// Addr gRPC server 監聽位址；未設定時使用 ":8513"
	Addr string `yaml:"addr"`
	// CAFile 內部 CA 憑證路徑；非空即啟用 client cert 驗證
	CAFile string `yaml:"ca_file"`
	// AllowedClientCNs mTLS 啟用時放行的呼叫端 CN 允許清單
	AllowedClientCNs []string `yaml:"allowed_client_cns"`
	// TrustedIPs 信任的內網來源 IP/CIDR（dev_090 通道）
	TrustedIPs []string `yaml:"trusted_ips"`
}

// GraphConfig agent_graph 的 gRPC 連線設定（必填——本服務的存在意義就是寫圖譜）。
type GraphConfig struct {
	// GRPCAddr agent_graph gRPC 位址，例 "localhost:8512"
	GRPCAddr string `yaml:"grpc_addr"`
	// InsecureSkipVerify 自簽憑證（dev）時跳過 TLS 驗證（ca_file 設定後無效）
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
	// ClientCertFile / ClientKeyFile 內部 mTLS 呼叫端憑證（CN=graphic_input）
	ClientCertFile string `yaml:"client_cert_file"`
	ClientKeyFile  string `yaml:"client_key_file"`
	// CAFile 內部 CA 憑證路徑
	CAFile string `yaml:"ca_file"`
	// DialTimeoutSec / CallTimeoutSec 逾時（秒）；<=0 → 5 / 120（IngestText 要跑 LLM）
	DialTimeoutSec int `yaml:"dial_timeout_sec"`
	CallTimeoutSec int `yaml:"call_timeout_sec"`
}

// VaultConfig Obsidian vault 落地設定（「Obsidian 版本」路徑）。
// Path 必須與 agent_graph 的 md.yaml vault.path 指向同一目錄。
type VaultConfig struct {
	// Path vault 根目錄；空 = 停用 obsidian 路徑（upload targets=obsidian 回錯）
	Path string `yaml:"path"`
	// InboxDir 人工輸入筆記的子目錄；未設定時 "inbox"
	InboxDir string `yaml:"inbox_dir"`
	// AttachmentsDir 圖片原檔子目錄；未設定時 "attachments"
	AttachmentsDir string `yaml:"attachments_dir"`
	// TempDir 知識沉澱暫存子目錄（須與 agent_graph 的 md.yaml vault.temp_dir 一致）；
	// 未設定時 "temp"。Documents 上傳 target=temp 時落這裡。
	TempDir string `yaml:"temp_dir"`
}

// VisionConfig 圖片 OCR（直接打 Ollama，同 agent_graph 慣例）。
type VisionConfig struct {
	// OllamaURL 例 "http://192.168.50.104:11434"；空 = 停用圖片輸入
	OllamaURL string `yaml:"ollama_url"`
	// Model 視覺模型 tag，例 "qwen3-vl:4b"
	Model string `yaml:"model"`
	// Prompt 抽文字提示；空 → 內建預設
	Prompt string `yaml:"prompt"`
	// TimeoutSec <=0 → 120
	TimeoutSec int `yaml:"timeout_sec"`
}

// DatabaseConfig 實作 hypconfig.DatabaseConfigInterface（graphic_input_log 表）。
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

// Load 讀取並套用預設值。
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(raw))), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.GRPC.Addr == "" {
		cfg.GRPC.Addr = ":8513"
	}
	if cfg.Vault.InboxDir == "" {
		cfg.Vault.InboxDir = "inbox"
	}
	if cfg.Vault.AttachmentsDir == "" {
		cfg.Vault.AttachmentsDir = "attachments"
	}
	if cfg.Graph.DialTimeoutSec <= 0 {
		cfg.Graph.DialTimeoutSec = 5
	}
	if cfg.Graph.CallTimeoutSec <= 0 {
		cfg.Graph.CallTimeoutSec = 120
	}
	return cfg, nil
}

// Hyp 提供 hypgo server 需要的設定子集。
func (c *Config) Hyp() *hypconfig.Config {
	return &hypconfig.Config{Server: c.Server}
}
