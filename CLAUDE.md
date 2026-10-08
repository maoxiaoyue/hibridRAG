# Single Agent Platform — 專案根目錄指南

> 最後更新：2026-07-06
> 架構版本：v2（7 微服務 + 桌面 client；參見 [TODO4.md](TODO4.md) 與 [docs/optimized_multi_agent_architecture_v2.svg](docs/optimized_multi_agent_architecture_v2.svg)）
> **本分支（dev_pub_0.9）為外網部署版**：client 全部流量收斂到 agent_gateway gRPC + API Key 裝置綁定認證——設計與實作紀錄見下方「dev_pub_0.9 外網部署架構設計」（2026-07-06 實作完成，端到端驗證待部署機）；部署步驟見 [deploy/README-deploy.md](deploy/README-deploy.md)

本檔說明 `D:\GoProjects\single_agent\` 下所有子目錄的職責與彼此關係。微服務細節請看各服務自己的 `README.md` / `CLAUDE.md`。

---

# 紀錄

- 每次開發，必須將開發過程輸出一份到 .\memory\YYYYMMdd_log.md
- 每個session開始前，檢視TODO*.md，確認現在的進度

## 整體拓樸（dev_pub_0.9 外網版，2026-07-06 起）

```
        外網                                      內網（dev_090 通道：192.168.50.103 免 Key/免憑證）
pub_client（Windows Fyne，設定/session 存 DuckDB 加密庫）
mobile_client（Android Fyne，設定/session 存 AES-GCM 加密 JSON）
        ║ gRPC + TLS + API Key（一 Key 一裝置 TOFU 綁定）
        ▼
agent_gateway/ (:9090 gRPC ClientGateway，唯一對外；:8090 A2A HTTP 限內網)
        │ 內部 mTLS gRPC（client cert CN 允許清單 或 trusted_ips）
        ├────────────────► agents/       (:9091 gRPC / :18080 HTTP，Chat/UploadIngest/GetIngestJob)
        ├────────────────► agent_graph/  (:8512 gRPC / :8504 HTTP，查詢 + proposed 審核)
        └─ 內部 HTTPS ───► agent_embedding/ (:8502，VerifySearch)
agents/ ── mTLS gRPC ─► agent_memory/ (:8510 gRPC / :8501 HTTP)、agent_critic/ (:8511 / :8503)、agent_graph/ (:8512)
        └── REST ──────► agent_registry/ (:8500，Agent Card 目錄)
graphic_input/ (:8505 HTTP / :8513 gRPC，內網專用) ── gRPC ─► agent_graph/（人工輸入：Obsidian/PG 雙路徑）
                                       │
                                       └── Postgres（192.168.50.201）/ Redis / Cassandra（infra/）
                                       └── Postgres（192.168.50.104，agent_graph 專用：Apache AGE + pgvector）
                                       └── Ollama（.104:11434 LLM/vision；.201:11434 BGE-M3）
