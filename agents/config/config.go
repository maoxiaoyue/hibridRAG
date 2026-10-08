package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	hypconfig "github.com/maoxiaoyue/hypgo/pkg/config"

	"agent/internal/cache"
	"agent/internal/database"
	"agent/internal/logger"
)

// Config 專案層組態；不嘗試覆蓋 hypgo 的 Server/Logger 介面，只把各子系統設定聚在一起
type Config struct {
	Server      hypconfig.ServerConfig `yaml:"server"`
	Database    database.Config        `yaml:"database"`
	Redis       cache.Config           `yaml:"redis"`
	Logger      logger.Config          `yaml:"logger"`
	Skills      SkillsConfig           `yaml:"skills"`
	GRPC        GRPCConfig             `yaml:"grpc"`
	Agent       AgentConfig            `yaml:"agent"`
	Embedding   EmbeddingConfig        `yaml:"embedding"`
	Memory      MemoryConfig           `yaml:"memory"`
	EventBus    EventBusConfig         `yaml:"eventbus"`
	Context     ContextConfig          `yaml:"context"`
	Retrieval   RetrievalConfig        `yaml:"retrieval"`
	Tools       ToolsConfig            `yaml:"tools"`
	Ingest      IngestConfig           `yaml:"ingest"`
	ChatSession ChatSessionConfig      `yaml:"chat_session"`
	Registry    RegistryConfig         `yaml:"registry"`
	Critic      CriticConfig           `yaml:"critic"`
	Graph       GraphConfig            `yaml:"graph"`
	Browser     BrowserConfig          `yaml:"browser"`
	Monitoring  MonitoringConfig       `yaml:"monitoring"`
}

// MonitoringConfig 監控相關設定。yaml 的 monitoring: 段既有的 metrics/trace 欄位
// 是 scaffold 佔位（尚無對應實作），這裡只映射實際使用的 gpu 子段。
type MonitoringConfig struct {
	GPU GPUMonitorConfig `yaml:"gpu"`
}

// GPUMonitorConfig Ollama/GPU 使用狀況監控（app/services/gpu_monitor.go）。
type GPUMonitorConfig struct {
	// Enabled 未設 → 啟用（有可觀測的 Ollama URL 時）；明確 false 才關閉。
	Enabled *bool `yaml:"enabled"`
	// OllamaURLs 要觀測 /api/ps 的 Ollama base URL 清單；空 → 用 llm.yaml 的 chat URL
	OllamaURLs []string `yaml:"ollama_urls"`
	// IdleIntervalSec 空閒背景觀測間隔（秒）；<=0 → 600（10 分鐘）
	IdleIntervalSec int `yaml:"idle_interval_sec"`
	// StaleAfterSec 推理前檢查：快取超過此秒數才重新取樣；<=0 → 30
	StaleAfterSec int `yaml:"stale_after_sec"`
	// NvidiaSMI 未設 → 嘗試本機 nvidia-smi（找不到執行檔自動停用）；明確 false 不嘗試
	NvidiaSMI *bool `yaml:"nvidia_smi"`
	// UtilWarnPercent 本機 GPU 使用率警告門檻；<=0 → 95
	UtilWarnPercent int `yaml:"util_warn_percent"`
	// VRAMWarnPercent 本機 VRAM 佔用率警告門檻；<=0 → 90
	VRAMWarnPercent int `yaml:"vram_warn_percent"`
}

// BrowserConfig web_browse 工具用的遠端瀏覽器（browserless）連線設定。
// WSURL 留空 → 不註冊 web_browse 工具（部署環境有 browserless sidecar 才啟用）。
type BrowserConfig struct {
	// WSURL browserless 的 WebSocket 位址，例 "ws://localhost:33000"；空 = 停用
	WSURL string `yaml:"ws_url"`
	// NavTimeoutSec 導頁 + 抓取逾時（秒），預設 45
	NavTimeoutSec int `yaml:"nav_timeout_sec"`
	// MaxBodyChars 送給摘要 LLM 的 body 字數上限（避免爆 context），預設 12000
	MaxBodyChars int `yaml:"max_body_chars"`
}

