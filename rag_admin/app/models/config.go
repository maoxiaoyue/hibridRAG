// Package models 持有 agent_client 的資料結構：client 設定（含持久化）與
// /api/chat、/api/ingest 的請求/回應 DTO。
package models

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ToolSpec 一個使用者登錄的本地工具（由本機 client 執行）。
//
//	Schema     : 給 LLM 看的 JSON Schema（object），描述參數。
//	Command    : 要執行的程式（絕對路徑或 PATH 中可找到的名稱）。
//	Args       : argv 樣板；元素中的 {{name}} 會以 tool_call 的對應參數替換。
//	             不經 shell 解析（避免注入）。
//	Background : true 表示這是「視窗程式」（記事本、瀏覽器、小畫家…）——只負責啟動，
//	             不等待它結束、不套用 TimeoutSec；否則一般命令列工具會被等到逾時砍掉。
type ToolSpec struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Schema         json.RawMessage `json:"schema,omitempty"`
	Command        string          `json:"command"`
	Args           []string        `json:"args,omitempty"`
	RequireConfirm bool            `json:"require_confirm"`
	TimeoutSec     int             `json:"timeout_sec"`
	Background     bool            `json:"background,omitempty"`
}

// Config client 全域設定。
//
// dev_pub_0.9：三個後端 URL（agents/graph/embedding）收斂成單一 GatewayAddr +
// APIKey——client 只認識 agent_gateway 一個位址，全部流量走 gRPC。
type Config struct {
	// GatewayAddr agent_gateway gRPC 位址（host:port，不帶 scheme），例 "127.0.0.1:9090"
	GatewayAddr string `json:"gateway_addr"`
	// APIKey gateway 簽發的 API Key（sak_ 開頭；一 Key 只能綁一台裝置）
	APIKey string `json:"api_key,omitempty"`
	// DeviceID 隨機 fallback 裝置 id——只在取不到 Windows MachineGuid 時產生並持久化；
	// 正常情況由機器特徵推導、不落地（見 services.StableDeviceID）
	DeviceID string `json:"device_id,omitempty"`

	AgentID       string     `json:"agent_id"`     // 例 agent-main
	InsecureTLS   bool       `json:"insecure_tls"` // 自簽憑證時需開（正式上線改 CA pinning）
	Tools         []ToolSpec `json:"tools"`
	WorkspaceRoot string     `json:"workspace_root,omitempty"` // 內建 Coding Agent 工具的根目錄；空 = 停用該功能
	Temperature   float32    `json:"temperature,omitempty"`    // 對話取樣溫度（0–1）；0 = 用 server 預設
	// KnownAgentIDs 用過的 RAG ID 清單（多 persona 部署時供下拉快速切換）。
	KnownAgentIDs []string `json:"known_agent_ids,omitempty"`
	// GraphInputBaseURL graphic_input 服務位址（內網 dev_090 專用，例
	// "https://127.0.0.1:8505"）；空 = 完全不建「Graph Input」分頁——
	// 外網（dev_pub_0.9）預設留空，外網 client 沒有直接寫圖譜的能力。
	GraphInputBaseURL string `json:"graph_input_base_url,omitempty"`
	// RegistryBaseURL agent_registry 服務位址（內網專用，例 "https://127.0.0.1:8500"）；
	// 供「Models」分頁讀取模型目錄。空 = 不建 Models 分頁（外網預設）。
	RegistryBaseURL string `json:"registry_base_url,omitempty"`
	// ThemeMode 介面主題："system"（跟隨作業系統）/ "dark" / "light"；空 = system。
	ThemeMode string `json:"theme_mode,omitempty"`
}

// RememberAgentID 把 id 記進 KnownAgentIDs（空字串或已存在時不動作）。
func (c *Config) RememberAgentID(id string) {
	if id == "" {
		return
	}
	for _, known := range c.KnownAgentIDs {
		if known == id {
			return
		}
	}
	c.KnownAgentIDs = append(c.KnownAgentIDs, id)
}

// AgentIDOptions 回傳下拉選項：KnownAgentIDs ∪ 目前 AgentID（保證目前值一定在列）。
func (c *Config) AgentIDOptions() []string {
	opts := append([]string{}, c.KnownAgentIDs...)
	for _, o := range opts {
		if o == c.AgentID {
			return opts
		}
	}
	if c.AgentID != "" {
		opts = append(opts, c.AgentID)
	}
	return opts
}

// DefaultConfig 回傳預設設定。
func DefaultConfig() *Config {
	return &Config{
		GatewayAddr: "127.0.0.1:9090",
		AgentID:     "agent-main",
		InsecureTLS: true,
	}
}

// AppDataDir 是 %APPDATA% 下的資料夾名稱（設定檔 + sessions）。
// LegacyAppDataDir 為改名前的舊名，供一次性遷移。
const (
	AppDataDir       = "rag_admin"
	LegacyAppDataDir = "agent_client"
)

// ConfigPath 回傳設定檔路徑（%APPDATA%\rag_admin\config.json；無 APPDATA 時退到工作目錄）。
func ConfigPath() string { return filepath.Join(appDataRoot(), AppDataDir, "config.json") }

func legacyConfigPath() string { return filepath.Join(appDataRoot(), LegacyAppDataDir, "config.json") }

func appDataRoot() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "."
	}
	return dir
}

// migrateLegacyConfig 一次性：若新的 rag_admin\config.json 不存在、但舊的
// agent_client\config.json 存在，就把它複製過來（保留 Gateway 位址 / API Key / 工具等，
// 不必重綁一 Key 一裝置）。best-effort，失敗不阻擋啟動。
func migrateLegacyConfig() {
	newP := ConfigPath()
	if _, err := os.Stat(newP); err == nil {
		return // 新檔已存在，不覆蓋
	}
	data, err := os.ReadFile(legacyConfigPath())
	if err != nil {
		return // 舊檔也沒有 → 全新安裝
	}
	if err := os.MkdirAll(filepath.Dir(newP), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(newP, data, 0o644)
}

// LoadConfig 讀取設定；檔案不存在時回傳預設值（不視為錯誤）。
func LoadConfig() (*Config, error) {
	migrateLegacyConfig() // 一次性把舊 agent_client 設定搬過來
	p := ConfigPath()
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("read config %s: %w", p, err)
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", p, err)
	}
	return cfg, nil
}

// Save 把設定寫回磁碟（自動建立目錄）。
func (c *Config) Save() error {
	p := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", p, err)
	}
	return nil
}