```

---

## 子目錄一覽

### 微服務（每個都是獨立 Go module）

| 目錄                                   | 角色                                                                                                                                                                                                                                                    | 對外埠                         | 對應架構元件                         |
| ------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------- | ------------------------------ |
| [agent_gateway/](agent_gateway/)     | A2A 對外唯一入口；解析 `x-skill` → 查 registry → 轉發 JSON-RPC                                                                                                                                                                                                    | `:8090` HTTP                | A2A Gateway                    |
| [agents/](agents/)                   | Agent Core：gRPC Inbound、ContextBuilder、HybridSearcher（RRF）、Tool Loop、Episodic 學習迴圈、ModelRegistry（chat/vision/embedding 14 顆 model spec）                                                                                                               | `:9091` gRPC + `:8081` HTTP | Agent Runtime                  |
| [agent_registry/](agent_registry/)   | Agent Card 註冊中心；提供 `/registry/register`、`/registry/search?skill=`、`/registry/agents/:id` 給 gateway 做 skill→agent 路由                                                                                                                                   | `:8500` HTTP                | Service Registry               |
| [agent_memory/](agent_memory/)       | 6 類記憶（personal / procedural / episodic / tool / shared 於 PG，long_term(semantic) 於 Cassandra + BGE-M3 embedding + SAI ANN）；REST CRUD + gRPC MemoryService（供 `agents` 呼叫）                                                                               | `:8501` HTTP + `:8510` gRPC | Memory Service                 |
| [agent_embedding/](agent_embedding/) | BGE-M3 dense embedding + Cassandra ANN + PG FTS + RRF Hybrid Search；Event Bus 消費端                                                                                                                                                                     | `:8502` HTTP                | Embedding + Vector Search      |
| [agent_critic/](agent_critic/)       | 答案品質評估（completeness / grounding / faithfulness + 修訂建議；基線 LexicalScorer，之後換 LLM-as-judge）；評估紀錄落 PG。`agents` 的 gRPC Inbound（`process()`）路徑已接線並跑 revision loop；`/api/chat` 同步路徑尚未                                                                        | `:8503` HTTP + `:8511` gRPC | A3 Critic / Gatekeeper         |
| [agent_graph/](agent_graph/)         | 知識圖譜（**embedded Kùzu 唯一後端**，圖+向量都在 `data/graph.kz`；AGE/pgvector 於 2026-07-08 移除）+ Obsidian vault **雙向**——vault→圖譜同步（fsnotify + WikiLink + LLM 抽取待審）、圖譜→vault 鏡像匯出（`entities/*.md`，寫入即增量同步）、vault .md 下載（VaultManifest/VaultFetch server-streaming，供 client Sync Vault）；查詢回填全圖 degree；直接打 Ollama（不經 agent_embedding）。PG（`192.168.50.104`）只剩 proposed_relations/graph_sync_state。`agents` 的 `graph_query` 工具已接線 | `:8504` HTTP + `:8512` gRPC | A4 Knowledge Agent / Graph RAG |
| [graphic_input/](graphic_input/)     | 知識圖譜人工輸入（**內網 dev_090 專用**，外網不部署）：結構化實體/關係輸入 + 文件雙路徑（Obsidian vault 筆記 / PG 直接抽取）；直連 agent_graph                                                                                                                                              | `:8505` HTTP + `:8513` gRPC | Graph Manual Input（內網）     |
| [pub_client/](pub_client/)           | 外網 Windows 桌面 client（Go + Fyne / CGO，原名 agent_client）；全部流量經 gateway gRPC + API Key；設定與對話 session 存 DuckDB 加密庫（%APPDATA%\pub_client\store.duckdb）                                                                                                          | —                           | End-user Client（外網）        |
| [mobile_client/](mobile_client/)     | **Android 手機版 client**（Go + Fyne v2.5.5，2026-07-09 由 pub_client 移植）：Chat / Upload / Graph / Settings 四分頁，走同一條 gateway gRPC + API Key；平台替代——DuckDB→AES-GCM 加密 JSON、MachineGuid device_id→隨機持久化、SAF 選檔 in-memory；本機工具/Coding Agent/Vault 不移植。`fyne package -os android/arm64` 出 APK；詳見 [mobile_client/README.md](mobile_client/README.md) 與 [mobile_client/CLAUDE.md](mobile_client/CLAUDE.md) | —                           | End-user Client（Android）     |

> 各服務都用同一套底層：**HypGo v0.8.11**（routing + DI）+ **hidb**（PG/Redis/Cassandra 統一連線層）。

> `agents` 服務的 ContextBuilder / EpisodicWriter / memory_read·memory_write 工具 / IngestRunner **已改為透過 gRPC 呼叫 `agent_memory`**（2026-07-03，見 [agent_memory/CLAUDE.md](agent_memory/CLAUDE.md) 與 [agents/CLAUDE.md](agents/CLAUDE.md) 的修正紀錄）——取代了先前直接打同一個 Postgres DSN 的重複實作。**`agents/config/config.yaml` 的 `memory.grpc_addr` 現在是必填**（同日稍晚移除了留空時 fallback 回直連 DB 的過渡期相容邏輯）；`agents` 自己的 `/api/memory/*` REST 代理（`agents/app/controllers/memory.go`）也已一併刪除——`agents` 對記憶資料的存取現在只剩 gRPC → `agent_memory` 這一條路徑，需要記憶 CRUD-by-id 的呼叫端請直接打 `agent_memory` 的 REST（`:8501`）或 gRPC（`:8510`）。
> 
> **記憶機制完整度**（2026-07-03 稽核 + 落實，6 類分類）：personal/procedural/episodic/**semantic**/**shared** 都已實作且接進推理流程；**short-term**（`/api/chat` 多輪對話歷史，Redis-backed `SessionStore`）也已補上。過程中發現並修好 `agent_memory` 的 `LongTermMemory`（semantic memory 的底層）兩個從沒被跑過的潛伏 bug：Cassandra 表從未建立、gocql 無法直接 marshal `[]float32` 進向量欄位。詳見 [agents/CLAUDE.md](agents/CLAUDE.md) 與 [agent_memory/CLAUDE.md](agent_memory/CLAUDE.md) 的修正紀錄。

