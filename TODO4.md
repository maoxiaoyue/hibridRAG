# Single Agent Platform — 未完成項目整合清單（TODO4）

> 整合來源：TODO.md（8 步驟框架）、TODO2.md（RAG Pipeline）、TODO3.md（Multi-Agent 拆分計劃）
> 整合日期：2026-05-01 ・ 最後更新：**2026-07-09**（A2A 需求全部移除——dev_pub_0.9 起 client 全部流量收斂到
> `agent_gateway` 單一 gRPC 通道 [ClientGateway service]，A2A JSON-RPC 對外互通不再是路線圖項目；
> Phase 1 A2A 相關已完成項目保留於歷史記錄）
> 已完成項目已從原始檔移除；對應 `TODO.html` 為視覺版

> **A2A 路線圖移除註記（2026-07-09）**：原 TODO3 規劃的「A2A JSON-RPC 對外多 agent 互通」（P1-2
> A2ADispatcher、P3-1 路徑 B A2A、P4-4 A2A→gRPC 翻譯）**全部移除**。原因：dev_pub_0.9 起
> `pub_client`/`rag_admin`/`mobile_client` 三個 client 都經 `agent_gateway` 的 ClientGateway gRPC
> 服務（單一入口 + API Key + 一 Key 一裝置），gateway 內部再以 mTLS gRPC 或內部 HTTP 轉發，
> **不再需要 A2A JSON-RPC 對外協定層**。`agent_gateway/:8090` A2A HTTP 端點與已完成的
> `AgentCardBuilder`/`/.well-known/agent-card.json` 仍保留（服務間 skill discovery 用），
> 但不再列為完整化路線圖。

---

## 已完成清單（截至 2026-05-17）

### 早期完成（2026-04-27 ~ 2026-05-01）

| 項目 | 來源 | 完成狀態 |
|------|------|---------|
| Step 1：Define Purpose & Scope | TODO.md | ✅ |
| Step 2：System Prompt（persona.Parse + ToInstruction） | TODO.md | ✅（除 guardrails） |
| Step 3：LLM Client（Ollama/Anthropic/OpenAI） | TODO.md | ✅（除 streaming/cost） |
| Step 5：Memory Systems（4 類記憶 CRUD） | TODO.md | ✅ |
| Step 6：gRPC Inbound Handler + main.go 啟動 | TODO.md | ✅ |
| Step 6：GrpcDispatcher + 生命週期 + 502/504 | TODO.md | ✅ |
| ① SKILL.md 解析 → 靜態記憶寫入 | TODO2.md | ✅ |
| ② 靜態記憶（PG memory tables） | TODO2.md | ✅ |
| ③ 知識攝取管線（DocumentChunker + IngestRunner.RunReferences） | TODO2.md | ✅ |
| ④ 存儲層（Cassandra dense + DDL） | TODO2.md | ✅（sparse 表已放棄） |
| BGE-M3 Embedding（dense client；sparse 放棄） | TODO2.md | ✅ |
| Phase 1 A2A：a2a.go / a2a_handler.go / `/.well-known` 路由 | TODO3.md | ✅（保留用於 skill discovery；A2A JSON-RPC 完整化已於 2026-07-09 取消） |
| Agent Registry 獨立服務 | TODO3.md | ✅（D:\GoProjects\agent_registry） |
| Phase 1 Event Bus（Redis Stream，agents + agent_embedding） | 架構 v2 | ✅ |
| Phase 2 RAG ingest（agents → agent_embedding HTTP 整合） | — | ✅ |

### 本季完成（2026-05-08 ~ 2026-05-17）

| 項目 | 路徑 | 狀態 |
|------|------|------|
| **P0-1** ContextBuilder（Build / BuildWithPersona） | `agents/internal/agent/context/builder.go` + `tokens.go` | ✅ |
| Token 預算（HeuristicCounter，CJK-aware） | `agents/internal/agent/context/tokens.go` | ✅ |
| 數量配額讀 `config.context.*`（三層 fallback） | `agents/main.go` + `config/config.go` | ✅ |
| **P0-2** HybridSearcher / RRF Fusion | `agents/internal/agent/retrieval/hybrid.go` | ✅ |
| 接到 main.go 取代 ragSearcher + `config.retrieval.*` | `agents/main.go` + `config/config.yaml` | ✅ |
| **P0-3** agent_grpc.process() 接入 RAG context | `agents/app/controllers/agent_grpc.go` | ✅ |
| EpisodicMemory 寫入封閉學習迴圈 | `agents/app/controllers/agent_grpc.go` | ✅ |
| ~~**P0-4** BGE-M3 Sparse 向量寫入~~ | — | ❌ 2026-05-08 放棄 |
| **P1-1** Tool Calling 框架（Tool / Registry / Schema） | `agents/app/services/tool.go` | ✅ |
| 內建工具：memory_read / memory_write / rag_query / web_search | `agents/app/services/tool_builtin.go` + `tool_web_search.go` | ✅ |
| MCP stdio JSON-RPC client | `agents/app/services/mcp_client.go` | ✅ |
| Tool 回注迴圈（RunToolLoop） | `agents/app/services/tool_loop.go` | ✅ |
| Ollama ChatWithTools（per-provider wire） | `agents/app/services/llm_ollama_tools.go` | ✅ |
| agent_grpc.process() 接入 RunToolLoop（條件式 fallback） | `agents/app/controllers/agent_grpc.go` + `main.go` | ✅ |
| **P1-3** Dispatcher 重試（exponential + jitter） | `agents/app/services/retry.go` | ✅ |
| Trace propagation（gRPC metadata x-trace-id + 雙向 interceptor） | `agents/app/services/trace_meta.go` | ✅ |
| 推理 Worker Pool（bounded queue + 429 backpressure） | `agents/app/services/worker_pool.go` | ✅ |
| **P2-1** 單元測試補齊（outbound / downstream_config / memory_service DTO / redis_stream） | `app/services/*_test.go` + `internal/eventbus/*_test.go` | ✅ |

### 本月完成（2026-07-02 ~ 2026-07-03）

