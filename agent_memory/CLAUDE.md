# agent_memory — 記憶服務

> 監聽埠：`:8501`（預設）
> 設定檔：`config/config.yaml`

---

## 功能概述

`agent_memory` 提供 6 類記憶：`personal`/`procedural`/`episodic`/`tool`（PostgreSQL）+ `long_term`（Cassandra，semantic memory：帶 BGE-M3 embedding、SAI ANN 相似度搜尋）+ `shared`（PostgreSQL，跨 agent 協調 KV pool，以 `pool_id` 而非 `agent_id` 隔離）。對外有兩個介面：
- **REST**（`:8501`）：`personal`/`procedural`/`episodic`/`tool`/`long_term` 完整 CRUD，供人工操作/其他工具使用。`shared` 目前沒有 REST route，只走 gRPC。
- **gRPC MemoryService**（`:8510`）：`agents` 服務的 ContextBuilder/EpisodicWriter/memory_read·memory_write 工具/IngestRunner 實際呼叫的介面——涵蓋 11 個方法（見下方 API 端點表）。

沒有對外呼叫其他微服務的 client（gRPC/HTTP 都是被動接收）。

`main.go` 直接內聯啟動 server（`app/commands/root.go` 目前是沒有 `serve` 子命令的空殼，不是實際入口）。

---

## API 端點（已驗證）

### REST（`:8501`）

| Method | Path | 說明 |
|--------|------|------|
| `GET` | `/health` | 健康檢查 |
| — | `/api/memory/{personal,procedural,episodic,tool}` | 4 類記憶 CRUD（PG） |
| `POST`/`GET`/`DELETE` | `/api/memory/long_term/:id` | LongTermMemory CRUD（Cassandra，`category`: knowledge/experience/concept/fact/preference） |
| `POST` | `/api/memory/context` | 綜合上下文查詢（Personal anchor + Procedural top-N + Episodic top-N + LongTerm ANN top-N） |

### gRPC（`:8510`，`proto/memory.proto`，`agent_memory.v1.MemoryService`）

| RPC | 對應 REST/service 方法 | 呼叫方 |
|-----|------------------------|--------|
| `GetPersonalByAgent` | `MemoryService.GetPersonalByAgent` | `agents` ContextBuilder（fallback 路徑）、memory_read 工具 |
| `UpsertPersonal` | `MemoryService.UpsertPersonal` | `agents` IngestRunner |
| `ListProceduralByAgent` | `MemoryService.ListProceduralByAgent` | `agents` ContextBuilder（fallback 路徑）、memory_read 工具 |
| `UpsertProcedural` | `MemoryService.UpsertProcedural` | `agents` IngestRunner、memory_write 工具 |
| `ListEpisodicByAgent` | `MemoryService.ListEpisodicBySession`（`session_id=""` → agent 全域最近 N 筆） | `agents` ContextBuilder、memory_read 工具 |
| `CreateEpisodic` | `MemoryService.CreateEpisodic` | `agents` EpisodicWriter（每次真實對話結束後寫入） |
| `CreateLongTerm` | `LongTermMemoryService.Insert`（semantic memory 寫入；caller 需先算好 1024 維 BGE-M3 embedding） | `agents` `*services.SemanticMemory`（`embed.Client` + 本 RPC）、memory_write 工具（kind=semantic） |
| `SearchLongTerm` | `LongTermMemoryService.SearchANN`（COSINE ANN） | `agents` ContextBuilder（semantic quota）、`SemanticMemory`、memory_read 工具（kind=semantic） |
| `GetShared` | `SharedMemoryService.Get` | `agents` memory_read 工具（kind=shared） |
| `UpsertShared` | `SharedMemoryService.Upsert`（`ON CONFLICT (pool_id, key)`） | `agents` memory_write 工具（kind=shared） |
| `ListShared` | `SharedMemoryService.List` | `agents` memory_read 工具（kind=shared，`key` 留空） |

`Identity`/`ExpressionDNA`/`Heuristics`/`Turns`/`SharedMemory.Value` 等 jsonb 欄位用 `google.protobuf.Struct` 傳遞（對應 Go 的 `map[string]interface{}`）；`LongTermMemory.Metadata` 用 `map<string,string>`（跟底層 Cassandra `map<text,text>` 一致，不需要 Struct 轉換）。找不到資料時回傳 gRPC `codes.NotFound`，client 端轉譯為 `services.ErrNotFound`；Cassandra 未連上時 `CreateLongTerm`/`SearchLongTerm` 回 `codes.Unavailable`。TLS 沿用 HTTP server 的憑證（`server.tls.*`）。

---

## 依賴

- PostgreSQL（主要持久化）
- Redis（見 `config.yaml` 的 `database.redis`）
- Cassandra（見 `config.yaml` 的 `cassandra`）
- 不依賴其他微服務

**被呼叫方**：`agents/app/services/memory_grpc_client.go`（agents → agent_memory，gRPC，內部呼叫）

---

## 修正紀錄

已遷移至專案 [memory/20260703_log.md](../memory/20260703_log.md)（「五、agents 改真 gRPC 呼叫 agent_memory」「六、補齊 6 類記憶中缺的 3 類」節，含兩個潛伏 bug 修復細節）。