### 子目錄內部慣例（agents 為代表）

`agents/` 目錄內主要結構：

| 路徑                                                                             | 內容                                                                                                                                                                         |
| ------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `main.go`                                                                      | 啟動入口：建 ModelRegistry → 配線 LLM/Vision/Embedding → 起 gRPC + HTTP server                                                                                                      |
| `app/controllers/`                                                             | HTTP / gRPC handler（`agent_grpc.go` 為核心：process() 串 RAG → ToolLoop → Episodic 寫回）                                                                                          |
| `app/services/`                                                                | 業務邏輯：`model_registry.go`、`tool*.go`（Tool framework + 內建 + MCP client + RunToolLoop）、`llm_*.go`（多 provider）、`retry.go`、`worker_pool.go`、`trace_meta.go`、`memory_service.go` |
| `app/models/`                                                                  | proto 生成的 `*.pb.go` + DTO                                                                                                                                                  |
| `app/routers/`                                                                 | HTTP 路由註冊                                                                                                                                                                  |
| `app/middleware/`                                                              | gRPC interceptor、HTTP middleware                                                                                                                                           |
| `internal/agent/`                                                              | Agent 核心：`context/`（ContextBuilder + Token 預算）、`retrieval/`（HybridSearcher RRF）、`persona/`、`vectordb/`                                                                     |
| `internal/database/` `internal/cache/` `internal/eventbus/` `internal/logger/` | 基礎設施 wrapper（皆走 hypgo/pkg/hidb）                                                                                                                                            |
| `cmd/probe/` `cmd/inbound-test/` `cmd/llm-demo/`                               | 獨立 CLI 工具（健康檢查 / gRPC 連發測試 / LLM 直連 demo）                                                                                                                                  |
| `proto/agent.proto`                                                            | A2A / Inbound / Outbound 契約來源                                                                                                                                              |
| `config/config.yaml` `config/llm.yaml` `config/llm.local.yaml`                 | 主設定 + LLM provider（remote Ollama / local llama.cpp）                                                                                                                        |
| `migrations/`                                                                  | PG `001-004_*.sql` + `cassandra/` DDL                                                                                                                                      |
| `skills/`                                                                      | SKILL.md（persona + skillpack）來源；目前有 `demo/`                                                                                                                                |
| `certs/`                                                                       | 自簽 TLS（gRPC 用）                                                                                                                                                             |
| `uploads/`                                                                     | 文件上傳暫存（IngestRunner 入口）                                                                                                                                                    |
| `logs/`                                                                        | 執行期日誌                                                                                                                                                                      |
| `tests/`                                                                       | 整合測試（`//go:build integration` 標籤；連真 PG/Redis/Cassandra）                                                                                                                    |
| `scripts/gencert/`                                                             | 自簽 TLS 產生器                                                                                                                                                                 |
| `docker-compose.yml` `Dockerfile` `Makefile`                                   | 容器與本機開發                                                                                                                                                                    |
| `AGENTS.md` `GEMINI.md` `CLAUDE.md`                                            | 多家 AI agent 的工作筆記（[CLAUDE.md](agents/CLAUDE.md) 為主）                                                                                                                        |

其他微服務（`agent_gateway` / `agent_memory` / `agent_embedding` / `agent_registry`）結構相同：`app/{controllers,services,models,routers}` + `internal/{db,cache,eventbus,logger}` + `config/` + `migrations/`。

### 共享資源

| 目錄                    | 用途                                                                                                                                                                                                                                |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [infra/](infra/)      | 基礎設施 Dockerfile。目前只有 `postgres/`（PG + pgvector 自訂 image，`build.sh` 一鍵建）                                                                                                                                                           |
| [docs/](docs/)        | 架構圖（`.mmd` 原始 + `.svg` 輸出：`multi_agent_architecture` / `optimized_multi_agent_architecture_v2` / `mvp_architecture` / `agent_memory` / `hybrid_rag_architecture`）+ 專案文件（`setup.html` / `Agentic、Graphic RAG Hybrid.md`，見下方「專案文件」表；`TODO4.md`/`TODO.html` 現位於根目錄） |
| [memory/](memory/)    | 專案層級的工作日誌（`YYYYMMDD_log.md`）。**注意**：這是專案內歷史紀錄，不是 user 級 auto-memory（auto-memory 在 `~/.claude/projects/.../memory/`）                                                                                                               |
| [.claude/](\.claude/) | Claude Code 專案層 settings（`settings.local.json`）                                                                                                                                                                                   |