| 項目 | 路徑 / 證據 | 狀態 |
|------|------|------|
| **agent_client → HypGo 風格重構** | `agent_client/app/{models,services,views}/*`（原 `internal/{config,api,tools,chat,ui}` 佈局）；typed errors（E3001–E3005）、hypgo logger、desktop schema 註冊、`tools/genctx` | ✅ |
| **視窗（GUI）背景工具**：`ToolSpec.Background` | `agent_client/app/services/tool_service.go`（`launchBackground()`：`cmd.Start()` 不等待、不套 timeout，避免記事本/瀏覽器類程式被誤判逾時砍掉）+ 測試 | ✅ |
| **內建 Coding Agent**（讀檔/列目錄/搜尋/編輯/寫檔） | 新檔 `agent_client/app/services/code_service.go`（+ `code_service_test.go` 18 cases）+ `app/views/diff_dialog.go`；比照 Claude Code Read/Grep/Edit/Write 語意，寫入前強制 diff 審核彈窗、路徑限制工作目錄內（擋 `..` 逃逸）、二進位/非 UTF-8 拒讀拒寫 | ✅ |
| **對話頁 Normal／Code 模式切換**（雙重開關） | `agent_client/app/views/chat_view.go`：Normal 模式一律傳空字串當 workspaceRoot（`BuiltinCodeTools("")` 回空），需工作目錄已設 **且** 手動切到 Code 模式才把碼工具交給 agent | ✅ |
| **agent_registry Search() 路由修正** | skill-id 精確比對優先於 JSONB substring，避免撞到殘留死資料（舊測試 agent 指向死掉的 endpoint） | ✅ |
| **全 5 服務 HTTP/2 + 真 TLS** | 複用 `agents/scripts/gencert/`（ECDSA P-256, 10年自簽自驗）；各服務 `config.yaml` 加 `server.protocol: http2` + `server.tls` | ✅ |
| **gRPC TLS**（`agents:9091`、`agent_gateway:9090`） | server + client 端（`grpc_forwarder.go`） | ✅ |
| **P4-2 agent_memory 補齊 gRPC**（agents 端改真呼叫，非直連 DB） | 新 `agents/app/services/memory_grpc_client.go` + `agent_memory/app/controllers/memory_grpc.go`；`main.go` 依 `config.memory.grpc_addr` 是否空切換 gRPC / 舊直連 DB fallback | ✅ |
| **P4-2 收尾：`agents/app/controllers/memory.go` REST 代理移除** | 刪除該檔 + `app/routers/memory.go` + 直連 DB 的 `app/services/memory_service.go`；`memory.grpc_addr` 改必填（撥號失敗直接 exit，不再 fallback 直連 DB）；`agents/app/models/init.go` 的 `AutoMigrate` 不再建記憶表 | ✅ |
| **semantic memory 首次真正跑通**（`agent_memory` `LongTermMemoryService`，Cassandra + BGE-M3 + SAI ANN） | 揪出兩個從沒被跑過的潛伏 bug 並修復：(a) Cassandra 表從未建立（補 `EnsureSchema()`）；(b) gocql 無法直接 marshal `[]float32` 進向量欄位（移植 `CQLVector` wrapper 至 `agent_memory/app/services/vector_marshal.go`） | ✅ |
| **shared memory**（新 6 類記憶之一） | 新 PG 表 `shared_memory`（Bun `unique:` 複合唯一鍵）+ 新 RPC + `agent.pool_id` 設定；`tool_builtin.go` 加 `kind=shared` | ✅ |
| **short-term memory**（`/api/chat` 多輪對話歷史） | Redis-backed `SessionStore`（整包 JSON blob，maxTurns=40，TTL 2h，nil-receiver-safe：Redis 未連自動降級回單輪）接進 `ChatSync` | ✅ |

### 本月完成（2026-07-04）

| 項目 | 路徑 / 證據 | 狀態 |
|------|------|------|
| **Agent Card 動態組裝**（原 P1-2 首項；後續 A2ADispatcher 已取消） | 新 `agents/app/services/agent_card.go`（`AgentCardBuilder`）：Name/Description/Skills[] 從目前載入的 `SkillPack`（`persona.ParseResult`）推導（MentalModels → 一個 skill 一筆；無 MentalModels 時 fallback 回原本寫死的單一 `chat` skill，行為不變）；`a2a_handler.go` 的 `A2ACardHandler` 只用 `Get()`（cache 讀取，不觸發 `Load()`——高頻無認證探索端點不該有磁碟 I/O）。這份 card 現用於 gateway 對下游 skill discovery、`/.well-known/agent-card.json` | ✅ |
| **SKILL.md 新增 `## Model` 段 + ROLE.md 衍生檔** | `internal/agent/persona/parser.go` 加 `ParseResult.ModelPref`（`role`/`model` 雙軌，語意比照既有 `ApplyToOllamaConfig`：明確 `model` 覆寫優先，否則用 `role` 走 `ModelRegistry` 解析）；新 `app/services/role_compiler.go`（`RoleCompiler`）把解析結果寫成 `skills/{agent_id}/ROLE.md`（衍生檔，人工仍只寫 SKILL.md，`.gitignore` 已排除） | ✅ |
| **依 persona 動態切換推理用 LLM（僅 ollama 模式）** | 新 `app/services/skill_compile.go`（`CompilingSkillLoader`，包一層 `SkillLoader`，真正重讀時才觸發編譯 + 快取 `ModelResolution`）+ 新 `app/services/llm_pool.go`（`LLMClientPool`，依 model 名稱快取 `*OllamaClient`）；`AgentServer` 新增 `resolveLLM(agentID)`（`agent_grpc.go`），`process()`/`runChatLoop()` 兩條推理路徑（gRPC Inbound + `/api/chat`）都改用解析出的 client，未設定 `## Model` 或非 ollama 模式時 100% fallback 回原本的全域 `llm`，行為不變 | ✅ |
| **測試** | `persona/parser_test.go` +6 cases（`## Model` 解析、與既有「模型」= Mental Models 別名不衝突、`ToInstruction()` 不外洩）；`app/services/role_compiler_test.go`（`RoleCompiler`/`CompilingSkillLoader` 端到端，含未知 role 報錯、無偏好時仍寫 `(none)` 佔位）；`app/services/agent_card_test.go`（`AgentCardBuilder` 推導 + fallback + `slugify`）；`app/controllers/resolve_llm_test.go`（`resolveLLM` 白箱測試，涵蓋 4 種 fallback/命中分支）；`go build`/`go vet`/`go test` 全過 | ✅ |

### 本月完成（2026-06-06）

| 項目 | 路徑 / 證據 | 狀態 |
|------|------|------|
| **ModelRegistry** — chat / vision / embedding 模型單一事實來源 | `agents/app/services/model_registry.go`（14 顆 model spec：gemma4/qwen3.5/qwen3-vl/minicpm-v/bge-m3 + 2 顆未載入 Mellum2/mmproj；11 個 `ModelRole`：chat / chat_fast / chat_quality / chat_long / tool_calling / reasoning / vision / vision_uncensored / embedding / chunking / uncensored） | ✅ |
| `OllamaConfig.Role` / `VisionConfig.Role` 欄位 + `ApplyToOllamaConfig` helper | `agents/app/services/llm_client.go` + `agents/config/config.go` + `agents/app/services/model_registry.go` | ✅ |
| `main.go` 配線：啟動建 registry → LLM 與 Vision 兩條路徑都改走 registry 解析（明示 model 仍最高優先；空 model + role → registry；雙空 → defaultRole） | `agents/main.go` | ✅ |
| `config/llm.yaml` 加 `role:` 說明；`config/config.yaml` 視覺改為 `role: "vision"`（model 留空 → registry 解析為 qwen3-vl:4b） | `agents/config/llm.yaml` + `agents/config/config.yaml` | ✅ |
| Registry 測試 9 cases 全 PASS（Resolve / Skip-unloaded / Fallback / List / Apply 四場景） | `agents/app/services/model_registry_test.go` | ✅ |