// GraphConfig agent_graph 服務（A4 Knowledge Agent）的 gRPC 連線設定。
// GRPCAddr 留空 → 停用 graph_query 工具（行為同加入此功能前）。
type GraphConfig struct {
	// GRPCAddr agent_graph service 的 gRPC 位址，例如 "localhost:8512"
	GRPCAddr string `yaml:"grpc_addr"`
	// InsecureSkipVerify 自簽憑證（dev）時跳過 TLS 驗證（ca_file 設定後無效）
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
	// ClientCertFile / ClientKeyFile 內部 mTLS 呼叫端憑證（CN=agents）；dev 可留空
	ClientCertFile string `yaml:"client_cert_file"`
	ClientKeyFile  string `yaml:"client_key_file"`
	// CAFile 內部 CA 憑證路徑；非空時用它完整驗證 server 憑證
	CAFile string `yaml:"ca_file"`
	// DialTimeoutSec gRPC 連線逾時（秒），預設 5
	DialTimeoutSec int `yaml:"dial_timeout_sec"`
	// CallTimeoutSec 單次 RPC 呼叫逾時（秒），預設 5
	CallTimeoutSec int `yaml:"call_timeout_sec"`
}

// CriticConfig agent_critic 服務（A3 Critic / Gatekeeper）的 gRPC 連線設定。
// GRPCAddr 留空 → 停用答案品質評估（行為同加入此功能前）。
type CriticConfig struct {
	// GRPCAddr agent_critic service 的 gRPC 位址，例如 "localhost:8511"
	GRPCAddr string `yaml:"grpc_addr"`
	// InsecureSkipVerify 自簽憑證（dev）時跳過 TLS 驗證（ca_file 設定後無效）
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
	// ClientCertFile / ClientKeyFile 內部 mTLS 呼叫端憑證（CN=agents）；dev 可留空
	ClientCertFile string `yaml:"client_cert_file"`
	ClientKeyFile  string `yaml:"client_key_file"`
	// CAFile 內部 CA 憑證路徑；非空時用它完整驗證 server 憑證
	CAFile string `yaml:"ca_file"`
	// DialTimeoutSec gRPC 連線逾時（秒），預設 5
	DialTimeoutSec int `yaml:"dial_timeout_sec"`
	// CallTimeoutSec 單次 RPC 呼叫逾時（秒），預設 5
	CallTimeoutSec int `yaml:"call_timeout_sec"`
	// MaxRevisions 評分未通過時最多重新生成幾次答案；0（預設）= 只評分不重生。
	// 每次 revision 都是一次完整的 LLM 呼叫（含 tool loop），會增加延遲與成本。
	MaxRevisions int `yaml:"max_revisions"`
}

// RegistryConfig agent_registry 自動註冊設定。URL 留空 → 停用自動註冊
// （行為同加入此功能前，需要的話仍可手動 POST /registry/register）。
type RegistryConfig struct {
	// URL agent_registry base URL（例 "https://localhost:8500"）
	URL string `yaml:"url"`
	// HeartbeatSec 心跳（重新註冊）間隔秒數；<=0 → 60。
	// registry 的 Search() 同分時依 last_seen_at DESC，心跳讓活著的實例勝出。
	HeartbeatSec int `yaml:"heartbeat_sec"`
	// PublicURL 註冊 payload 的對外 A2A URL；留空 → 用 Agent Card 推導的 URL
	PublicURL string `yaml:"public_url"`
	// GRPCEndpoint 對外 gRPC endpoint（host:port）；留空 → 依 grpc.addr 推導
	GRPCEndpoint string `yaml:"grpc_endpoint"`
	// InsecureSkipVerify 自簽憑證（dev）時跳過 TLS 驗證
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
}

// ChatSessionConfig 短期記憶（POST /api/chat 多輪對話歷史，Redis-backed）設定。
// Redis 未連上時自動停用（main.go 的 cache.GetClient() 回 nil）。
type ChatSessionConfig struct {
	// MaxTurns 每個 session 最多保留幾則訊息（user+assistant 各算一則）；<=0 → 40
	MaxTurns int `yaml:"max_turns"`
	// TTLSec 每次寫入後刷新的過期秒數；<=0 → 7200（2 小時）
	TTLSec int `yaml:"ttl_sec"`
}