### 專案文件

| 檔案                                                                           | 用途                                                                                                                                    |
| ---------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| [TODO4.md](TODO4.md)                                                          | **主要待辦清單**（整合 TODO/TODO2/TODO3）。每次工作前先看這個                                                                                             |
| [TODO.html](TODO.html)                                                        | TODO4 的視覺版（同步輸出，可不維護）                                                                                                                 |
| [docs/setup.html](docs/setup.html)                                           | K8s + containerd 部署指南（4 張 SVG、port 分配、nerdctl build、Manifests 範例）                                                                     |
| [docs/Agentic、Graphic RAG Hybrid.md](docs/Agentic、Graphic%20RAG%20Hybrid.md) | Agentic + Graph RAG 混合架構設計文件（v0.3；A3 Critic 已落地為 agent_critic 且已接線，A4 Knowledge Agent 已落地為 agent_graph 且已接線；兩者都還沒向 agent_registry 註冊） |
| [docs/hybrid_rag_architecture.svg](docs/hybrid_rag_architecture.svg)          | 上述混合架構的視覺圖解，含 Agent 1-4 角色分工標示：Agent 1（ContextBuilder）/ Agent 2（ToolLoop）如何分別呼叫 Agentic RAG（agent_memory + agent_embedding）與 Graph RAG（Agent 4 = agent_graph），以及 Agent 3（agent_critic）revision loop |
| [docs/hybrid_rag_architecture.md](docs/hybrid_rag_architecture.md)           | 上述 SVG 的文字說明版：Agent 1-4 角色表、流程圖、兩種 RAG 機制細節、Critic revision loop、現況與待辦 |

---

## 跨服務的事實來源

- **proto 契約**：`agents/proto/agent.proto`、`agent_gateway/proto/agent.proto` — A2A / Inbound / Outbound 結構定義
- **ModelRegistry**：`agents/app/services/model_registry.go` — 11 個 `ModelRole`（chat / chat_fast / chat_quality / chat_long / tool_calling / reasoning / vision / vision_uncensored / embedding / chunking / uncensored）對應 14 顆 model spec，是 chat / vision / embedding 模型選擇的唯一事實來源
- **架構圖**：`docs/optimized_multi_agent_architecture_v2.svg`（整體微服務拓樸）+ `docs/hybrid_rag_architecture.svg`（Agentic + Graph RAG 混合架構細節）— 兩張互補，前者看服務怎麼連，後者看 RAG 怎麼跑
- **TODO4.md**：所有已完成 / 待完成項目；變更請寫到這裡

---

## 常見對應關係

- 想知道「某 skill 由誰處理？」→ 看 `agent_registry/app/services/registry_service.go` 的 `Search()`（skill id 精確比對優先，無精確符合才 fallback JSONB substring；同分依 `last_seen_at DESC`），與各 agent 的 `/.well-known/agent-card.json`
- 想加新工具？→ `agents/app/services/tool_builtin.go`（內建）或寫成 MCP stdio server 再走 `mcp_client.go`
- 想加新 LLM provider？→ `agents/app/services/llm_*.go` + `ModelRegistry` 註冊新 model spec
- 文件 RAG 攝取流程：`agents` 收 `/api/ingest/upload` → `IngestRunner` 切 chunk → 丟 Redis Stream → `agent_embedding` 消費 → 算 dense embedding 寫 Cassandra + PG FTS

---

## dev_pub_0.9 外網部署架構設計（2026-07-06 設計定案；同日實作完成，端到端驗證待部署機）

> 目標：把平台佈上外網（先在測試 Linux 服務器驗證）。client 的所有後端流量收斂到 `agent_gateway` 單一 gRPC 入口，內部服務不受理任何外部連線，並加上「一 Key 一裝置」的 API Key 認證。

### 目標拓樸

```
                    外網
agent_client ══ gRPC + TLS + API Key ══► agent_gateway (:9090 gRPC，唯一對外)
                                             │  內部 mTLS gRPC（client cert CN 允許清單）
             ┌───────────────────────────────┼───────────────────────┐
             ▼                               ▼                       │ 內部 HTTPS（僅 verify）
        agents :9091                  agent_graph :8512              ▼
             │ mTLS gRPC                                      agent_embedding :8502
             ├─► agent_memory :8510
             └─► agent_critic :8511
（agents/graph/embedding 原有的 REST 端點一律只 bind 內網/localhost，不對外）
```

**變更前後對照**：client 原本直連三個服務（agents `:18080` REST、agent_graph `:8504` REST、agent_embedding `:8502` REST）；改後只認識 `agent_gateway` 一個位址，三條連線合併為一條 gRPC channel。

