// Package models 持有 mobile_client 的資料結構：client 設定與後端 DTO。
// 設定的持久化在 app/store（AES-GCM 加密 JSON），本套件只有純資料結構。
package models

// Config client 全域設定（桌面版 pub_client Config 的手機精簡版：
// 沒有本機工具 / Coding Agent workspace / Vault 資料夾）。
type Config struct {
	// GatewayAddr agent_gateway gRPC 位址（host:port，不帶 scheme），例 "192.168.50.103:9090"
	GatewayAddr string `json:"gateway_addr"`
	// APIKey gateway 簽發的 API Key（sak_ 開頭；一 Key 只能綁一台裝置）
	APIKey string `json:"api_key,omitempty"`
	// DeviceID 首次啟動產生的隨機裝置 id（Android 取不到穩定機器特徵，
	// 一律持久化隨機 ID；重灌 app 視同新裝置，符合一 Key 一裝置語意）
	DeviceID string `json:"device_id,omitempty"`

	AgentID     string  `json:"agent_id"`               // 例 agent-main
	InsecureTLS bool    `json:"insecure_tls"`           // 自簽憑證時需開（正式上線改 CA pinning）
	Temperature float32 `json:"temperature,omitempty"`  // 對話取樣溫度（0–1）；0 = 用 server 預設
	// KnownAgentIDs 用過的 RAG ID 清單（多 persona 部署時供下拉快速切換）。
	KnownAgentIDs []string `json:"known_agent_ids,omitempty"`
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
		GatewayAddr: "",
		AgentID:     "agent-main",
		InsecureTLS: true,
	}
}