### 本月完成（2026-06-02）

| 項目 | 路徑 / 證據 | 狀態 |
|------|------|------|
| **hypgo v0.8.5 → v0.8.6** 全服務升級 | 四個 `go.mod` | ✅ |
| **hidb 統一**：PG/Redis/Cassandra 連線全部改走 `hypgo/pkg/hidb` | `agents/internal/{database,cache,agent/vectordb}/*` + `agent_embedding/internal/{postgres,cache,cassandra}/*` + `agents/cmd/probe/main.go` + integration test；`agent_memory` / `agent_gateway` 早已遵循 | ✅ |
| **P2-2 第一項** memory_service 真 CRUD（真 PG） | `agents/app/services/memory_service_integration_test.go`（`//go:build integration`，schema `agent_inttest` 自建自清，8 cases 全 PASS） | ✅ |
| **本地 LLM 串接驗證** | `agents/config/llm.local.yaml`（custom provider → llama.cpp 127.0.0.1:18080）；ollama provider → Ollama 11434；兩套都實測過 SKILL.md→persona→Chat→SkillPack 正確 | ✅ |
| **gRPC 全鏈路 live test**（非自動化）| `agents/cmd/inbound-test/main.go` 對 :9091 連發 3 次；log 顯示 episodic 0→1→2 線性增長，閉環學習迴圈通電 | ✅ |
| **K8s + containerd 部署文件** | `setup.html`（4 張 SVG 圖、port 分配表、nerdctl build 流程、Manifests 範例；LLM 改成 cluster 內 Ollama 11434） | ✅ |

---

## 待完成項目（依優先級分組）

### P1 — 功能增強

#### P1-2：〔已取消〕A2A 完整化

> **2026-07-09 移除**：dev_pub_0.9 起 client 全部流量收斂到 `agent_gateway` 的 ClientGateway
> gRPC 通道（Ping / Chat / ChatStatus / ChatImage / UploadIngest / Graph* / VaultManifest /
> VaultFetch / VerifySearch / ListPersonas / SetPersonaModel），gateway 內部再以 mTLS gRPC
> 或內部 HTTP 轉發給後端服務。**A2A JSON-RPC 對外協定層不再需要**。原規劃項目
> （A2ADispatcher / DownstreamConfig protocol switch / Tasks 狀態管理 / registry 整合）全部撤下。
> `AgentCardBuilder` 與 `/.well-known/agent-card.json` 已完成、保留作為 skill discovery 用途。

#### P1-4：LLM 進階功能（TODO.md #3）
- [ ] Streaming response（`StreamingLLMClient` 介面，server-streaming gRPC）
- [ ] Cost tracking（依 `TokenUsage` × provider 單價計算，寫入 PG metric 表）

#### P1-5：Guardrails（TODO.md #2）
- [ ] 拒絕清單規則（黑名單關鍵字、危險 prompt patterns）
- [ ] 安全邊界（max input length、max tokens、禁用工具白名單）
- [ ] 違規時的回應策略（拒絕模板）

#### P1-X：P0-1 剩餘子項（相似度排序，需先補 embedding）
- [ ] ProceduralMemory top-3 改為**依 query 相似度**（目前 `updated_at desc`）
  - 前置：`procedural_memory` 表加 vector 欄位，或在 chunks_dense 加 `kind=procedural`
- [ ] EpisodicMemory top-2 改為**相似對話**（目前 `occurred_at desc`）
  - 前置：episodic.summary 上 embedding

---

### P2 — 測試與品質

#### P2-2：Integration Tests
- [x] **memory_service 真正 CRUD**（2026-06-02 完成）— `agents/app/services/memory_service_integration_test.go`，`//go:build integration` tag 隔離，直連真 PG（`192.168.50.201:5433`，schema `agent_inttest` 自建自清），8 cases 全 PASS：Personal/Procedural/Episodic/Tool CRUD + Upsert 冪等 + List 排序/limit + JSONB 巢狀 round-trip。DB 連不上時 Skip 非 Fail。跑法：`go test -tags=integration -run Integration ./app/services/`
- [ ] gRPC Inbound → ContextBuilder → LLM → Outbound 全鏈路（mock LLM）
- [ ] Ingest 全鏈路：`POST /api/ingest/references` → `runner.RunReferences` → agent_embedding `/api/upsert` → `embedding.upserted` 事件
- [ ] LLM provider 切換測試（mock httptest 已存在，需擴充 e2e）

#### P2-3：Evals（品質評估）
> 新增獨立小工具 `evals/`（比照 `black_browser/` 慣例，自己的 go.mod）承接下面兩項；
> 詳細設計與 phase 拆分見 [evals/README.md](evals/README.md)。
- [ ] Latency benchmark（Inbound → Ack 延遲 P50/P95/P99）
- [🟡] **LLM 回應品質指標（BLEU / 人工標記 / LLM-as-judge）** — Phase 1 完成：`evals/pkg/bleu`
  簡化版 BLEU-4（CJK 逐字 tokenize）+ `evals/cmd/run` 呼叫 `agents` Chat 算分、印總表、存
  JSON。**人工標記、LLM-as-judge 尚未做**（見 evals/README.md「後續 phase」：Phase 2 把
  `agent_critic` 的 Scorer 抽介面、Phase 3 加 LLMJudgeScorer、Phase 4 人工標記 CSV round-trip）。
  **本次實作未跑 build/vet/test/實際執行**，下一步先驗證。
- [ ] Memory recall accuracy（給定 query，能否取出正確記憶）
- [ ] RAG retrieval relevance（NDCG / MRR）
- [🟡] **建立 eval dataset（測試案例 + 預期輸出）** — Phase 0 完成：`evals/cases/*.yaml`
  schema（`Case{id,agent_id,category,input,expected,difficulty,notes}`）+ `evals/pkg/dataset`
  loader/validate + 20 筆種子案例（`go_basics.yaml` 12 筆 general_chat、`rag_concepts.yaml`
  8 筆 rag_qa，皆對應 `demo` persona 領域）。同上，未跑驗證。
- [ ] CI 整合 eval 跑分並設門檻（GitHub Actions）

---

### P3 — 接入方式與文件

> **2026-06-06 範圍調整**：原 P3-1「自建 Chat Web App / Slack / Discord bot」需求**取消**。改為既有 client 接入路徑。
> **2026-07-09 範圍再調整**：原「路徑 B — A2A JSON-RPC 對外多 agent 互通」隨 P1-2 一同取消（見上）。
>
> 目前接入路徑：
> 1. **桌面 client / 手機 client** → 全部走 `agent_gateway` 的 ClientGateway gRPC 通道
>    - `rag_admin/`（原 agent_client，Windows Fyne 內網管理版）
>    - `pub_client/`（Windows Fyne 外網使用者版）
>    - `mobile_client/`（Android Fyne，2026-07-09 新增）
> 2. **內部 REST**（`agents/:18080`、`agent_graph/:8504` 等）— 供 gateway 自己或內部除錯用；client 不直接打

