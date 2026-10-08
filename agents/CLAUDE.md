# Single Agent - 工作總結

> 最後更新：2026-04-27

## 專案定位

單一 agent 服務，對外透過 **gRPC** 接收訊息，並可作為 client 將訊息轉發給其他下游 agent。
HTTP 側保留 Ingest 相關 REST API（RAG 用途）；Memory 資料一律透過 gRPC 打 `agent_memory`，
本服務不再自己暴露 `/api/memory/*` REST（2026-07-03 收斂，見下方修正紀錄）。

- Module：`agent`
- Framework：HypGo v0.8.5 + Bun ORM
- Go：1.24+

---

## 訊息流（外部 agent 連線）

```
上游 API (另一服務)
    │  gRPC Unary: agent.v1.AgentService/Inbound(InboundReq) → InboundAck
    ▼
┌─────────────────────────────────┐
│ 本 Agent                         │
│  1. 驗證 payload                  │
│  2. 立即回 InboundAck (202/200)   │
│  3. 非同步處理 → 產 OutboundMsg    │
└─────────────────────────────────┘
    │  gRPC Unary: 呼叫下游的 Inbound
    ▼
下游 agent (依 OutboundMsg.To 查 config)
```

- **Inbound 模式**：Unary（上游推一包、立即 ack、agent 內部非同步處理）
- **Outbound 模式**：本 agent 作為 client 呼叫下游的 `Inbound` RPC；輸出契約 `OutboundMsg` 顯式帶 `From/To/Direction`
- **認證**：目前免驗證（內部服務網路信任）；未來若對外暴露改走 interceptor
- **傳輸層 TLS**：gRPC server（`:9091`）與 HTTP server 共用同一組自簽憑證（`certs/server.crt`+`server.key`，`server.tls.enabled: true` 時生效）；`downstream.agents[]` 呼叫下游時依各自 `tls_enabled`/`ca_file` 設定

---

## 核心型別（由 proto 生成）

位置：[app/models/agent.pb.go](app/models/agent.pb.go) / [app/models/agent_grpc.pb.go](app/models/agent_grpc.pb.go)
來源：[proto/agent.proto](proto/agent.proto)

| 型別 | 用途 |
|------|------|
| `AgentReq` | 核心請求載體（session_id / user_id / agent_id / messages / metadata） |
| `AgentResp` | 核心回應載體（delta / done / finish_reason / code / error） |
| `Message` | role + content |
| `Metadata` | model + temperature + stream |
| `InboundReq` | 上游 → 本 agent：trace_id + from + payload(*AgentReq) |
| `InboundAck` | 本 agent → 上游：trace_id + message_id + accepted + code + error |
| `OutboundMsg` | 本 agent → 下游：trace_id + from + to + direction + payload(*AgentReq) |

gRPC service：`agent.v1.AgentService/Inbound(InboundReq) returns (InboundAck)`

**生成指令**：`make proto`（呼叫 protoc，把 `agent/app/models/*.pb.go` 移回 `app/models/`）

---

## Outbound Dispatcher

[app/services/outbound.go](app/services/outbound.go)

- `Dispatcher` 介面：`Send(ctx, *OutboundMsg) → (*InboundAck, error)` + `Close() error`
- `GrpcDispatcher` 實作：
  - 欄位：`cfg *DownstreamConfig` + `conns map[string]*grpc.ClientConn` + `sync.Mutex`
  - `Send` 流程：`ValidateOutbound` → `cfg.Find(msg.To)` → 查無則 502/`ErrNoRoute` → `getOrDial` → `client.Inbound()` → 錯誤依 `DeadlineExceeded` 區分 502/504
  - 連線 lazy dial + pool 重用
  - TLS：讀 `ca_file` 組 `x509.CertPool`，支援 `ServerName` 與 `InsecureSkipVerify`