### 1. Client-facing gRPC 契約（新 proto：`agent_gateway/proto/client_gateway.proto`）

新 service `agent_gateway.v1.ClientGateway`，涵蓋 client 現有全部功能：

| RPC | 型態 | 轉發目標 | 備註 |
|-----|------|----------|------|
| `Ping` | unary | —（gateway 本地） | Test Connection 兼驗 API Key 有效性 |
| `Chat` | unary | agents **新增** `AgentService.Chat` RPC | 對應現有 REST `/api/chat` 的 ChatSync 語意（client_tools / continuation_id / tool_results 全保留） |
| `UploadIngest` | **client-streaming** | agents **新增** `AgentService.UploadIngest` RPC | 首則訊息帶 metadata（agent_id/tags/檔名清單），後續訊息帶檔案 chunk（≤64KB/則）；gateway 邊收邊轉，不整檔緩衝 |
| `GetIngestJob` | unary | agents 新增 `AgentService.GetIngestJob` RPC | 輪詢攝取任務 |
| `GraphSearch` / `GraphContext` / `GraphHybridSearch` | unary | agent_graph 既有 `GraphService` 對應 RPC | 原樣轉發 |
| `GraphListProposed` / `GraphApproveProposed` / `GraphRejectProposed` | unary | agent_graph **新增** 3 個 RPC | 之前刻意只做 REST（不給 LLM 工具用）；現在加進 `GraphService`，但 `agents` 的 `graph_query` 工具**不會**包它們——LLM 工具面不變，只有帶有效 API Key 的 client 經 gateway 可達 |
| `VerifySearch` | unary | agent_embedding 既有 REST `POST /api/search`（gateway 內部 HTTPS 呼叫） | embedding 是唯一不加 gRPC 的服務（只有這一個輕量查詢，為它建整套 proto/pb 不划算）；內部 REST 綁內網即可 |

**agents 端配套**：`proto/agent.proto` 的 `AgentService` 加 `Chat`/`UploadIngest`/`GetIngestJob` 三個 RPC，實作直接委派給既有的 `ChatSync`/`IngestService` 邏輯（controller 層薄包裝，不動業務邏輯）。REST `/api/chat`、`/api/ingest/*` 保留（內部/除錯用），部署時只 bind 內網。

### 2. 內部 gRPC 只回應合法呼叫端（mTLS + CN 允許清單）

**機制**：內部服務間 gRPC 全面改 mutual TLS。`scripts/gencert` 擴充成兩段式：
1. 產生**內部 CA**（`certs/internal-ca.crt` + `.key`，只存在於部署機）
2. 用內部 CA 簽發每個服務的 server cert 與 client cert（client cert 的 `CN=<服務名>`，如 `CN=agent_gateway`）

每個內部 gRPC server 設 `tls.RequireAndVerifyClientCert`（RootCA = 內部 CA），並加一個 interceptor 依 client cert CN 比對**允許清單**：

| gRPC server | 允許的呼叫端 CN |
|-------------|----------------|
| agents `:9091` | `agent_gateway` |
| agent_memory `:8510` | `agents` |
| agent_critic `:8511` | `agents` |
| agent_graph `:8512` | `agents`, `agent_gateway` |

- gateway 對外的 `:9090` **不用 mTLS**（外部 client 無法安全持有內部憑證），用 API Key 認證（見 §3）；gateway 對內撥出時帶自己的 client cert。
- 允許清單放各服務 `config.yaml`（`grpc.allowed_client_cns: [...]`），空清單 = 拒絕全部（安全預設）。
- **第二道防線（部署層）**：內部服務的 HTTP/gRPC 全部 bind 內網 IP 或 `127.0.0.1`，防火牆只開 gateway `:9090`。mTLS 是防「內網被穿」的縱深，不是唯一屏障。

### 3. API Key 認證 + 一 Key 一裝置

**儲存**：gateway 從「無狀態」改為掛一個 PG 連線（hidb，沿用 `192.168.50.201` 實例），新表 `api_keys`：

```sql
id TEXT PK, key_hash TEXT UNIQUE NOT NULL,   -- SHA-256(key)，明文不落地
name TEXT NOT NULL,                          -- 發給誰/用途
device_id TEXT,                              -- NULL = 尚未綁定
device_info TEXT, status TEXT NOT NULL DEFAULT 'active',  -- active|revoked
created_at / first_used_at / last_used_at TIMESTAMPTZ, last_seen_ip TEXT
```