#### P3-1：接入方式（取代原 UI 需求）
- [x] **路徑 A — client**：pub_client / rag_admin / mobile_client 三個 client，全部經 `agent_gateway`（P7 章節）
- [x] **路徑 B — REST API（同步對話，內部）**：`POST /api/chat`（`status=tool_calls` / `final`）+ `POST /api/ingest/upload` + `GET /api/ingest/jobs/:id`；schema 已註冊（hypgo `schema.Route`）
- [x] **路徑 B — gRPC（內部 agent 間）**：`agent.v1.AgentService/Inbound` 已上線（`:9091`）；下游 dispatch 走 `GrpcDispatcher`（含重試 / trace propagation / 502/504）

#### P3-2：API 文件（保留）
- [ ] OpenAPI / Swagger 文件（自動從 hypgo `schema.Route` 產出，給 API 接入方參考）
- [ ] client 工具登錄教學文件（`tools_test.go` 已涵蓋格式；補一份使用者向 README 範例）

---

### P4 — 架構 v2 拆分

> 原 `docs/MIGRATION.md` 已於 2026-05-17 刪除；以下列出剩餘工項，搭配 SVG 對照表（見下方）即可單獨執行。

#### 📐 與 SVG 原設計圖對照（2026-05-17）

參照 `docs/optimized_multi_agent_architecture_v2.svg`（v2 plan：Gateway + 6 個 shared microservices + Event Bus）。
**現況**：實際運行 5 個 microservice（`agents` / `agent_embedding` / `agent_gateway` / `agent_memory` / 外部 `agent_registry`），對齊大方向約 50%。

| SVG 規劃 | port / 通訊 | 實裝狀態 | 對應 TODO |
|----------|------------|---------|----------|
| ★ agent_memory | gRPC :50052 | ✅ 已拆出（`agent_memory/`）且已補 gRPC（實際埠 :8510，非 SVG 規劃的 :50052）；`agents` 端 `memory_grpc_client.go` 真呼叫，REST :8501 仍並存 | P4-2（2026-07-03 完成，殘留見下） |
| ☆ LLM Gateway | gRPC :50053 | ❌ 未拆，仍在 `agents/app/services/llm_*.go` | P4-3 |
| ☆ Embedding Svc | gRPC :50054 | 🟡 在 `agent_embedding/` 但走 HTTP :8502 | P4-1 |
| ☆ Vector Search | gRPC :50055 | 🟡 與 Embedding **合併**在 `agent_embedding` 同 binary | P4-1（拆 binary） |
| ※ Ingest Service | gRPC :50056 | ❌ 未拆，仍在 `agents/app/services/ingest_service.go` | P4-3 |
| ※ Skill Store | gRPC :50057 | ❌ 未拆，仍在 `agents/app/services/skill_loader.go` + `skill_selector.go` | P4-3 |
| Gateway Layer（ClientGateway gRPC + Router + TLS） | :443 + :8080 | ✅ `agent_gateway` 的 ClientGateway gRPC（:9090）已上線並成為 client 唯一入口；內部 mTLS + API Key 完成；:8090 A2A HTTP 端點保留但外網不曝光 | P4-4（Ingress / RateLimit / Auth 強化） |
| ⚡ Redis Stream | :6379 | ✅ 已實裝 | — |
| ⚡ Kafka | :9092 | ❌ 未部署（按需，Redis Stream 已足） | P4-5 |
| Storage（PG / Redis / Cassandra / TEI 即 Ollama） | — | ✅ 一致 | — |
| Internal Agents A/B/C | gRPC :50051 | ✅ `agents` 單一 binary 可承擔多 agent_id | — |
| ➕ agent_registry（SVG 未列） | :8500 | ✅ 額外實裝（service discovery） | — |

**三大偏離點**：
1. 內網通訊規劃 gRPC、現多為 HTTP → P4-1
2. LLM Gateway / Ingest / Skill Store 未拆 → P4-3
3. Embedding 與 Vector Search 合併在同 binary → P4-1 拆 `cmd/embedding/` + `cmd/vector-search/`

**合理偏離**：HTTP 先行 → gRPC 漸進切換是雙跑策略；agent_registry 補 SVG 漏列的 discovery。

#### P4-1：Phase 2 — Embedding / Vector Search 拆分（~2 days）
- [ ] `agent_embedding/proto/embedding.proto` — `EmbeddingService.Embed` / `EmbedBatch`
- [ ] `agent_embedding/proto/vector_search.proto` — `VectorSearchService.Upsert` / `Search` / `Delete`
- [ ] 加入 gRPC server（共用 binary，:50054 + :50055）
- [ ] `agents` 加入 gRPC client（`embedding.mode: grpc` 選項）
- [ ] 雙跑驗證（HTTP vs gRPC 結果一致）→ 切換 → 移除 HTTP path
- [ ] 拆成獨立 binary（`cmd/embedding/`、`cmd/vector-search/`）

#### P4-2：Phase 3 — agent_memory 補齊 gRPC（2026-07-03 完成，含收尾）
> agent_memory 已存在但走 HTTP，需補 gRPC 並讓 agents 端 client 化。
- [x] proto + `agent_memory/app/controllers/memory_grpc.go` — gRPC server 上線（實際埠 :8510，非原規劃 :50052；REST :8501 並存）
- [x] `agents/app/services/memory_grpc_client.go` 實作 gRPC client
- [x] agents 把 `MemoryService` 換成走 gRPC client
- [x] **收尾**：`agents/app/controllers/memory.go`（`agents` 自己的 `/api/memory/*` REST 代理）+ 直連 DB 的 `memory_service.go` 已刪除；`config.memory.grpc_addr` 改必填（不再 fallback 回直連 DB）——`agents` 對記憶資料只剩 gRPC → `agent_memory` 一條路徑
- [ ] agent_memory 訂閱 `ingest.completed` → 自動寫 episodic（尚未做）

#### P4-3：Phase 4 — Skill Store / LLM Gateway / Ingest 拆出（~7 days）
- [ ] **Skill Store（:50057）**
  - [ ] 抽 `skill_loader.go` + `skill_selector.go` 至獨立服務
  - [ ] proto：`SkillStoreService.{Load,Get,Select}Skill`
  - [ ] 訂閱 `embedding.upserted` → 自動觸發 reload
- [ ] **LLM Gateway（:50053）**
  - [ ] 抽 `llm_*.go`（含 `llm_ollama_tools.go`）至獨立服務
  - [ ] proto：`LLMService.Chat` / `Stream` / `ChatWithTools`（server-streaming RPC）
  - [ ] 共享 Redis 做 session cache