- `ToInboundReq(*OutboundMsg) *InboundReq` — 下游端點收到的即為 `InboundReq`，`From` = 本 agent
- `ValidateOutbound` — 擋缺 `To` / 空 messages / 缺 direction

---

## 下游設定

[app/services/downstream_config.go](app/services/downstream_config.go) + [config/config.yaml](config/config.yaml) 的 `downstream:` 區塊

```yaml
downstream:
  defaults:
    dial_timeout: 3s
    call_timeout: 5s
    tls_enabled: false
    insecure_skip_verify: false
    max_retries: 2
    retry_backoff: 200ms
  agents:
    - id: "agent-b"          # 對應 OutboundMsg.To（範例：本 agent 自己的 gRPC port，已開 TLS）
      endpoint: "127.0.0.1:9091"
      tls_enabled: true
      ca_file: "certs/server.crt"   # 自簽憑證本身即為 CA（gencert 產生時 IsCA: true）
      call_timeout: 5s
      tags: ["primary"]
    - id: "agent-c"
      endpoint: "agent-c.internal:9091"
      tls_enabled: true
      ca_file: "certs/downstream-ca.crt"
      server_name: "agent-c.internal"
```

- `DownstreamConfig.Find(id)` 以 `agent.id` 查 endpoint 設定
- `ApplyDefaults()` 將 `defaults` cascade 到未指定的 agent 欄位
- `Validate()` 擋重複 id / 缺 endpoint / TLS 開但無 CA

---

## Schema 註冊

[app/routers/agent.go](app/routers/agent.go) — 透過 HypGo `schema.Global().Register()` 註冊兩條：

| Command | 方向 | Input → Output |
|---------|------|----------------|
| `agent.v1.AgentService/Inbound` | server RPC | `InboundReq` → `InboundAck` |
| `agent.v1.AgentService/Outbound` | client 契約 | `OutboundMsg` → `InboundAck` |

供 hyp context / contract testing / manifest 使用。

---

## 保留功能（REST 側）

- `/health`、`/metrics`
- `/api/ingest/{skill,references,jobs/:id}` — SKILL.md / references 攝取管線

## Persona 模型設定端點（內網 / dev_090 專用，2026-07-07）

讓 `agent_client`（內網）逐 persona 設定推理用 model；**gateway 不轉發**（外網 client 無此能力）。

| Method | Path | 說明 |
|--------|------|------|
| `GET` | `/api/personas` | 掃 `skills/` 列出所有含 SKILL.md 的 persona + 目前 model |
| `GET` | `/api/personas/:agent_id` | 單一 persona 的模型設定現況 |
| `PUT` | `/api/personas/:agent_id/model` | body `{model, role}`：設定該 persona 推理模型 |

**關鍵：事實來源是 `SKILL.md` 的 `## Model` 段，不是 ROLE.md。** runtime 從不讀 ROLE.md
（ROLE.md 是 `RoleCompiler` 產生的衍生檔）。`/api/chat` 挑模型的鏈路是
`AgentServer.resolveLLM(agentID)` → `CompilingSkillLoader.ModelResolutionFor` → `llmPool.Get(model)`，
而 `ModelResolution` 由 SKILL.md 重讀時的 `persona.Parse` + `RoleCompiler.Compile` 產生。

因此 `PUT .../model` 的流程（`app/services/persona_admin.go` 的 `SetModel`）是：
1. `persona.UpsertModelSection`（`internal/agent/persona/edit.go`）把 model/role 寫進
   SKILL.md 的 `## Model` 段（新增或整段替換，其餘保留）；
2. `compilingLoader.Load(agentID, force=true)` force reload → 重生 ROLE.md + 刷新 ModelResolution
   → **`/api/chat` 立即改用新模型**。

注入：main.go 於 `setupRoutes` 前 `controllers.SetPersonaAdmin(services.NewPersonaAdmin(skillsDir, compilingLoader))`；
路由在 `registerPersonaRoutes`。相關檔案：`app/controllers/persona.go`、`app/services/persona_admin.go`、
`internal/agent/persona/edit.go`（含 `edit_test.go`）。

