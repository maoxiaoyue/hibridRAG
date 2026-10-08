# agent_critic — 答案品質評估服務（A3 Critic / Gatekeeper）

> 監聽埠：`:8503`（HTTP/2, TLS）+ `:8511`（gRPC, TLS）
> 設定檔：`config/config.yaml`
> 建立日期：2026-07-04（HypGo v0.8.11，比照 agent_memory 慣例）

---

## 功能概述

`agent_critic` 對「question + answer + sources」做品質評估，輸出三維評分與修訂建議，
供 agents 服務（或 orchestrator）決定是否進入 revision loop。對應
[docs/Agentic、Graphic RAG Hybrid.md](../docs/Agentic、Graphic%20RAG%20Hybrid.md) 中的 **A3 Critic / Gatekeeper**。

評分維度（0-1，越高越好）：

| 維度 | 語意 | 基線算法（LexicalScorer） |
|------|------|---------------------------|
| `completeness` | 答案是否回應了問題 | 問題關鍵詞在答案中的覆蓋率（排除疑問詞/功能詞） |
| `grounding` | 答案是否有證據支持 | 答案關鍵詞在 sources 中的支持率；**無 sources 時為 0** |
| `faithfulness` | 逐句是否忠於證據 | 「至少三成關鍵詞有 sources 支持」的句子比例 |

`overall` = 加權平均（權重 `critic.weights`，預設 0.4/0.3/0.3）；`passed` = overall ≥ `critic.pass_threshold`（預設 0.7）。
關鍵詞抽取：ASCII 單詞（≥3 字元、排除英文停用詞）+ CJK 相鄰雙字 bigram（排除純功能字組合），中英混合皆可。

**基線評分器定位**：`app/services/scorer.go` 的 `LexicalScorer` 是不依賴 LLM 的詞彙級近似，
之後換成 LLM-as-judge / NLI 時以相同的 `Score(question, answer, sources) ScoreResult` 介面替換，
評分語意與 feedback 結構不變。每筆評估連同 question/answer/sources 原文落地 PG
（`critic_evaluations` 表），可作為日後訓練/校準 judge 的資料。

---

## API 端點（已驗證）

### REST（`:8503`，HTTP/2 h2 + 自簽 TLS）

| Method | Path | 說明 |
|--------|------|------|
| `GET` | `/health` | 健康檢查（PG 必要） |
| `POST` | `/api/critic/evaluate` | 評估並落庫，回 201 + 完整評分（`agent_id`/`question`/`answer` 必填） |
| `GET` | `/api/critic/evaluations/:id` | 取單筆評估紀錄 |
| `GET` | `/api/critic/evaluations?session_id=&limit=` | 依 session 取最近評估（revision loop 追蹤，預設 20 筆） |

### gRPC（`:8511`，`proto/critic.proto`，`agent_critic.v1.CriticService`）

| RPC | 說明 |
|-----|------|
| `Evaluate` | 同 REST evaluate；缺必填欄位回 `codes.InvalidArgument` |
| `GetEvaluation` | 取單筆；不存在回 `codes.NotFound` |
| `ListBySession` | 依 session 列表 |

TLS 沿用 HTTP server 的憑證（`server.tls.*`，`scripts/gencert` 產生的 10 年自簽憑證）。

---

## 依賴

- PostgreSQL（`critic_evaluations` 表，AutoMigrate 自建）
- 不依賴其他微服務、不依賴 Cassandra/Redis 執行期功能（Redis 設定僅為 hidb 介面要求）

**呼叫方**：`agents` 服務——`app/services/critic_grpc_client.go`（`CriticGRPCClient`）於 gRPC Inbound
推理路徑（`process()`）產出答案後呼叫 `Evaluate`，依 `passed` 決定是否重新生成（`AgentServer.reviseWithCritic`，
最多 `critic.max_revisions` 次）。詳見 [agents/CLAUDE.md](../agents/CLAUDE.md) 2026-07-04 修正紀錄。
**`/api/chat` 同步對話路徑尚未接線**（continuation-based 多輪迴圈，形狀與 `process()` 不同）。

---

## 已驗證項目（2026-07-04）

- `go build ./...`、`go vet ./...` 零警告；`app/services/scorer_test.go` 4 個單元測試通過（純邏輯，不需 DB）。
- 對真實環境（192.168.50.201 PG）端到端煙霧測試：REST `POST /api/critic/evaluate` → 落庫 →
  `GET /api/critic/evaluations?session_id=` 命中；grpcurl `Evaluate`（TLS）回傳正確評分。測試資料已清除。
- 中文（bigram）與英文（單詞）關鍵詞抽取皆驗證有效；無 sources 時 grounding/faithfulness=0 且 feedback 提示。

## 待辦 / 下一步

- [x] `agents` 服務接上 CriticService gRPC client，在 process() 產出答案後評估、依 `passed` 進 revision loop（2026-07-04，見上方「呼叫方」與 [agents/CLAUDE.md](../agents/CLAUDE.md)）
- [ ] `/api/chat`（`agent_chat.go` runChatLoop，同步對話路徑）比照接上 revision loop
- [ ] LLM-as-judge 評分器（替換 LexicalScorer，同介面）；`config/llm.yaml` 比照 agents 服務
- [ ] 向 agent_registry 註冊 Agent Card（skill: `answer-critique`），讓 gateway 可路由