- [ ] **Ingest Service（:50056）**
  - [ ] 抽 `ingest_service.go` + `internal/agent/ingest/` 至獨立服務
  - [ ] 改為 Kafka consumer（接收上游推送的「請攝取此 doc」事件）
  - [ ] 完成後發 `ingest.completed` 給 Skill Store / Vector Search 重建索引

#### P4-4：Phase 5 — API Gateway 強化（~3 days）
> `agent_gateway/` 的 ClientGateway gRPC 已作為 client 唯一入口上線（dev_pub_0.9）；本階段補強生產化細節。
- [x] 服務發現（依 agent_id 路由，串 `agent_registry`）
- [x] Auth（API Key + 一 Key 一裝置 TOFU 綁定；SHA-256 存 hash）
- [x] TLS 終端（:9090 對外 TLS；內部 mTLS gRPC 給 agents/graph；client cert CN 允許清單）
- [ ] Rate Limit（per API key + per IP，含 429 backpressure）
- [ ] Ingress 反向代理接入（NGINX / Traefik，配合 K8s Ingress）
- [ ] gateway metrics / access log（結構化 JSON，便於 SLO 觀測）

#### P4-5：Phase 6 — Kafka（按需）
- [ ] 何時加入：Ingest 量超過 Redis Stream 上限（>100K events/sec）或需要 Exactly-Once
- [ ] 當下 Redis Stream 已足夠，列為**未來增強**

---

### P6 — Agent Memory v2（生產級分層記憶）

> 來源：`docs/agent_memory.svg`「生產級 Agent Memory 系統架構」四大區塊。
> 對照現有實作後，三大區塊有缺。
>
> **注意與根目錄 CLAUDE.md 的 6 類記憶分類（personal/procedural/episodic/semantic/shared/short-term）不是同一件事**：後者 6 類已於 2026-07-03 全數接進推理流程（見上方「本月完成」），但本節針對的是 SVG 的「摘要品質 / 滑動窗口 / Policy / 雙時態 / 自動固化」——這些仍大多未做，下表狀態未變。

#### 📊 差距分析（2026-06-02 比對）

| SVG 區塊 | 圖中項目 | 對應現況 | 狀態 |
|---|---|---|:---:|
| 一、分層記憶 | 1. 會話原數據 (Metadata) — 用完即丟 | `InboundReq.Metadata`（model/temperature）+ trace_id；不進 long-term | ✅ |
| | 2. 結構化檔案卡 (JSON) — 單一事實、直接覆蓋 | `personal_memory` + `tool_memory`（JSONB）+ `UpsertPersonal` ON CONFLICT | ✅ |
| | 3. 近期對話摘要 (Summary) — 長篇閒聊濃縮 | `episodic_memory.summary` 欄位有，但**只填 `truncate(user_msg, 200)`**，不是 LLM 壓縮的真摘要 | 🟡 半套 |
| | 4. 滑動窗口 (Sliding Window) — 超量舊訊直接丟棄 | 無；ContextBuilder 直接取 episodic top-2 by `occurred_at desc` | ❌ |
| 二、三、大模型與控制 | 大模型（直覺系統） | `services.LLMClient` | ✅ |
| | 慢思考迴路（暫停 / 調用工具找證據） | Tool Calling 框架 + `RunToolLoop`（P1-1 已完成） | ✅ |
| | 記憶大腦：Policy（決定何時查/寫/遺忘） | **無**；目前每輪固定「全查 + 全寫」，無自適應 | ❌ |
| | 原始賬本 (Ledger) — 只追加不修改 | `episodic_memory` 是 append-only；其他三類 memory 仍可 update | 🟡 部分 |
| | 派生視圖 (Views) — 轉模型可讀 | `persona.ToInstruction()` + `ContextBuilder.Build()` | ✅ |
| 四、防雷機制 | 雙時態（valid_from / system_at 雙時間戳） | 只有 `occurred_at` / `created_at` / `updated_at`；無「現實生效時間」對偶 | ❌ |
| | 程序性記憶（成功軌跡 → 固化 Skill） | `procedural_memory` 表存在但僅從 SKILL.md 解析；無自動壓縮固化機制 | 🟡 表存在 |

**對照結論**：4 項已實作、3 項半套、4 項未實作。屬於「memory 層次往上一級」的整體升級，跨多個服務（agents core + agent_memory）。

#### P6-1：對話摘要（Summary）升級為真摘要（~2 days）

把 `episodic.summary` 從「user message 截斷」改為 LLM 壓縮的真摘要。

- [ ] 新增 `services.SummaryService.Summarize(ctx, turns []ChatMessage) (string, error)`，內部呼叫 LLM（複用現有 `LLMClient`，低溫度 + 系統提示「壓縮為 ≤200 token 摘要」）
- [ ] `agent_grpc.writeEpisodic()` 中：若一輪 turns ≥ N 個訊息（閥值可調），改呼叫 SummaryService；否則維持原 truncate
- [ ] 加 `config.context.summary_min_turns`、`summary_max_tokens` 兩參數
- [ ] 測試：`summary_service_test.go` 用 mock LLM 驗證輸出格式

#### P6-2：滑動窗口（Sliding Window）（~1 day）

避免 episodic 無上限累積；超量丟棄。

- [ ] `MemoryService.PruneEpisodic(ctx, agentID, keepLast int) (int, error)`：保留最近 N 筆，其餘 DELETE
- [ ] 定時 job（worker pool 背景任務）每隔 M 分鐘對所有 agent 跑一次
- [ ] 或入站時觸發（每次寫入後若超量自動裁切）— 後者較佳，無需排程
- [ ] 加 `config.memory.sliding_window`（每 agent 保留筆數）

#### P6-3：記憶大腦 Policy（讀/寫/遺忘決策）（~3 days）

從「每輪全查全寫」升級為自適應策略。

- [ ] 新增 `internal/agent/policy/policy.go`：
  - `ShouldQuery(query, agentState) bool` — 是否查 RAG / Episodic
  - `ShouldWrite(turn) bool` — 是否寫入 episodic（過濾低價值對話如 "ok"/"thanks"）
  - `ShouldForget(memory, age) bool` — 老舊低訪問記憶是否標記遺忘
- [ ] 預設策略：rule-based（query 長度 < 5 不查 RAG；assistant 回應 < 10 字不寫 episodic；訪問次數 0 且 > 30 天遺忘）
- [ ] 進階策略：LLM-based（小模型判斷價值）— 第二階段
- [ ] 在 `agent_grpc.process()` 與 `writeEpisodic()` 注入 Policy 檢查

#### P6-4：雙時態機制（Bi-temporal）（~3 days）

防幻覺：分離「事實何時生效」與「系統何時記錄」。

- [ ] 給 `episodic_memory` / `personal_memory` 加兩欄：`valid_from TIMESTAMPTZ`、`valid_to TIMESTAMPTZ`（NULL = 仍生效）；保留現有 `created_at` / `updated_at` 當系統時間
- [ ] `MemoryService.GetAsOf(ctx, agentID, asOfRealTime time.Time)` — 時間切片查詢
- [ ] `UpdatePersonal` 新增模式：不就地覆蓋，改插新 row 並把舊 row 的 `valid_to = now()`（事件溯源風格）
- [ ] migration `005_add_bitemporal.up.sql`
- [ ] 測試覆蓋：時間切片 round-trip、valid_to 自動填寫