> 模型「目錄」（有哪些模型可選、各自 modalities）由 `agent_registry` 的 `/registry/models`
> 提供（打 Ollama 探得）；本服務只負責把選定 model 落地到 persona 的 SKILL.md。

## 對話階段狀態端點（client 每秒輪詢，2026-07-07）

| Method | Path | 說明 |
|--------|------|------|
| `GET` | `/api/chat/status/:session_id` | 回報該 session 目前處理到混合 RAG 的哪個 Agent 階段：`{stage, active}` |

用途：client 對話中的狀態列把「目前哪個 Agent 在處理」顯示在秒數前（取代只跳秒數）。
client 不直接打此端點——由 `agent_gateway` 的 `ClientGateway.ChatStatus` 經內部 HTTP 呼叫。

機制（`Chat` 是 unary，子階段全在本服務同一 blocking call 內跑，故用 in-memory 追蹤 + 輪詢）：
- `app/services/chat_status.go` 的 `ChatStatusTracker`：`session_id → stage`（RWMutex，5min TTL，
  方法容忍 nil receiver）。階段標籤：`Agent 1 · ContextBuilder` / `Agent 2 · ToolLoop` /
  `Agent 3 · Critic`（目前僅 gRPC Inbound 路徑）/ `Agent 4 · Graph`。
- `AgentServer.WithStatus(tracker)` 注入（main.go 建一顆，同時給 `controllers.SetChatStatusTracker`）。
- 埋點（`app/controllers/agent_chat.go`）：`ChatSync` 進場設 ContextBuilder；`runChatLoop` 每輪設
  ToolLoop、`graph_query` 工具設 Graph、`defer` 離場 Clear（終局或暫停等 client 工具時清掉 →
  輪詢回 active=false → client 顯示自己的本地階段）。
- 前提：client 新對話首輪自帶 `session_id`（client 端產生），server 沿用它當 tracker key，輪詢首輪
  才有 key 可查。

## 對話附加圖片辨識端點（2026-07-07）

| Method | Path | 說明 |
|--------|------|------|
| `POST` | `/api/chat/image?agent_id=&filename=` | body=原始圖片 bytes；抽文字（供當下對話辨識）+ ingest 進 RAG，回 `{text, chars}` |

用途：client 對話附加圖片時,server 用 VisionExtractor 把圖片 OCR/描述成文字,client 再把文字併進
該輪訊息,讓（可能純文字的）對話模型也能「辨識」圖片內容。client 不直接打——由 `agent_gateway`
的 `ClientGateway.ChatImage` 經內部 HTTP 轉發。

實作（`app/controllers/chat_image.go`,重用 `ingest_upload.go` 的既有 helper）：落地 uploads/{agent_id}/
→ `extractToText`（圖片走 `VisionExtractor`,需 `ingest.vision.enabled`；未設定回 422）→ 讀回文字 →
`EnqueueReferences`（tag `chat-image`,best-effort 同時入庫,未來可檢索）→ 回傳文字。上限 32 MiB。

記憶系統 CRUD（`personal`/`procedural`/`episodic`/`tool`）**不再**由本服務以 REST 暴露——
本服務自己的 `/api/memory/*` 代理（直連 DB）已於 2026-07-03 移除；需要記憶 CRUD 的呼叫端
請直接打 `agent_memory` 的 REST（`:8501`，路由與舊版完全相同）或 gRPC（`:8510`）。
本服務內部（ContextBuilder / EpisodicWriter / memory_read·memory_write 工具 / IngestRunner）
一律透過 [app/services/memory_grpc_client.go](app/services/memory_grpc_client.go) 呼叫 `agent_memory` 的 gRPC。

