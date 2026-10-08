package services

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// DownstreamConfig 對應 config/config.yaml 中的 `downstream:` 區塊。
// 描述本 agent 作為 gRPC client 推送 OutboundMsg 給下游 agent 時的連線設定。
type DownstreamConfig struct {
	Defaults DownstreamDefaults      `yaml:"defaults"`
	Agents   []DownstreamAgentConfig `yaml:"agents"`
}

// DownstreamDefaults 為所有下游 agent 的共用預設值。
type DownstreamDefaults struct {
	DialTimeout        time.Duration `yaml:"dial_timeout"`
	CallTimeout        time.Duration `yaml:"call_timeout"`
	TLSEnabled         bool          `yaml:"tls_enabled"`
	InsecureSkipVerify bool          `yaml:"insecure_skip_verify"`
	MaxRetries         int           `yaml:"max_retries"`
	RetryBackoff       time.Duration `yaml:"retry_backoff"`
}

// DownstreamAgentConfig 為單一下游 agent 的連線設定。
// ID 對應 OutboundMsg.To。
type DownstreamAgentConfig struct {
	ID          string        `yaml:"id"`
	Endpoint    string        `yaml:"endpoint"`      // host:port
	TLSEnabled  bool          `yaml:"tls_enabled"`
	CAFile      string        `yaml:"ca_file"`       // TLS CA 憑證路徑
	ServerName  string        `yaml:"server_name"`   // TLS SNI / override
	CallTimeout time.Duration `yaml:"call_timeout"`  // 單 agent 層級 override
	DialTimeout time.Duration `yaml:"dial_timeout"`
	Tags        []string      `yaml:"tags"`
}

// downstreamYAML 為部分載入 config.yaml 的輔助結構 (只解析 downstream 區塊)。
type downstreamYAML struct {
	Downstream DownstreamConfig `yaml:"downstream"`
}

// LoadDownstreamConfig 從指定 yaml 檔讀取 `downstream:` 區塊。
func LoadDownstreamConfig(path string) (*DownstreamConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("downstream: read %s: %w", path, err)
	}
	var y downstreamYAML
	if err := yaml.Unmarshal(data, &y); err != nil {
		return nil, fmt.Errorf("downstream: parse %s: %w", path, err)
	}
	cfg := y.Downstream
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ApplyDefaults 將 Defaults 下的值套用到未指定的 agent 欄位。
func (c *DownstreamConfig) ApplyDefaults() {
	if c.Defaults.DialTimeout == 0 {
		c.Defaults.DialTimeout = 3 * time.Second
	}
	if c.Defaults.CallTimeout == 0 {
		c.Defaults.CallTimeout = 5 * time.Second
	}
	if c.Defaults.RetryBackoff == 0 {
		c.Defaults.RetryBackoff = 200 * time.Millisecond
	}
	for i := range c.Agents {
		a := &c.Agents[i]
		if a.DialTimeout == 0 {
			a.DialTimeout = c.Defaults.DialTimeout
		}
		if a.CallTimeout == 0 {
			a.CallTimeout = c.Defaults.CallTimeout
		}
	}
}

// Validate 對 downstream 設定做基本檢查。
func (c *DownstreamConfig) Validate() error {
	seen := make(map[string]struct{}, len(c.Agents))
	for i, a := range c.Agents {
		if a.ID == "" {
			return fmt.Errorf("downstream.agents[%d]: missing id", i)
		}
		if a.Endpoint == "" {
			return fmt.Errorf("downstream.agents[%s]: missing endpoint", a.ID)
		}
		if _, dup := seen[a.ID]; dup {
			return fmt.Errorf("downstream.agents: duplicate id %q", a.ID)
		}
		seen[a.ID] = struct{}{}
		if a.TLSEnabled && a.CAFile == "" && !c.Defaults.InsecureSkipVerify {
			return fmt.Errorf("downstream.agents[%s]: tls_enabled=true but ca_file is empty", a.ID)
		}
	}
	return nil
}

// Find 以 agent id 查找 endpoint 設定；對應 OutboundMsg.To。
func (c *DownstreamConfig) Find(id string) (*DownstreamAgentConfig, bool) {
	for i := range c.Agents {
		if c.Agents[i].ID == id {
			return &c.Agents[i], true
		}
	}
	return nil, false
}
