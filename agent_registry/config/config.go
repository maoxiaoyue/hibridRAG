package config

import (
	"fmt"
	"os"
	"strings"

	hypconfig "github.com/maoxiaoyue/hypgo/pkg/config"
	"gopkg.in/yaml.v3"

	"agent_registry/internal/logger"
)

type Config struct {
	Server   hypconfig.ServerConfig `yaml:"server"`
	Database DatabaseConfig         `yaml:"database"`
	Logger   logger.Config          `yaml:"logger"`
	Ollama   OllamaConfig           `yaml:"ollama"`
}

// OllamaConfig 供 model catalog 探測用的 Ollama 位址。
type OllamaConfig struct {
	Host string `yaml:"host"`
}

// GetHost 回傳設定的 Ollama host；未設定時退回內網預設。
func (c OllamaConfig) GetHost() string {
	if strings.TrimSpace(c.Host) == "" {
		return "http://192.168.50.104:11434"
	}
	return c.Host
}

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
	return nil
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
	return cfg, nil
}

func (c *Config) Hyp() *hypconfig.Config {
	return &hypconfig.Config{
		Server: c.Server,
	}
}