**認證流程**（gateway 的 unary + stream interceptor 統一處理，`ClientGateway` 所有 RPC 都要過）：
1. client 每次呼叫在 gRPC metadata 帶 `x-api-key` + `x-device-id`
2. gateway 查 `SHA-256(key)`：不存在或 `status != active` → `Unauthenticated`
3. **裝置綁定（TOFU，首用即綁）**：`device_id IS NULL` → 以 `UPDATE ... SET device_id = $1 WHERE key_hash = $2 AND device_id IS NULL` 原子綁定（防多裝置同時搶綁的競態）；已綁定但不符 → `PermissionDenied`（訊息明示「key 已綁定其他裝置」）
4. 通過後更新 `last_used_at`/`last_seen_ip`（best-effort，不阻塞請求）

**client 端 device_id**：Windows 用 registry `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid` + 目前使用者名，串接後 SHA-256——跨重裝穩定、不含明文 PII；取不到時退回隨機 UUID 存 config.json（換機視同新裝置，符合「一 Key 一裝置」語意）。

**Key 管理**：gateway 加 admin CLI 子命令（不走網路 API，只能在部署機上執行）：
`gateway apikey create --name <who>`（明文只印一次）/ `list` / `revoke <id>` / `unbind <id>`（重置裝置綁定，換機用）。

**傳輸安全**：key 只在 TLS 通道內傳送；DB 只存 hash；日誌一律只記 key id/name 不記明文。

### 4. agent_client 改動

- 移除 `APIClient`/`GraphClient`/`EmbeddingClient` 三個 REST client → 單一 `GatewayClient`（gRPC，`pb/gatewaypb`）
- `Config` 改：`GatewayAddr`（host:port）+ `APIKey` 取代 `BaseURL`/`GraphBaseURL`/`EmbeddingBaseURL` 三個 URL；`InsecureTLS` 保留（測試機自簽憑證用；正式上線改成 CA pinning——把 gateway server cert 的 CA 放進 client 隨附的 `gateway-ca.pem`）
- 設定頁：Gateway Address + API Key 兩欄 + Test Connection（打 `Ping`，同時驗 key）；上傳改走 client-streaming
- 對話/上傳/圖譜/審核/Verify 全部改經 `GatewayClient`；UI 與互動流程不變

### 5. 測試 Linux 服務器佈署要點

- 7 個服務各一個 systemd unit；`agents`/`agent_graph`/`agent_embedding`/`agent_memory`/`agent_critic`/`agent_registry` 全部 bind `127.0.0.1`（或內網網段），**只有 gateway `:9090` 對外**（`:8090` HTTP A2A 入口在公網版一併關閉或限內網）
- 部署機上先跑 `gencert`（新版）一次產出內部 CA + 全部 server/client 憑證；gateway 對外憑證可先自簽（client 開 InsecureTLS）或掛 Let's Encrypt
- PG/Redis/Cassandra 沿用現有 `192.168.50.201`/`.104`（測試機需可達），或在測試機起本地實例後改各 config.yaml 的 DSN
- Windows→Linux 交叉編譯：後端服務皆純 Go（`GOOS=linux go build`）；`agent_client` 仍是 Windows 桌面程式不用搬

### 6. 實作順序（預計）

1. `gencert` 兩段式（內部 CA + per-service certs）+ 各服務 mTLS server/client + CN 允許清單 interceptor
2. agents 加 `Chat`/`UploadIngest`/`GetIngestJob` gRPC；agent_graph 加 3 個 proposed RPC
3. gateway：`client_gateway.proto` + 轉發實作 + PG/api_keys + auth interceptor + admin CLI
4. agent_client：`GatewayClient` 替換三個 REST client + 設定頁改版
5. Linux 部署腳本/systemd 範本 + 端到端驗證

### 未決事項（實作時再定，不阻塞開始）

- gateway 多副本時 `last_used_at` 更新的寫入放大（可改批次/取樣）
- API Key 到期/輪替機制（第一版先只有 revoke）
- `UploadIngest` 單檔大小上限與 gateway 端限流
- `agent_registry` 是否也要進 mTLS 圈（目前只有 gateway 打它的 REST，部署層綁內網已足夠，第一版不動）

### 實作紀錄（2026-07-06，同日完成；per-service 變動清單）

