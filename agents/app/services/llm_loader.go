package services

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadLLMConfig 從 YAML 路徑讀取並解析 LLMConfig。
//
// 自動展開 ${ENV_VAR} 環境變數（出現於任何字串欄位中）。
// 找不到對應 ENV → 保留原 ${...} 字面值，由 client 端做後續驗證。
func LoadLLMConfig(path string) (*LLMConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("llm: read %s: %w", path, err)
	}
	return ParseLLMConfig(raw)
}

// ParseLLMConfig 將 YAML bytes 解析成 LLMConfig（先展開 ${ENV}）
func ParseLLMConfig(raw []byte) (*LLMConfig, error) {
	expanded := expandEnvVars(string(raw))

	var cfg LLMConfig
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("llm: parse yaml: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate 檢查 LLMConfig 必填欄位
func (c *LLMConfig) Validate() error {
	switch c.Mode {
	case "ollama":
		if c.Ollama == nil {
			return fmt.Errorf("llm: mode=ollama but ollama section missing")
		}
		if c.Ollama.URL == "" {
			return fmt.Errorf("llm: ollama.url required")
		}

	case "api":
		if c.API == nil {
			return fmt.Errorf("llm: mode=api but api section missing")
		}
		if c.API.Provider == "" {
			return fmt.Errorf("llm: api.provider required")
		}
		if c.API.Model == "" {
			return fmt.Errorf("llm: api.model required")
		}
		if c.API.Provider == "custom" && c.API.BaseURL == "" {
			return fmt.Errorf("llm: api.provider=custom requires base_url")
		}
		// api_key 可能為空（custom 自架）；交給 client 自行檢查

	case "none", "":
		// 允許未設；NewLLMClient 會回明確錯誤

	case "rag":
		// RAG 模式需要組合多個元件，不在本 client 範圍

	default:
		return fmt.Errorf("llm: unsupported mode %q", c.Mode)
	}
	return nil
}

// expandEnvVars 展開字串中的 ${VAR} 與 ${VAR:-default}；
// 不使用 os.ExpandEnv，因為它會把找不到的 ${VAR} 換成空字串，
// 容易出現「key 存在但值為空」的歧義；本函式保留行為：
//   - ${VAR}            → env 值；未定義則保留原字面，由 Validate 診斷
//   - ${VAR:-default}   → env 值；未定義或為空則使用 default
var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

func expandEnvVars(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		groups := envVarRe.FindStringSubmatch(match)
		name := groups[1]
		hasDefault := strings.Contains(match, ":-")
		defaultVal := groups[2]

		if v, ok := os.LookupEnv(name); ok && v != "" {
			return v
		}
		if hasDefault {
			return defaultVal
		}
		return match // 保留原 ${VAR}（無 default 時才保留，便於診斷）
	})
}