#### P6-5：程序性記憶自動固化（~3 days）

把成功軌跡壓縮為 Skill；架構圖中「將成功互動軌跡壓縮過濾，固化為可一鍵執行的標準 Skill」。

- [ ] 新增 `internal/agent/skill_distill/distiller.go`：
  - 觀察相似 query → tool_call 序列重複出現（≥ K 次成功）
  - 用 LLM 壓縮成 procedural template（input pattern + tool sequence）
  - 寫入 `procedural_memory`（新 source: `auto_distilled`）
- [ ] 訂閱 `episodic.recorded` 事件批次分析（背景 worker）
- [ ] 加 `config.skill_distill.{min_occurrences, similarity_threshold, enabled}`
- [ ] 與 P4-3 Skill Store 對接時可演化為「自動 reload skill」

#### P6-6：Ledger 強化（Append-only contract）（~1 day）

`episodic` 已 append-only；補 contract 一致性。

- [ ] `PersonalMemory` / `ProceduralMemory` 增加 `version` 欄位，每次 update bump（為未來事件溯源鋪路）
- [ ] 提供 `MemoryService.History(ctx, id) []Snapshot` 取所有版本（依雙時態查詢實作；與 P6-4 並行）
- [ ] log 強制：任何 mutation 都寫一筆 audit event 到 Redis Stream `memory.mutated`

#### 建議實作順序（P6）

```
P6-1 (Summary)  ←  最便宜，立刻可看到 episodic 內容變漂亮
P6-2 (Window)   ←  防止 memory 爆量；單純 cron job
P6-3 (Policy)   ←  rule-based 先做；之後再升級 LLM-based
P6-4 (雙時態)   ←  動表結構，建議和 P6-6 一起做
P6-5 (固化)     ←  最有 wow effect 但最重；建議放最後
```

---

### P7 — Agent Client（Windows Fyne 桌面 client）