相關原始碼：[app/controllers/ingest.go](app/controllers/ingest.go)、[app/services/ingest_service.go](app/services/ingest_service.go)、[app/services/memory_grpc_client.go](app/services/memory_grpc_client.go)、[app/models/memory.go](app/models/memory.go)（request/response DTO，仍由 gRPC client 轉換用）、[app/models/ingest.go](app/models/ingest.go)

---

## 已移除的 template 範本

| 項目 | 原因 |
|------|------|
| `app/models/user.go` | 未使用，原本即為非合法 Go 檔 |
| `app/services/user_service.go` | 同上 |
| `app/services/auth_service.go` | User 認證範本，agent 場景不需要 |
| `app/controllers/api.go` | User CRUD template |
| `app/middleware/auth.go` | JWT / RequireRole，只給 User 用 |
| `app/routers/router.go` | 引用不存在的 `ChatController` |
| `config.yaml` 的 `api.jwt` 區塊 | 配合上述移除 |
| `main.go` 中 `/auth/*`、`/users/*`、`/ws` 路由與 JWT middleware | 配合上述移除 |

---

## 建置與工具

[Makefile](Makefile)

| Target | 說明 |
|--------|------|
| `make proto` | 由 `proto/agent.proto` 生成 `app/models/agent*.pb.go` |
| `make install-tools` | 安裝 air / golangci-lint / swag / migrate / **protoc-gen-go** / **protoc-gen-go-grpc** |
| `make build` / `make run` / `make test` | 標準建置、執行、測試 |

**工具路徑（本機已安裝）**
- `protoc.exe` v34.1 → `D:/GoProjects/bin/protoc.exe`
- `protoc-gen-go.exe` → `D:/GoProjects/bin/`
- `protoc-gen-go-grpc.exe` → `D:/GoProjects/bin/`
- 對應 Go module cache 路徑：`D:/GoProjects/pkg/mod/`

---

## RAG Pipeline — 已完成模組

> 詳細差距分析見 [TODO2.md](TODO2.md)

| 模組 | 路徑 | 說明 |
|------|------|------|
| SKILL.md 解析器 | `internal/agent/persona/parser.go` | Parse() → ParseResult（含 ToInstruction() 渲染為 LLM system prompt） |
| IngestRunner | `internal/agent/ingest/runner.go` | RunSkill: persona 解析 → UpsertPersonal/UpsertProcedural → PG |
| LLM Client | `app/services/llm_*.go` | 統一 `Chat()` 介面，支援 Ollama / Anthropic / OpenAI / OpenAI-compatible |
| **llm-demo CLI** | `cmd/llm-demo/main.go` | 端到端驗證：SKILL.md → Parse → ToInstruction → Chat（單輪 / REPL） |
| **gRPC Inbound Handler** | `app/controllers/agent_grpc.go` | AgentServer：InboundReq → SkillPack → LLM → OutboundMsg（端到端可運作） |
| 範例 SKILL.md | `skills/demo/SKILL.md` | 中文 agent「小柴」，3 個心智模型，可立即跑 demo |
| BGE-M3 Embedding | `internal/agent/embed/bge_m3.go` | Ollama + TEI 雙後端，dense[1024] + sparse |
| Cassandra ANN | `internal/agent/vectordb/cassandra.go` | Dense 向量存取 + SearchANN |
| DocumentChunker | `internal/agent/ingest/document_chunker.go` | 512字元/50重疊，支援 md/json |
| MemoryGRPCClient | `app/services/memory_grpc_client.go` | Personal/Procedural/Episodic/Tool/Semantic/Shared CRUD，全部經 gRPC 打 `agent_memory`（2026-07-03 起唯一實作，不再有直連 DB 版本） |
| SkillLoader | `app/services/skill_loader.go` | mtime+SHA256 快取，載入 SKILL.md + references/ |
| **CompilingSkillLoader** | `app/services/skill_compile.go` | 包一層 SkillLoader；真正重讀時觸發 `RoleCompiler.Compile`，快取 `ModelResolution`（2026-07-04，見修正紀錄） |
| **RoleCompiler** | `app/services/role_compiler.go` | 解析 persona 的 `## Model` 偏好 → `ModelRegistry` 解析 → 寫 `skills/{agent_id}/ROLE.md`（2026-07-04） |
| **LLMClientPool** | `app/services/llm_pool.go` | 依 model 名稱快取 `*OllamaClient`，支援每個 persona 各自指定推理用模型（2026-07-04，僅 ollama 模式） |
| **AgentCardBuilder** | `app/services/agent_card.go` | 從 persona.ParseResult + config 動態組 A2A Agent Card（2026-07-04，P1-2） |