| 範圍 | 變動 |
|------|------|
| **gencert**（`agent_gateway/scripts/gencert`，重寫） | 兩段式：內部 CA（`<root>/certs-internal/`）→ 簽發 6 個服務的 `server.{crt,key}` + `client.{crt,key}`（CN=服務名）+ `internal-ca.crt` 信任錨副本；`-dns`/`-ip` 加部署機 SAN。**同時把所有 `*/certs/` 與 `certs-internal/` 加進 .gitignore 並移出 git 追蹤**（外網分支不進私鑰） |
| **mTLS 共用邏輯**（5 份拷貝：`{agents,agent_memory,agent_critic,agent_graph,agent_gateway}/internal/mtls/mtls.go`） | `ServerCredentials`（ca_file 非空 → RequireAndVerifyClientCert）、`ClientCredentials`（ca 空 → InsecureSkipVerify dev 舊行為）、`Unary/StreamAllowlist`（CN 允許清單 interceptor，mTLS 啟用時空清單=拒絕全部） |
| **4 個內部 gRPC server**（agents/memory/critic/graph main.go） | 接上 `mtls.ServerCredentials` + 允許清單 interceptor；config 加 `grpc.ca_file` + `grpc.allowed_client_cns`（yaml 已補欄位，dev 預設留空 = 行為不變） |
| **內部 gRPC client**（agents 的 memory/critic/graph client + gateway forwarder） | 構造參數改 `GRPCClientTLS{InsecureSkipVerify, ClientCertFile, ClientKeyFile, CAFile}`；config 對應段各加三個欄位 |
| **agents 新 RPC**（proto + `app/controllers/agent_grpc_client_api.go`） | `AgentService` 加 `Chat`（薄包裝 ChatSync）/`UploadIngest`（client-streaming：首則 meta、後續 file chunk、64MB 上限、落地後走 extractToText→EnqueueReferences 同一條流程）/`GetIngestJob`。proto 新訊息因與手寫 DTO 撞名改叫 `ChatToolSpec/ChatToolCall/ChatToolResult/TokenUsage` |
| **agent_graph 新 RPC**（proto + graph_grpc.go） | `ListProposed`/`ApproveProposed`/`RejectProposed`（委派昨日的 facade 方法；`ErrAlreadyReviewed` → `FailedPrecondition`）。`graph_query` 工具面不變 |
| **gateway**（大改） | ① 新 `proto/client_gateway.proto`（`ClientGateway`：Ping/Chat/UploadIngest/GetIngestJob/Graph×6/VerifySearch）＋鏡像訊息生成 `pb/gatewaypb`；agents/graph proto 拷貝生成 client stub。② `app/controllers/client_gateway.go` 轉發實作（對內非阻塞 dial；上傳邊收邊轉不整檔緩衝；VerifySearch 內部 HTTPS 打 embedding）。③ PG `api_keys`（hidb + bun AutoMigrate）+ `apikey_service.go`（SHA-256 only、TOFU 條件式 UPDATE 原子綁定、last_used best-effort）＋ unary/stream auth interceptor（只攔 `ClientGateway` prefix；`auth.enabled=false` 直通 dev）。④ admin CLI：`gateway apikey create/list/revoke/unbind`。⑤ config 加 `internal:`/`auth:`/`database:` 段 |
| **agent_client**（大改） | 刪除三個 REST client（api_service/graph_client/embedding_client + 對應測試）→ 單一 `gateway_client.go`（gRPC，介面對齊舊 client，views 幾乎零邏輯改動）；`ChatEngine` 改吃 `ChatBackend` 介面；`device_id.go`（SHA-256(MachineGuid+username)，fallback 隨機存 config）；Config 改 `GatewayAddr`+`APIKey`（移除三個 URL）；設定頁改 Gateway Address + API Key（PasswordEntry）+ Test Connection（Ping 顯示延遲+key 名） |
| **部署** | `deploy/README-deploy.md`（交叉編譯/目錄佈局/憑證/mTLS 開關表/bind 內網/防火牆/apikey 流程/8 步驗證清單）+ `deploy/systemd/agent_gateway.service` 範本 |

**驗證狀態**：6 個 module `go build`/`go vet`/`go test` 全綠（新舊測試含 client 端 engine 改用 stub backend 後全部通過）。**尚未做**：真機端到端（需要部署機起全部服務 + PG；照 deploy/README-deploy.md §8 清單走）；`:8090` A2A HTTP 入口的公網關閉屬部署層設定（bind 127.0.0.1），程式碼未動。

### 全日歷程總結（2026-07-06，四個 commit）