> 位置：`D:\GoProjects\single_agent\agent_client\`（Go + [Fyne](https://fyne.io)，需 CGO + MinGW-w64）
> 對接後端 `agents` 服務：`POST /api/chat`（同步對話 + client 端工具回合制）、`POST /api/ingest/upload`、`GET /api/ingest/jobs/:id`
> 設定持久化：`%APPDATA%\agent_client\config.json`

#### P7-1：基礎四分頁 UI <span>2026-06-05 完成</span>
- [x] **對話分頁** — 與 agent 同步對話；agent 要求時於本機執行已登錄工具（回合制）`internal/ui/chat.go` + `internal/chat/engine.go`
- [x] **上傳分頁** — 選 PDF / 圖片 / 文字 → 上傳攝取 → 輪詢進度 `internal/ui/upload.go`
- [x] **工具分頁** — 登錄 / 編輯 / 刪除本地可執行工具（command + argv 樣板 + JSON Schema）`internal/ui/tools.go` + `internal/tools/tools.go`（含 `tools_test.go`）
- [x] **設定分頁** — Agent Base URL / Agent ID / 自簽 TLS `internal/ui/settings.go`
- [x] 設定持久化 `internal/config/config.go`（`%APPDATA%\agent_client\config.json`）
- [x] HTTP client（含自簽 TLS 跳過）`internal/api/client.go`
- [x] CGO + MinGW-w64 UCRT gcc 15 建置驗證（`go build -o agent_client.exe .` 成功）

#### P7-2：本地工具執行（回合制）<span>2026-06-05 完成</span>
- [x] 工具登錄時送 manifest（name/description/schema）→ 對話帶 `client_tools[]`
- [x] 後端回 `status=tool_calls` → client 本機 `exec.Command`（不經 shell，避免注入）→ 回送 `tool_results` → 接續到 `final`
- [x] 安全邊界：allowlist（只執行登錄工具）+ argv 陣列代入 + 可選「執行前確認」彈窗 + 硬性逾時（預設 30s）

#### P7-3：UX / 觀測強化（待辦）
- [ ] 對話分頁顯示 token 用量 / `finish_reason` / 工具迴圈次數（從後端 `ChatTurnResponse.Usage` 取，目前未顯示）
- [ ] 對話歷史保留與「重置」按鈕（目前重啟即丟）
- [ ] 上傳進度條（目前只輪詢顯示百分比，無視覺進度條）
- [ ] 工具執行 log tab（stdout / stderr 即時跟著對話顯示）
- [ ] 多 agent 切換（記住多組 Base URL + Agent ID，下拉切換）
- [ ] 串流回應（待後端 P1-4 streaming 完成後接 SSE / gRPC server-stream）

#### P7-6：內建 Coding Agent（讀檔/搜尋/編輯 + diff 審核）<span>2026-07-02 完成，尚未 commit</span>
> 與 P7-2 的「使用者登錄工具」不同：這組是 **client 內建**、比照 Claude Code 的 Read/Grep/Edit/Write 語意。
- [x] 5 個內建工具：`code_list_files` / `code_search` / `code_read_file` / `code_edit_file` / `code_write_file` — `agent_client/app/services/code_service.go`（+ `code_service_test.go` 18 cases）
- [x] 寫入前強制 diff 審核（`difflib.UnifiedDiff` + `dialog.NewCustomConfirm`）— `agent_client/app/views/diff_dialog.go`
- [x] 路徑安全：`resolveInWorkspace` 用 `filepath.Rel` 擋 `..` 逃逸，逃逸時明確回錯而非靜默鉗制
- [x] 二進位/非 UTF-8 內容一律拒讀拒寫
- [x] 雙重開關：`Config.WorkspaceRoot` 已設 **且** 對話頁 Normal/Code 模式切到 Code，兩者缺一 `BuiltinCodeTools("")` 回空
- [x] `chat_view.go` 對話頁模式切換按鈕（`MediumImportance`/`WarningImportance` 視覺區分；未設工作目錄就切 Code 會提示先去設定頁）
- [ ] **已知限制**（README 已誠實記載，非待辦）：本地小模型（`gemma4:e4b`）多檔案/複雜重構可靠度遠不如雲端大模型；後端 `tools.max_iterations`（預設 8）可能不夠跑完複雜任務；只支援單一文字檔逐次編輯，不支援真正多檔案 diff 規劃
- [ ] 尚待：目前 8 個變更檔案（README / config.go / chat_service.go / tool_service.go / chat_view.go / settings_view.go / tools_view.go + 3 新檔）仍是**未 commit 狀態**

#### P7-4：跨平台 / 打包（待辦）
- [ ] macOS / Linux 建置驗證（Fyne 支援；目前只 Windows）
- [ ] Windows 簽章 / MSI 打包（`fyne package` 或 WiX）
- [ ] Auto-update 機制（檢查 GitHub release / 自架 update server）

#### P7-5：設定 / TLS 強化（待辦）
- [ ] 改用 mTLS（配合 P4-4 Gateway TLS 終端）
- [ ] 「Trust on first use」對自簽憑證做 pin（指紋記住，再次連線比對）
- [ ] 切多環境（dev / staging / prod profile，一鍵切換）

---

### P5 — 雜項與重構

#### P5-1：可選優化
- [ ] `IngestService` 改為 channel-based worker pool（限制並發數）— 可參考已有的 `services.WorkerPool` 模式
- [ ] `SkillProvider` 介面 + `RemoteSkillLoader`（為 Skill Store 拆分鋪路）
- [ ] `memory_client.go`（為 agent_memory gRPC 化鋪路）

#### P5-2：K8s 部署落地
- [ ] 把 `setup.html` 中的 K8s manifests 從 inline 範例展開為 `k8s/` 目錄真實 YAML（namespace / secret / configmap × 4 / deployment × 4 / svc × 4 + NodePort）
- [ ] 共用 `Dockerfile` + build script（`scripts/build-images.sh`）
- [ ] 多節點映像分發策略落地（建議：in-cluster `registry:2` Deployment）
- [ ] 第一次部署到 K8s master 192.168.50.202 並通過完整鏈路驗證

---

## 建議實作順序

```
✅ 已完成：Phase 1 Event Bus + Phase 2 RAG ingest
✅ 已完成：P0-1 / P0-2 / P0-3（核心 RAG 鏈路通電）
✅ 已完成：P1-1（Tool Calling 全鏈，Ollama provider）
✅ 已完成：P1-3（重試 / trace / worker pool）
✅ 已完成：P2-1（單元測試補齊）
✅ 已完成：hidb 統一 + hypgo v0.8.6 + 本地 LLM 串接 + K8s 部署文件 + P2-2 memory CRUD
✅ 已完成：P4-2（agent_memory gRPC 化）+ 6 類記憶全補齊 + 全服務 TLS + agent_client Coding Agent
```

### 下一步建議（依「投資 / 報酬 / 阻擋什麼」排序，2026-07-03 重新排序）

| 優先 | 項目 | 為何先做 | 估時 |
|:---:|------|------|:---:|
| ★★★ | **P7-6 收尾：commit agent_client Coding Agent 變更**<br>目前 8 個檔案改動 + 3 新檔仍未 commit | 功能已完成、有測試（18 cases），但「未 commit」代表隨時可能被覆蓋或遺失；應是最優先的收尾動作，成本近乎零。 | <1 天 |
| ★★★ | **P1-5 Guardrails**<br>拒絕清單 + max input/tokens + 工具白名單 | agent_client 現在能讓本地 LLM **直接寫入使用者磁碟檔案**（雖有 diff 審核閘門），加上 K8s 對外暴露在即，安全邊界的急迫性比先前評估更高。 | ~2 天 |
| ★★★ | **P2-2 第二項：gRPC 全鏈路 integration test (mock LLM)**<br>把剛 live 跑通的鏈路用 bufconn + mock LLM 寫成自動化測試 | 已用 `inbound-test` live 證明鏈路通，且 memory 已改真 gRPC（P4-2 完成），趁脈絡還在把測試補齊，比之後重做便宜。 | ~1 天 |
| ★★ | **P5-2 K8s 真正部署落地**<br>展開 setup.html → k8s/*.yaml 並完成首次部署 | 單機完整鏈路（含新 TLS + gRPC memory）已驗證，趁餘溫把單機 → K8s 的「最後一哩」走完。 | ~2 天 |
| ★★ | **P4-4 Gateway 生產化**<br>Rate Limit + Ingress + metrics/access log | ClientGateway 已是唯一 client 入口，但生產部署還缺限流、Ingress、觀測；K8s 落地時同步做。 | ~2 天 |
| ★ | **P4-1 Embedding gRPC 化**<br>Embedding + Vector Search 拆 proto + binary | 架構 v2 第一塊里程碑；HTTP→gRPC 雙跑時間長但風險低。可延到 P1-5 完成後再啟動。 | ~2 天 |
| ★★ | **P6-1 + P6-2 Memory v2 第一波**<br>真摘要 + 滑動窗口 | 來自 `docs/agent_memory.svg` 規劃；注意這與已完成的「6 類記憶」是不同層次（見 P6 章節說明）。摘要讓 episodic 內容有用，sliding window 防爆量。 | ~3 天 |
| ★ | **P6-3 ~ P6-5 Memory v2 後段**<br>Policy / 雙時態 / 自動固化 | 較重投資，建議上述項目都告一段落後再啟動。可顯著拉開「toy agent」與「production agent」差距。 | ~9 天 |
| ○ | P1-4 Streaming / Cost、P1-X 相似度排序、P7-3/P7-4/P7-5、P3-2 文件 | 「nice-to-have」，等核心進 prod 後再排。 | — |

### 推薦路徑（一條最划算的線）

```
本週：  P5-2 K8s 部署落地         ← 把剛寫的 setup.html 兌現
下週：  P2-2 第二項（gRPC chain test）+ P1-5 Guardrails 並行
        ↓
月底：  P4-4 Gateway 生產化（Rate Limit / Ingress / metrics）
        ↓