### SKILL.md 解析器規格（`internal/agent/persona`）

**SKILL.md 格式**（支援中英文 section 標題）：
```markdown
# Agent Name          ← AgentName
## Identity           ← PersonalMemory.Identity（bullet key: value）
## Expression DNA     ← PersonalMemory.ExpressionDNA（bullet key: value）
## Mental Models      ← 多筆 ProceduralMemory
### Model Name        ← ProceduralMemory.ModelName
Content...            ← ProceduralMemory.Content
**Heuristics**        ← ProceduralMemory.Heuristics（bullet key: value）
- key: value
```

中文別名支援：`身份卡`、`表達 DNA`、`心智模型`、`決策啟發式`、`啟發式`

**對外 API**：
- `Parse(body string) *ParseResult` — 純解析，無副作用
- `result.ToPersonalMemoryRequest(agentID)` → `*CreatePersonalMemoryRequest`（nil 表示無需寫入）
- `result.ToProceduralMemoryRequests(agentID)` → `[]CreateProceduralMemoryRequest`
- `result.ToInstruction() string` → 渲染成 LLM system prompt（可餵 `llmagent.Config.Instruction`）

**ToInstruction 輸出範例**：
```
You are SupportBot, Customer Support Specialist. Your domain is E-commerce.

## About You
- language: zh-TW

## Communication Style
- style: concise, structured
- tone: warm yet professional

## Mental Models

### First Principles Thinking
Break problems into atomic components.

Heuristics:
- challenge_assumptions: Ask "why" 3 times
```

特性：
- 中文 alias：`名稱`/`名字` → name slot；`角色` → role；`領域`/`領域範圍` → domain
- 確定性輸出：所有 map 以 key 字典序排序（MentalModels 維持 SKILL.md 原順序）
- 缺欄位容錯：無 ExpressionDNA 不出 `## Communication Style`，無 heuristics 不出 `Heuristics:`
- AgentName fallback：Identity 沒設 `name` 時用 H1
- 空 result → 回傳空字串（呼叫端決定 fallback）

測試：`internal/agent/persona/parser_test.go`（18 cases，全 PASS）+ `example_test.go`（一字不差比對輸出格式）

### IngestRunner 規格（`internal/agent/ingest/runner.go`）

**依賴注入介面**（`MemoryWriter`）：
```go
type MemoryWriter interface {
    UpsertPersonal(ctx, req)   (*PersonalMemory, error)
    UpsertProcedural(ctx, req) (*ProceduralMemory, error)
}
```
由 `app/services.MemoryGRPCClient` 滿足（gRPC → `agent_memory`；2026-07-03 前是直連 DB 的 `*services.MemoryService`，已移除）。

**RunSkill 流程**：讀檔 → `persona.Parse()` → UpsertPersonal → UpsertProcedural×N → 更新 `job.TotalChunks`/`DoneChunks`

**RunReferences**：Phase 2（需 embed client + vectordb），目前回傳明確 error，job.TotalChunks 仍填路徑數。

**UpsertPersonal**：`ON CONFLICT (agent_id) DO UPDATE`（personal_memory 有 unique index）

**UpsertProcedural**：SELECT-then-insert/update（agent_id + model_name 無 unique 約束）

測試：`internal/agent/ingest/runner_test.go`（7 cases，全 PASS）