| Commit | 內容 |
|--------|------|
| `537bf0c` | dev_pub_0.9 主體（上表）：mTLS、ClientGateway、API Key 裝置綁定、client 改走 gateway、deploy 範本 |
| `caf8434` | 信任內網 IP 通道（`trusted_ips`，dev_090 相容——見下方補充節）+ graphic_input scaffold |
| `939bddc` | graphic_input 全套實作：agent_graph 三寫入端點（UpsertEntity/UpsertRelation/IngestText，含 embedding 同步）+ 服務本體（文件雙路徑：Obsidian inbox 筆記 / PG 直接抽取）+ client「Graph Input」分頁（`GraphInputBaseURL` 空 = 分頁不建，外網預設） |
| `f4f3381` | `agent_client` → `pub_client` 改名（目錄/module/imports）+ **DuckDB 加密儲存**：設定與對話 session 存單一加密檔 `%APPDATA%\pub_client\store.duckdb`（`ATTACH ... ENCRYPTION_KEY`，金鑰由機器特徵推導不落地、fallback store.key）——API Key 不再明文落地；舊 config.json 首次啟動自動遷移；History 鈕改 session 瀏覽器 |

完整決策與踩坑紀錄：[memory/20260706_log.md](memory/20260706_log.md)（設計篇）+ [memory/20260706_log2.md](memory/20260706_log2.md)（實作篇，含 commit 對照表）。7 個 module + pub_client 全部 build/vet/test 綠；真機端到端統一待測試機部署（deploy/README-deploy.md §8）。

### 補充（2026-07-06 稍晚）：信任內網通道（dev_090 相容）＋外網唯一入口釐清

**需求修正**：先前實作漏了三件事——(1) 內網開發機 `192.168.50.103` 要保留 dev_090 式的直連通道（免 API Key、可打所有服務）；(2) 外網唯一入口 = gateway + API Key，由測試機防火牆只放行 client→gateway；(3) 新增 graph_input 服務（設計先行，見下節）。

**信任 IP 通道實作**（已完成，全模組建置綠）：
- `mtls` 套件（5 份拷貝同步更新）：新增 `PeerIPTrusted(ctx, trusted)`（支援單一 IP 與 CIDR）；`ServerCredentials` 加 `allowNoClientCert` 參數——**有設 trusted_ips 時 mTLS 握手從 `RequireAndVerifyClientCert` 降為 `VerifyClientCertIfGiven`**（否則沒憑證的內網呼叫端在握手就被擋、輪不到 interceptor 判 IP）；允許清單 interceptor 改判「CN 在允許清單 **或** 來源 IP 在信任清單」。
- 4 個內部 gRPC server config 加 `grpc.trusted_ips: []`；gateway 加 `auth.trusted_ips: []`（命中即免 API Key，Ping 回報 key 名稱顯示 "(trusted internal IP)"）。
- **安全註記**：IP 偽造比憑證偽造容易——trusted_ips 只該搭配「防火牆已把外網擋在 gateway 之外」使用，是內網便利通道不是安全機制；設計上刻意讓「不設 trusted_ips = 行為與之前完全相同」（維持最嚴的 RequireAndVerifyClientCert）。
- 部署對照：測試機防火牆只開 `:9090`（外網 client→gateway）；內網段（含 `.103`）可達所有服務 port——`.103` 打 gateway ClientGateway 免 Key，直打各服務 REST/gRPC 也放行（gRPC 靠 trusted_ips，REST 靠 bind 內網本來就可達）。

### graphic_input 服務（已實作；內網 dev_090 專用，:8505 HTTP / :8513 gRPC）

設計文件已分離至 [graphic_input/CLAUDE.md](graphic_input/CLAUDE.md)（比照各微服務的服務級文件慣例）。要點：知識圖譜的人工輸入面，涵蓋（a）**結構化直接輸入**（實體/關係表單與批次，不經筆記也不經待審）與（b）**文件雙路徑輸入**——內網 client 送來的 markdown/text/image/PDF 抽成文字後，依指定劃分成 **Obsidian 版本**（落地 vault `inbox/` 筆記，走既有同步管線與 confidence gate）或 **PG 版本**（agent_graph 新 `IngestText` 直接 LLM 抽取 + entity_embeddings 進 PG，不留檔案）。直連 agent_graph；**dev_pub_0.9 不部署、gateway 不轉發**（外網 client 永遠沒有直接寫圖譜的能力）；前置工作是 agent_graph 補 UpsertEntity/UpsertRelation/**IngestText** 三組端點（含 entity_embeddings 同步、抽取邏輯重用）。**2026-07-06 已實作完成**（單元測試綠；真機端到端待部署驗證），實作紀錄見該文件。client 端「Graph Input」分頁由 `Config.GraphInputBaseURL` 控制（空 = 分頁完全不建，外網版預設）。