// IngestConfig 文件上傳 / 攝取設定。
type IngestConfig struct {
	// UploadsDir 上傳檔案落地目錄；未設定時使用 "./uploads"
	UploadsDir string       `yaml:"uploads_dir"`
	Vision     VisionConfig `yaml:"vision"`
}

// VisionConfig 用視覺 LLM（Ollama）把圖片抽成文字的設定。
//
// Model / Role 二擇一（同 OllamaConfig 規則）：
//   - Model 非空 → 直接使用
//   - Model 空、Role 非空 → 由 ModelRegistry 依用途解析（如 "vision" / "vision_uncensored"）
//   - OllamaURL 空且 Role 解析成功 → 用 registry 給的 host
type VisionConfig struct {
	Enabled    bool   `yaml:"enabled"`
	OllamaURL  string `yaml:"ollama_url"`  // 例 http://127.0.0.1:11434；空 → registry 預設 host
	Model      string `yaml:"model"`       // 視覺模型；空且 Role 設定 → registry 解析
	Role       string `yaml:"role"`        // 視覺用途（vision / vision_uncensored）
	Prompt     string `yaml:"prompt"`      // 抽文字提示；空 → 用內建預設
	TimeoutSec int    `yaml:"timeout_sec"` // <=0 → 120
}

// ToolsConfig 控制 Tool Calling 迴圈行為。
type ToolsConfig struct {
	MaxIterations  int `yaml:"max_iterations"`   // <=0 → 8
	ToolTimeoutSec int `yaml:"tool_timeout_sec"` // <=0 → 30
}

// RetrievalConfig HybridSearcher（RAG）行為調整；未設或 0 → 用 retrieval 套件 defaults。
type RetrievalConfig struct {
	DenseTopN           int     `yaml:"dense_top_n"`          // 預設 20
	SparseTopN          int     `yaml:"sparse_top_n"`         // 預設 20
	FinalTopK           int     `yaml:"final_top_k"`          // 預設 5（會被 ContextBuilder.RAG 配額覆蓋）
	RRFK                int     `yaml:"rrf_k"`                // 預設 60
	SimilarityThreshold float32 `yaml:"similarity_threshold"` // 預設 0（不過濾）
}

// ContextConfig ContextBuilder 預設配額（可被 SKILL.md Identity context_* 覆蓋）
type ContextConfig struct {
	Personal   int `yaml:"personal_memory"`    // 預設 1
	Procedural int `yaml:"procedural_memory"`  // 預設 3
	RAG        int `yaml:"rag_chunks"`         // 預設 5
	Episodic   int `yaml:"episodic_memory"`    // 預設 2
	Semantic   int `yaml:"semantic_memory"`    // 預設 3（LongTermMemory ANN top-K）
	MaxChars   int `yaml:"max_context_chars"`  // 預設 16000（粗略字元上限）
	MaxTokens  int `yaml:"max_context_tokens"` // 預設 4000（用 TokenCounter 估算）
}

// EventBusConfig 對應架構 v2 的 ⚡ Event Bus 元件（Redis Stream）
type EventBusConfig struct {
	Enabled            bool   `yaml:"enabled"`
	StreamIngestStart  string `yaml:"stream_ingest_started"`
	StreamIngestDone   string `yaml:"stream_ingest_completed"`
	StreamIngestFailed string `yaml:"stream_ingest_failed"`
	MaxLen             int64  `yaml:"max_len"`
	PublishTimeoutSec  int    `yaml:"publish_timeout_sec"`
}

// EmbeddingConfig 向量嵌入服務設定
type EmbeddingConfig struct {
	// Mode "http" 表示透過 agent_embedding HTTP service；空字串或其他值表示不啟用
	Mode string `yaml:"mode"`
	// HTTPBaseURL agent_embedding service 的 base URL，例如 "https://localhost:8502"
	HTTPBaseURL string `yaml:"http_base_url"`
	// TimeoutSec HTTP 請求超時（秒），預設 30
	TimeoutSec int `yaml:"timeout_sec"`

	// Provider BGE-M3 直連 embedding 後端（"ollama" | "tei"），供 semantic memory 用；
	// 空字串 → 停用 semantic memory 的自動 embedding（memory_write kind=semantic 會失敗）。
	Provider string `yaml:"provider"`
	// TEIEndpoint BGE-M3 服務位址（Ollama 或 TEI 皆可，依 Provider 決定協定）
	TEIEndpoint string `yaml:"tei_endpoint"`
	// Model embedding 模型 tag，預設 "bge-m3"
	Model string `yaml:"model"`
}