### LLM Client 規格（`app/services/llm_*.go`）

**統一介面**：
```go
type LLMClient interface {
    Chat(ctx, ChatRequest) (*ChatResponse, error)
    Provider() string
}
```

**支援的 provider**：
| Provider | Endpoint | 備註 |
|----------|----------|------|
| `ollama` | `{url}/api/chat` | 本地/內網，免 api_key |
| `anthropic` | `{base_url}/v1/messages` | system 自動抽到 top-level；header 帶 `x-api-key` + `anthropic-version` |
| `openai` | `{base_url}/v1/chat/completions` | header 帶 `Authorization: Bearer ...` |
| `custom` | 同 OpenAI 協定 | 自架 vLLM / LM Studio / LocalAI；`api_key` 可選 |
| `gemini` | — | 不支援（建議走 ADK `gemini.NewModel` 直接接） |

**Config 載入**：
```go
cfg, _ := services.LoadLLMConfig("config/llm.yaml")  // ${ENV} 自動展開
client, _ := services.NewLLMClient(*cfg)
```

**設計重點**：
- Anthropic API：`system` 必須抽到 top-level 而非塞 messages 陣列，client 內部 `extractSystemAndRest()` 處理
- 找不到的 `${ENV_VAR}` 保留原字面值（不換成空字串），讓後續 Validate 能診斷
- `custom` provider 走 OpenAI-compatible 協定，`api_key` 可選（自架不需要）
- MVP 不做 streaming；之後加 `StreamingLLMClient` 介面，不影響現有 `LLMClient`
- 所有 provider 統一回 `TokenUsage{prompt, completion, total}`，方便 cost tracking

**檔案**：
- `llm_client.go` — interface + types + factory
- `llm_ollama.go` — Ollama provider
- `llm_anthropic.go` — Anthropic provider
- `llm_openai.go` — OpenAI / custom provider
- `llm_loader.go` — YAML 載入 + `${ENV}` 展開 + Validate
- `llm_client_test.go` — 18 cases httptest mock，全 PASS

### llm-demo CLI（`cmd/llm-demo/main.go`）

端到端 smoke test 工具，把 SKILL.md → persona.Parse → ToInstruction → LLM Chat 串起來。

**用法**：
```bash
# 單輪
go run ./cmd/llm-demo --skill skills/demo/SKILL.md --prompt "Go 怎麼處理 graceful shutdown？"

# 互動 REPL（保留 conversation history）
go run ./cmd/llm-demo --skill skills/demo/SKILL.md

# 顯示渲染後的 system prompt
go run ./cmd/llm-demo --skill skills/demo/SKILL.md --show-instruction --prompt "..."
```

**Flags**：`--skill`（必填）、`--config`（預設 `config/llm.yaml`）、`--prompt`、`--show-instruction`、`--temp`、`--max-tokens`、`--timeout`

**REPL 指令**：`exit` / `quit` / Ctrl+D 結束；`reset` 清 history（保留 system prompt）

**輸出**：每次回應顯示 `[model | tokens (in/out) | finish_reason | turn]`，便於 cost 與品質檢視

---

## 未完成 / 下一步

> 完整 TODO 見：
> - [TODO.md](TODO.md) — AI Agent 8 步驟框架差距分析（System Prompt、Orchestration、Testing 等）
> - [TODO2.md](TODO2.md) — RAG Pipeline 架構差距分析（IngestRunner、RRF Fusion、ContextBuilder、LLM Client 等）

### 阻擋編譯（pre-existing template 殘骸）
- `main.go` 使用 `workspace/...` import 前綴，但 module 是 `agent` → 需全改為 `agent/...`
- `app/middleware/middleware.go` 為空檔（僅 `package middleware`），但 `main.go` 引用 `middleware.RequestID()` / `Logger()` / `Recovery()` / `CORS()` / `Security()` / `Metrics()` / `RateLimit()`
- 本地無 `workspace/config` Go 套件（只有 `config/config.yaml`），`main.go` 的 `config.Load()` 無對應實作
- `app/services/tei_client.go` 缺 `github.com/sirupsen/logrus` 的 go.sum entry（`go get agent/app/services` 可修）

