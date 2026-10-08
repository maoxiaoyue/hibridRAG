# agent_registry — Agent Card 註冊中心

> 監聽埠：`:8500`（預設）
> 設定檔：`config/config.yaml`

---

## 功能概述

`agent_registry` 是整個 A2A 多 Agent 架構的 Agent Card 目錄服務。`agent_gateway` 解析請求裡的 `x-skill` 後，靠它把 skill 提示查成實際要轉發的下游 agent（URL / gRPC endpoint）。純被動接收服務——不會主動呼叫其他微服務。

---

## API 端點

| Method | Path | 說明 |
|--------|------|------|
| `POST` | `/registry/register` | 註冊/更新 agent（`Upsert`，以 `id` 為 key，`ON CONFLICT (id) DO UPDATE`） |
| `GET` | `/registry/agents/:id` | 依 `id` 精確查詢 |
| `GET` | `/registry/search?skill=` | 依 skill 查詢（無 `skill` 參數時回傳全部，依 `last_seen_at DESC` 排序） |
| `DELETE` | `/registry/agents/:id` | 移除註冊 |
| `POST` | `/registry/models/sync` | 從 Ollama（`/api/tags` + `/api/show`）探得目前可用模型，upsert 進 `llm_models` |
| `GET` | `/registry/models` | 列出已記錄的模型（含 modalities，供 client 選 model 的下拉來源） |
| `GET` | `/registry/models/:name` | 依名稱查單一模型 |
| `GET` | `/health` | 健康檢查 |

**注意**：目前註冊完全是手動的（服務啟動時不會自動呼叫 `/registry/register`），沒有心跳/TTL 機制自動淘汰久未更新的 registration；要清掉過期/測試殘留資料只能手動 `DELETE`。

## Model Catalog（LLM 模型清單 + modalities，2026-07-07）

記錄「LLM 目前可用哪些模型、每顆能吃什麼內容型態（text / image / audio）」，是 `agent_client`
的「Models」分頁選 model 的事實來源。**不是** agents 推理時挑模型的來源——agents 仍用自己的
`ModelRegistry`（`agents/app/services/model_registry.go`）；本目錄純為「記錄 + 顯示」。

- 事實來源 = 打 Ollama（`config.yaml` 的 `ollama.host`，預設 `192.168.50.104:11434`）：
  `POST /registry/models/sync` 觸發探測 → `OllamaProber` 讀 `/api/tags`（清單）+ `/api/show`
  （capabilities / model_info 上下文長度）→ 推導 modalities（text 恆真、`vision`→image、
  `audio`→audio 預留；capabilities 缺漏時以 details.families 的 clip/mllama/llava/qwen*vl 補推 image）
  → upsert 進 `llm_models`。
- 資料模型 `app/models/model_catalog.go` 的 `LLMModel`（bun 表 `llm_models`）：
  `Name`(pk) / `Host` / `Modalities{Text,Image,Audio}` / `Capabilities []string` /
  `Family` / `ParameterSize` / `Quantization` / `SizeBytes` / `ContextLen` / `*_at`。
- 相關檔案：`app/services/ollama_probe.go`（探測 + 推導，含純函式單元測試 `ollama_probe_test.go`）、
  `app/services/model_catalog_service.go`（Sync/List/Get）、`app/controllers/model_catalog.go`
  （host 由 `commands.setupRoutes` 以 `controllers.SetModelCatalogHost` 注入）。
- 遷移：`models.MigrateModelCatalog(db)` 於 `commands/root.go` 的 AutoMigrate 階段建表（與
  `agent_registrations` 分開）。

---

## 資料模型 (`app/models/registration.go`)

```go
type AgentSkill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type AgentRegistration struct {
	ID           string       `bun:"id,pk"`          // 唯一鍵
	Name         string
	URL          string
	GRPCEndpoint string
	Version      string
	Skills       []AgentSkill `bun:"skills,type:jsonb"` // 全域自由字串，無 agent 命名空間隔離、無唯一性約束
	Capabilities AgentCapabilities
	LastSeenAt   time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
```

`Skills` 是純 JSONB 陣列，只有 GIN index 加速查詢，沒有任何 constraint 防止兩個不同 agent 註冊相同/相似的 skill id。

---

## 目錄結構

```
agent_registry/
├── main.go                        # 入口：呼叫 commands.Execute()
├── config/config.yaml
├── app/
│   ├── commands/root.go           # Cobra root + serve 子命令（含 model catalog 路由 + host 注入）
│   ├── controllers/
│   │   ├── registry.go            # Register/GetAgent/SearchAgents/Deregister
│   │   └── model_catalog.go       # SyncModels/ListModels/GetModel（2026-07-07）
│   ├── services/
│   │   ├── registry_service.go
│   │   ├── ollama_probe.go        # OllamaProber：/api/tags + /api/show → modalities（2026-07-07）
│   │   └── model_catalog_service.go
│   └── models/
│       ├── registration.go        # AgentRegistration + AutoMigrate
│       └── model_catalog.go       # LLMModel（llm_models 表）+ MigrateModelCatalog（2026-07-07）
└── scripts/gencert/main.go        # 自簽 TLS 憑證產生器（複製自 agents/）
```

---

## 依賴

- PostgreSQL（唯一持久化，`AutoMigrate` 建表 + GIN index，見 `config.yaml` 的 `database.dsn`）
- 不依賴其他微服務

---

## 修正紀錄

已遷移至專案 [memory/20260703_log.md](../memory/20260703_log.md)（「一、agent_gateway 路由修正」「二、全服務 HTTP/2 + 真 TLS」節）。