// MemoryConfig agent_memory 服務的 gRPC 連線設定。所有記憶資料的存取一律
// 經由這個 gRPC 端點打 agent_memory；本服務不再直連 Postgres 存記憶。
type MemoryConfig struct {
	// GRPCAddr agent_memory service 的 gRPC 位址，例如 "localhost:8510"。必填——
	// 留空或撥號失敗時服務會直接啟動失敗（見 main.go）。
	GRPCAddr string `yaml:"grpc_addr"`
	// InsecureSkipVerify 是否跳過 TLS 憑證驗證（agent_memory 用自簽憑證，dev 預設 true；
	// ca_file 設定後此欄位無效——一律做完整驗證）
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
	// ClientCertFile / ClientKeyFile 內部 mTLS 呼叫端憑證（CN=agents）；dev 可留空
	ClientCertFile string `yaml:"client_cert_file"`
	ClientKeyFile  string `yaml:"client_key_file"`
	// CAFile 內部 CA 憑證路徑；非空時用它完整驗證 server 憑證
	CAFile string `yaml:"ca_file"`
	// DialTimeoutSec gRPC 連線逾時（秒），預設 5
	DialTimeoutSec int `yaml:"dial_timeout_sec"`
	// CallTimeoutSec 單次 RPC 呼叫逾時（秒），預設 5
	CallTimeoutSec int `yaml:"call_timeout_sec"`
}

// SkillsConfig skill runtime 設定
type SkillsConfig struct {
	Dir string `yaml:"dir"` // skills/ 根目錄；未設定時使用 "./skills"
}

// GRPCConfig gRPC server 設定
type GRPCConfig struct {
	// Addr は gRPC server の listen アドレス（例: ":9091"）
	// 未設定時は ":9091" を使用
	Addr string `yaml:"addr"`
	// Worker pool（推理任務限流）
	Workers   int `yaml:"workers"`    // <=0 → 8
	QueueSize int `yaml:"queue_size"` // <=0 → workers*4
	// CAFile 內部 CA 憑證路徑（dev_pub_0.9 mTLS）；非空即要求並驗證呼叫端 client cert
	CAFile string `yaml:"ca_file"`
	// AllowedClientCNs mTLS 啟用時放行的呼叫端 CN 允許清單；空清單 = 拒絕全部（安全預設）
	AllowedClientCNs []string `yaml:"allowed_client_cns"`
	// TrustedIPs 信任的內網來源 IP/CIDR（dev_090 通道，如 "192.168.50.103"）——
	// 命中即免 client cert 放行；非空時 mTLS 握手降為 VerifyClientCertIfGiven
	TrustedIPs []string `yaml:"trusted_ips"`
}

// AgentConfig 本 agent 固有の設定
type AgentConfig struct {
	// ID は OutboundMsg.From に使用される本 agent の識別子
	// 未設定時は "agent-main" を使用
	ID string `yaml:"id"`
	// PoolID 本 agent 所屬的 shared memory pool；空字串表示不加入任何 pool
	// （shared_read/shared_write 工具會擋掉，因為沒有預設 pool_id 可用）。
	// 同一個 pool_id 下的所有 agent 都能讀寫彼此的 shared memory。
	PoolID string `yaml:"pool_id"`
}

// Load 讀取 YAML 設定檔；支援 ${VAR} 環境變數展開
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	expanded := os.Expand(string(raw), func(key string) string {
		// ${VAR} / $VAR → 環境變數；無設定則空字串
		return os.Getenv(strings.TrimSpace(key))
	})

	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

// Hyp 組出給 hypgo server.New 使用的 config.Config
// 只搬 Server 區塊（hypgo server 啟動僅讀這塊）
func (c *Config) Hyp() *hypconfig.Config {
	return &hypconfig.Config{
		Server: c.Server,
	}
}