### RAG Pipeline 下一步（Phase 2）
- [ ] `internal/agent/ingest/runner.go` → `RunReferences` 實作（chunk → BGE-M3 embed → Cassandra dense + PG sparse）
- [ ] `migrations/004_create_chunks_sparse.up.sql` — chunks_sparse 表
- [ ] `scripts/cassandra_init.cql` — chunks_dense DDL
- [ ] `internal/agent/retrieval/hybrid.go` — 並發 Dense+Sparse + RRF Fusion
- [ ] `internal/agent/context/builder.go` — ContextBuilder（PersonalMemory anchor + ProceduralMemory top-3 + RAG top-5 + Episodic top-2）
- [ ] `app/services/llm_client.go` — LLM 生成 client（Ollama / Anthropic / OpenAI，依 config/llm.yaml 切換）

### gRPC 推理鏈
- [x] `AgentServer.Inbound` 實作（`controllers/agent_grpc.go`）：驗證 → message_id → 立即 ack → goroutine: SkillPack → LLM → OutboundMsg
- [x] main.go 啟動 gRPC listener（`:9091`），`grpc` + `agent` 加入 config
- [x] `GrpcDispatcher` 接入 main.go 生命週期（啟動時 `NewGrpcDispatcher`、停機時 `GracefulStop` + `Close`）
- [x] `message_id` 產生策略（`msg_<crypto/rand 16 hex>`）
- [ ] OpenTelemetry trace propagation（透過 `trace_id` 串接上下游）
- [ ] 重試機制（依 `defaults.max_retries` / `retry_backoff`）— 目前 Dispatcher 尚未實作 retry
- [ ] 推理佇列 / 限流（目前 goroutine 數量無界）
- [ ] EpisodicMemory 寫入（Phase 3：每輪對話寫入歷史）

---

## 關鍵檔案速查

| 領域 | 檔案 |
|------|------|
| 訊息定義 | [proto/agent.proto](proto/agent.proto) |
| 生成型別 | [app/models/agent.pb.go](app/models/agent.pb.go)、[app/models/agent_grpc.pb.go](app/models/agent_grpc.pb.go) |
| Outbound dispatcher | [app/services/outbound.go](app/services/outbound.go) |
| 下游設定 | [app/services/downstream_config.go](app/services/downstream_config.go)、[config/config.yaml](config/config.yaml) |
| Schema 註冊 | [app/routers/agent.go](app/routers/agent.go) |
| 建置 | [Makefile](Makefile) |
| **SKILL.md 解析器** | [internal/agent/persona/parser.go](internal/agent/persona/parser.go) |
| **IngestRunner** | [internal/agent/ingest/runner.go](internal/agent/ingest/runner.go) |
| **LLM Client** | [app/services/llm_client.go](app/services/llm_client.go) |
| **llm-demo CLI** | [cmd/llm-demo/main.go](cmd/llm-demo/main.go) |
| **gRPC Inbound Handler** | [app/controllers/agent_grpc.go](app/controllers/agent_grpc.go) |
| **範例 SKILL.md** | [skills/demo/SKILL.md](skills/demo/SKILL.md) |

---

## 修正紀錄

已遷移至專案工作日誌：[memory/20260703_log.md](../memory/20260703_log.md)（skill 路由修正、全服務 HTTP/2+TLS、gRPC 補 TLS、agents 改真 gRPC 呼叫 agent_memory、補齊 semantic/shared/short-term 記憶機制）、[memory/20260704_log.md](../memory/20260704_log.md)（agent_critic/agent_graph gRPC 接線 + revision loop、Registry 自動註冊、AgentID 綁定修正、動態 Agent Card + SKILL.md `## Model` 段）。
