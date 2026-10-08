// Package config（app/config）— 固定模型與 vault 設定，跟根 config/ 的
// server/database/grpc 設定分開放：這兩份是「業務規則」而非「基礎設施連線」，
// 比照 HypGo Human Inside 慣例讓非工程背景的人也能單獨調整。
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// OllamaConfig 固定模型設定：Obsidian 同步時的實體/關係抽取（chat）與實體
// 描述向量化（embedding）都用固定模型，不像 agents/ 的 ModelRegistry 可依
// persona 動態切換。直接打 Ollama HTTP API，不經過 agent_embedding。
type OllamaConfig struct {
	Chat      OllamaChatConfig      `yaml:"chat"`
	Embedding OllamaEmbeddingConfig `yaml:"embedding"`
}

type OllamaChatConfig struct {
	URL         string  `yaml:"url"`
	Model       string  `yaml:"model"`
	TimeoutSec  int     `yaml:"timeout_sec"`
	Temperature float32 `yaml:"temperature"`
}

func (c OllamaChatConfig) Timeout() time.Duration {
	if c.TimeoutSec <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.TimeoutSec) * time.Second
}

type OllamaEmbeddingConfig struct {
	URL        string `yaml:"url"`
	Model      string `yaml:"model"`
	Dimension  int    `yaml:"dimension"`
	TimeoutSec int    `yaml:"timeout_sec"`
}

func (c OllamaEmbeddingConfig) Timeout() time.Duration {
	if c.TimeoutSec <= 0 {
		return 30 * time.Second
	}
	return time.Duration(c.TimeoutSec) * time.Second
}

// LoadOllamaConfig 讀取 app/config/ollama.yaml，支援 ${VAR} 環境變數展開
func LoadOllamaConfig(path string) (*OllamaConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ollama config %s: %w", path, err)
	}
	expanded := os.ExpandEnv(string(raw))
	cfg := &OllamaConfig{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse ollama config: %w", err)
	}
	return cfg, nil
}