中期：  P4-1 → P4-3（架構 v2 漸進拆分；P4-2 已完成）
長期：  P3-2 API 文件 + P6 Memory v2
```

---

## 變更歷史

| 日期 | 內容 |
|------|------|
| 2026-04-27 | 初版 TODO.md（8 步驟框架） |
| 2026-04-27 | TODO2.md（RAG Pipeline 差距分析） |
| 2026-04-29 | TODO3.md（A2A + Multi-Agent 拆分計劃） |
| 2026-04-30 | Phase 2 RAG ingest 完成（agents → agent_embedding HTTP 整合） |
| 2026-05-01 | Phase 1 Event Bus 完成（Redis Stream） |
| 2026-05-01 | 整合三檔成 TODO4.md，移除已完成項目 |
| 2026-05-08 | P0-4 放棄 sparse 路徑：BGE-M3 sparse 工程量過高，dense-only 已足；PG `chunks_sparse` 仍 DROP，向量資料一律 Cassandra dense |
| **2026-05-17** | **P0-1 / P0-2 / P0-3 全完成；P1-1 全完成（含 Ollama ChatWithTools + agent_grpc 接線）；P1-3 全完成；P2-1 全完成；新增 SVG 對照表；`docs/MIGRATION.md` 已刪除** |
| **2026-06-02** | **(1)** hypgo v0.8.5→v0.8.6 全升；**(2)** PG/Redis/Cassandra 全部改走 `hypgo/pkg/hidb`（含 agents + agent_embedding；agent_memory / agent_gateway 早已遵循）；**(3)** P2-2 第一項 memory_service 真 CRUD（真 PG + build tag + schema 隔離，8 cases 全 PASS）；**(4)** 本地 LLM 串接（llm.local.yaml 同時支援 llama.cpp custom 與 Ollama provider）；**(5)** gRPC 完整鏈路 live test（cmd/inbound-test，episodic 0→1→2 證明閉環學習）；**(6)** K8s + containerd 部署文件（setup.html 含 SVG 圖、port 分配、nerdctl build、K8s manifests，LLM 改 cluster 內 Ollama）；**(7)** P5-2 已替換為「K8s 部署落地」項目；**(8)** 對照 `docs/agent_memory.svg` 補上 P6 章節（Memory v2：摘要 / 滑動窗口 / Policy / 雙時態 / 自動固化技能 / Ledger），含差距分析表 |
| **2026-06-05** | 新增 **P7** Agent Client 章節：Windows Fyne 桌面 client（對話 / 上傳 / 工具 / 設定四分頁），對接 `POST /api/chat`（含 client 端工具回合制）與 `POST /api/ingest/upload`；P7-1 / P7-2 完成（基礎四分頁 + 本地工具 allowlist execv），P7-3 ~ P7-5 待辦（UX 強化 / 跨平台打包 / TLS 強化） |
| **2026-06-06** | **(1) ModelRegistry**：`agents/app/services/model_registry.go` 集中管理 192.168.50.104:11434 上 14 顆模型（含 2 顆未載入），定義 11 種 `ModelRole`（chat / chat_quality / tool_calling / vision …）；`OllamaConfig` / `VisionConfig` 加 `Role` 欄位；`main.go` LLM 與 Vision 兩條路徑都改走 registry 解析（明示 model 仍最高優先，model 空 + role → registry 依 ロード済み 優先 fallback）；`config/llm.yaml` 加說明、`config/config.yaml` 視覺改 `role: "vision"`；測試 9 cases 全 PASS。**(2) P3 範圍調整**：取消原「自建 Chat Web App / Slack / Discord bot」需求，改為兩條接入路徑（agent_client + API/A2A），P3-1 多數項目改為 ✅（已存在 endpoint），剩 A2A 完整化 + OpenAPI 文件 |
| **2026-07-02** | **agent_client 重構為 HypGo 風格**（`app/{models,services,views}`，含 typed errors / hypgo logger / desktop schema）；新增 **視窗程式背景執行**（`ToolSpec.Background` + `launchBackground()`，避免 GUI 程式被誤判逾時砍掉）；新增**內建 Coding Agent**（`code_service.go` + `diff_dialog.go`：read/list/search/edit/write 5 工具，比照 Claude Code 語意，寫入前強制 diff 審核，路徑限制工作目錄內）；新增對話頁 **Normal／Code 模式切換**（雙重開關防止誤觸編輯狀態）。詳見 [agent_client/CLAUDE.md](agent_client/CLAUDE.md)。此批**尚未 commit**（見 P7-6） |
| **2026-07-03** | **(1)** `agent_registry` Search() 路由修正（skill-id 精確比對優先，避免撞殘留死資料）；**(2)** 全 5 服務 HTTP/2 + 真 TLS（複用 `agents/scripts/gencert/`）；**(3)** gRPC TLS（`agents:9091` / `agent_gateway:9090`）；**(4)** **P4-2 完成**：agents 改真 gRPC 呼叫 `agent_memory`（新 `memory_grpc_client.go`，`config.memory.grpc_addr` 空則 fallback 直連 DB；`agents/app/controllers/memory.go` REST 代理仍直連 DB，屬已知殘留）；**(5)** 補齊 **semantic**（`agent_memory` LongTermMemoryService 首次真正跑通，順手修好兩個潛伏 bug：Cassandra 表未建立、gocql `[]float32` marshal）/ **shared**（新 PG 表 + RPC + `pool_id`）/ **short-term**（Redis `SessionStore`，maxTurns=40 TTL 2h）三類記憶，至此 6 類記憶全部接進推理流程；**(6)** 各服務 CLAUDE.md 同步更新。詳見 [memory/20260703_log.md](memory/20260703_log.md)。兩次 commit：`3cdc0a5`、`ca19559` |
| **2026-07-03（稍晚）** | **P4-2 收尾**：移除 (4) 留下的已知殘留——刪除 `agents/app/controllers/memory.go`（REST 代理）+ `app/routers/memory.go` + 直連 DB 的 `app/services/memory_service.go`（含其兩個測試檔）；`config.memory.grpc_addr` 改必填，撥號失敗直接 exit，不再 fallback 直連 DB；`agents/app/models/init.go` 的 `AutoMigrate` 不再建 4 張記憶表（改由 `agent_memory` 自己的 AutoMigrate 擁有）。至此 `agents` 對記憶資料的存取只剩「gRPC → `agent_memory`」一條路徑；需要記憶 CRUD-by-id 的呼叫端改直接打 `agent_memory` 的 REST（`:8501`，路由與舊版相同）或 gRPC（`:8510`）。`go build`/`go vet`/`go test` 全過。三份 CLAUDE.md 同步更新。 |
| **2026-07-09** | **TODO4.md 依 phase 重整 + A2A 需求移除**：dev_pub_0.9 起 `pub_client`/`rag_admin`/`mobile_client` 三個 client 全部收斂到 `agent_gateway` 的 ClientGateway gRPC 通道（單一入口 + API Key 一 Key 一裝置 + 內部 mTLS/HTTP 轉發），A2A JSON-RPC 對外協定層不再需要。移除項目：P1-2「A2A 完整化」整章、P3-1「路徑 B — A2A JSON-RPC」、P3-2「A2A AgentCard JSON 範例」、P4-4 中的 A2A JSON-RPC 翻譯項目、優先級表與推薦路徑中的 P1-2。保留：`AgentCardBuilder`、`/.well-known/agent-card.json`、`:8090` A2A HTTP 端點（skill discovery 用；外網不曝光）。同時將 P3-1 接入路徑更新為三個 client + 內部 REST 兩層；P4-4 改寫為「Gateway 生產化」（Rate Limit / Ingress / metrics） |
| **2026-07-09** | **新增 `mobile_client/`（Android 手機版 client，P7 延伸）**：pub_client 功能移植到 Android（Go + Fyne v2.5.5 獨立 module）——Chat（含 ChatStatus 輪詢/附圖辨識/session 歷史）/ Upload（SAF 選檔 in-memory 上傳 + Verify）/ Graph（觸控版 force-directed 圖 + 待審審核）/ Settings 四分頁，全走 gateway gRPC + API Key。平台替代：DuckDB→AES-256-GCM 加密 JSON、MachineGuid device_id→隨機持久化、本機工具/Coding Agent/Vault 不移植（tool_calls 回錯續推理）。`fyne package -os android/arm64` 產出 APK（NDK 29；fyne v2.4.x 會因 ALooper_pollAll 編不過，故用 v2.5.5）。build/vet/test 全綠；實體手機端到端待部署機。見 [mobile_client/README.md](mobile_client/README.md) |
