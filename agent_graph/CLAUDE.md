# agent_graph — 知識圖譜服務（A4 Knowledge Agent，Graph RAG）

> 監聽埠：`:8504`（HTTP/2, TLS）+ `:8512`（gRPC, TLS）
> 設定檔：`config/config.yaml`（server/PG/gRPC/圖名稱）+ `app/config/ollama.yaml`（固定 LLM/embedding 模型）+ `app/config/md.yaml`（Obsidian vault 路徑）
> 建立日期：2026-07-04（HypGo v0.8.11，比照 agent_critic 慣例）

---

## 功能概述

`agent_graph` 是 [docs/Agentic、Graphic RAG Hybrid.md](../docs/Agentic、Graphic%20RAG%20Hybrid.md) 評估後落地的 **A4 Knowledge Agent**：
把 Obsidian vault 的 markdown 筆記同步進知識圖譜，並提供查詢/寫入操作供 `agents` 服務或其他呼叫端使用。

**儲存（2026-07-08 起：embedded Kùzu 唯一後端；AGE/pgvector 路徑已移除）**：
- **Kùzu**（embedded 圖資料庫，檔案 `data/graph.kz`，config `graph.kuzu_path`）——`Entity` 節點
  （name/category/description/source_file）+ `RELATES_TO` 邊（`type` 屬性），**含向量**
  （`EntityEmbedding` node table + HNSW index，1024 維 BGE-M3，供 `hybrid_search`）。
- **PostgreSQL**（`192.168.50.104:5432`）只剩兩張一般表：`proposed_relations`（待審關係）+
  `graph_sync_state`（vault 同步簿記）。PG 仍為必要依賴，但**圖資料不在 PG**。

> 歷史：原設計 AGE-on-Postgres + pgvector；2026-07-08 查證該 PG 從未裝過 AGE/pgvector（圖資料
> 從未落在 PG），故整條 AGE 路徑（age_client/agtype/vector_store + config `graph.backend`/`graph.name`）
> 移除。`GraphBackend`/`VectorBackend` 介面保留（facade/sync worker 只認契約），要復活其他後端
> 只需新實作 + main.go 一行接線。Kùzu 是單行程 embedded DB——**同時只能一個 process 開檔**。

**Obsidian 同步**（`internal/sync/`）是服務內部的 background worker，不是獨立微服務——啟動時全量掃描一次 vault，之後（`md.yaml` 的 `vault.watch: true`）用 fsnotify 持續監聽變動：
- WikiLink（`[[Page]]`）→ 確定性、高信心，直接 `MERGE` 進圖譜（`type=links_to`）
- LLM 抽取的關係（直接打 Ollama，見下）→ `confidence >= graph.confidence_threshold`（預設 0.75）才自動 MERGE，否則寫入 `proposed_relations` 表待人工審核——對應設計文件「主動式知識整理」的高信心自動/低信心待審護欄

**Vault 鏡像匯出**（`app/services/vault_export.go`，2026-07-08）——上面的反方向：**圖譜寫入 → vault .md**。每個 Entity 對應 `<vault>/<export_dir>/<sanitized-name>.md`（frontmatter aliases/category/`tags: kb/<cat>`/source + 描述 + 出向關係 `[[wikilink]]`）；render 確定性（排序、無時間戳），內容沒變不落盤。觸發：facade 四個寫入點 + sync worker 寫完圖譜後 `Notify(實體...)`，exporter 背景迴圈 debounce 2s 批次匯出；啟動時全量 reconcile（含刪除圖譜已無對應實體的殘檔）。**防迴圈**：`md.yaml` 的 `export_dir`（預設 `entities`）被 FullScan/watchLoop/addRecursive 三處排除，鏡像檔不會被再攝取。`export_notes: false` 可關閉（未設 = 啟用）。用 Obsidian 開這個 vault，graph view 的節點/邊與 Kùzu 圖譜一致。

**Vault 檔案下載**（`app/services/vault_files.go` 的 `VaultFS` + gRPC `VaultManifest`/`VaultFetch`，2026-07-08）：pub_client「Sync Vault」經 gateway 轉發呼叫——Manifest 列全 vault .md（跳 dot 目錄，含 sha256 供 client 差異比對）；`VaultFetch` 是 **repo 第一個 server-streaming RPC**（≤64KB chunk，每檔尾隨 eof chunk）。路徑安全：絕對路徑/`..` 逃逸/非 .md 明確回錯。vault path 未設 → `Unavailable`。

**Entity.degree**（proto 欄位 6，2026-07-08）：facade 查詢層統一回填全圖關係數（`degreeMap()` 由 `ListRelations` 計算，best-effort），SearchEntities/GetLocalContext/HybridSearch 都帶——client 拿來做節點大小與「關鍵節點」判斷。`GraphBackend` 介面同日加 `ListEntities`/`ListRelations`（鏡像匯出與 degree 的資料來源；ListRelations 回真實方向，GetLocalContext 的無向遍歷方向不可信）。

**知識沉澱管線**（`app/services/temp_digest.go` + `AppendTemp` gRPC，2026-07-10）：對話/上傳先落 `vault/<temp_dir>/`（預設 `temp`，被 sync 排除、不進圖譜），背景 `DigestWorker` 兩個 ticker——分類（`classify_interval_sec`，預設 60s）用 `ChatClient.ClassifyText` 把 temp 筆記歸到 `vault/<category>/`（搬出 temp → sync worker 攝取進圖譜；`WithOnFiled(worker.SyncFile)` 讓分類完立即攝取、不等 fsnotify）；每日整理（`maintain_interval_sec`，預設 86400s）把檔數 > `max_category_files`（預設 50）的分類再細分子分類。`md.yaml` 的 `temp_dir` + `digest:` 段控制；`AppendTemp` gRPC（title/content/source/prefix）寫 temp，供 agents 對話結束後 best-effort 沉澱問答。**排除機制擴成多目錄**：`VaultConfig.ExcludedDirNames()` = [export_dir, temp_dir]，worker 的 `underExcludedDir` 三處（FullScan/watchLoop/addRecursive）都排除。

**清理 CLI**（`cmd/cleanup`，2026-07-10）：Kùzu Entity 的 example.com 清理（`DETACH DELETE`，dry-run 優先，`--apply` 才刪）。**Kùzu 單行程鎖檔——跑前要停 agent_graph 服務**。PG + Cassandra 的清理在 `agent_memory/cmd/cleanup`。

**直接打 Ollama，不經過 agent_embedding**（使用者明確要求，跟 `agents/` 的 `SemanticMemory` 同一慣例）：`app/services/ollama_client.go` 的 `EmbedClient`（`/api/embed`，BGE-M3）+ `ChatClient`（`/api/chat`，`format:"json"` 結構化抽取實體/關係）。模型固定寫在 `app/config/ollama.yaml`，不像 `agents/` 的 ModelRegistry 可依 persona 動態切換——這裡的推理只是同步流程的內部步驟，不是對話。

---

## API 端點

### REST（`:8504`，HTTP/2 h2 + 自簽 TLS）

| Method | Path | 對應設計文件操作 |
|--------|------|------------------|
| `GET` | `/health` | — （PG 必要；AGE/pgvector 可選，狀態顯示於 `components.age`/`components.vector`） |
| `POST` | `/api/graph/search` | `search_entities`（name/description substring match） |
| `POST` | `/api/graph/context` | `get_local_search_context`（N-hop 鄰居 + 直接關係，1-4 hops） |
| `POST` | `/api/graph/cypher` | `execute_cypher`（內部/除錯用逃生艙口，呼叫端需自行保證查詢正確性） |
| `POST` | `/api/graph/hybrid_search` | `hybrid_vector_graph_search`（pgvector 找種子 + 逐一圖遍歷擴充） |
| `POST` | `/api/graph/propose_relation` | `propose_new_relation`（低信心關係候選，只寫 `proposed_relations`，不碰圖譜） |
| `POST` | `/api/graph/proposed/list` | 列出待審關係（status 空 = pending，`created_at ASC` 先進先審；2026-07-05 新增） |
| `POST` | `/api/graph/proposed/approve` | 核准：`UpsertRelation` MERGE 進圖譜（自動補 stub 端點）成功才標 `approved`；已審核回 409、AGE 停用回 503（2026-07-05 新增） |
| `POST` | `/api/graph/proposed/reject` | 拒絕：只標 `rejected`，不碰圖譜；已審核回 409（2026-07-05 新增） |
| `POST` | `/api/graph/entity` | 直接建立/更新實體 + **同步寫 entity_embeddings**（graphic_input 人工輸入；2026-07-06 新增） |
| `POST` | `/api/graph/relation` | 直接 MERGE 關係（自動補 stub 端點；2026-07-06 新增） |
| `POST` | `/api/graph/ingest_text` | 純文字 LLM 抽取（confidence gate 同 sync worker）+ embeddings（graphic_input「PG 版本」路徑；2026-07-06 新增） |

> 審核端點的呼叫方：`agent_client` 知識圖譜分頁的「Pending Relations」子分頁（Refresh / Approve / Reject）。gRPC 未同步增加這三個操作——審核是人工 UI 流程，不給 LLM 工具用。

### gRPC（`:8512`，`proto/graph.proto`，`agent_graph.v1.GraphService`）

跟 REST 一一對應（`SearchEntities`/`GetLocalContext`/`ExecuteCypher`/`HybridSearch`/`ProposeRelation`）；`ExecuteCypher` 的 params/rows 用 `google.protobuf.Struct` 傳遞。錯誤碼：`ErrGraphDisabled`/`ErrVectorDisabled` → `codes.Unavailable`；`ErrNotFound` → `codes.NotFound`。

---

## 依賴

- Kùzu（embedded，`data/graph.kz`——圖 + 向量的唯一存放處；單行程鎖檔）
- PostgreSQL（`192.168.50.104:5432`，必要——AutoMigrate 建 `graph_sync_state`/`proposed_relations` 兩張一般表；**不存圖資料**）
- Ollama（`app/config/ollama.yaml` 指定的 host，目前設定跟 PG 同一台 `192.168.50.104:11434`）——直連，不經過 agent_embedding
- Obsidian vault 檔案系統（`app/config/md.yaml` 的 `vault.path`）
- 不依賴其他微服務

**呼叫方**：`agents` 服務——`app/services/graph_grpc_client.go`（`GraphGRPCClient`）+ `tool_builtin.go` 的 `graph_query` Tool（`kind=search|context|hybrid_search|propose_relation`），比照 `rag_query`/`memory_read` 的「薄包裝呼叫外部服務」模式，而非走 `OutboundMsg`（見根 CLAUDE.md 對 A2↔A4 溝通差異的評估）。詳見 [agents/CLAUDE.md](../agents/CLAUDE.md) 2026-07-04 修正紀錄。**刻意不暴露 `execute_cypher`**——那留在本服務的 REST/gRPC admin 逃生艙口，不給 LLM 直接下任意 Cypher 的權限。

**agent_client 桌面 client**：「知識圖譜」分頁（`app/views/graph_view.go`）直接打這個服務的 REST API（不經 `agents`），設定頁 `Graph Base URL` 留空則該分頁顯示停用提示。

---

## Kùzu 技術細節（供之後維護者參考）

- **Schema**（`kuzu_schema.go`）：`Entity(name PK, category, description, source_file, updated_at)` node
  table + `RELATES_TO(FROM Entity TO Entity, type)` rel table + `EntityEmbedding(name PK,
  embedding FLOAT[dim], updated_at)` node table；HNSW index（`entity_embedding_idx`）由
  `EnsureKuzuVectorSchema` 建立（INSTALL/LOAD vector extension，冪等，啟動時每次跑）。
- **參數化**：查詢文字固定字串常數，動態值走 params（防注入）；hop 數同樣在 Go 端 clamp `[1,4]`
  後格式化（Cypher 不支援參數化路徑長度）。
- **單行程鎖**：Kùzu 是 embedded DB，同時只能一個 process 開 `data/graph.kz`——服務跑著時
  其他工具（測試/腳本）開同一檔會失敗。
- **Windows 執行**：go-kuzu 是 cgo 動態連結，跑測試/工具需把 `kuzu_shared.dll` 加 PATH：
  `$(go env GOMODCACHE)/github.com/kuzudb/go-kuzu@v0.11.3/lib/dynamic/windows`。
- **hybrid_search**：先查 `EntityEmbedding` HNSW（cosine）拿種子實體名稱，再對每個種子呼叫
  `GetLocalContext` 做圖遍歷擴充（與舊 AGE 版流程相同，只是向量層換成 Kùzu 內建）。
- **health**：`components.graph`/`components.vector`（原 `components.age` 已改名）。

---

## 已驗證項目 vs 未驗證項目（2026-07-04，誠實記錄）

**已驗證**（本機真實跑過）：
- `go build`/`go vet`/`go test ./...` 全過（`app/services`、`internal/sync` 兩個套件共 23 個單元測試）：agtype 解析（vertex/edge/scalar/畸形 JSON/含 `::` 的純量字串不誤切）、pgvector 文字格式化、markdown frontmatter/WikiLink 解析（含畸形 frontmatter 容錯、圖片 embed 過濾、標題/描述三層 fallback）、AGE/Vector client 停用時的 sentinel error、`ValidateGraphName`、confidence threshold 預設值
- 服務啟動流程正確：載入設定 → 連線 PG → 在無法連線時清楚回報錯誤並乾淨結束（非 hang、非 panic）
- TLS 憑證產生（`scripts/gencert`）正常

**未驗證**（環境限制，非程式碼問題）：
- **`192.168.50.104:5432`（PG）與 `192.168.50.104:11434`（Ollama）從開發沙箱連不到**（`192.168.50.201` 那組舊服務可連得到，`.104` 本機也對其他 port 有回應，但這兩個 port 目前連不上——可能是服務還沒在該主機啟動，或防火牆限制）。因此：
  - `EnsureAGESchema`/`EnsureVectorSchema` 實際對真實 Postgres 執行從未驗證過（`CREATE EXTENSION age`/`vector` 是否真的能裝上、`create_graph` 語法是否與目標 AGE 版本完全相符）
  - 任何實際 Cypher 查詢執行、`agtype` 從真實 AGE 回傳的文字格式，都只依照官方文件描述實作，未經真實 round-trip 驗證
  - Ollama `/api/embed`/`/api/chat` 呼叫（含 `format:"json"` 結構化抽取）未對真實 Ollama 服務測試過
  - Obsidian sync worker 的 fsnotify 監聽、全量掃描，都只在邏輯層（`markdown.go`）驗證，未接真實 vault 目錄跑過

**部署前建議動作**：在能連到 `192.168.50.104` 的網路環境跑一次 `go run .`，觀察 `Apache AGE ready`/`pgvector ready`/`Ollama clients ready` 三行 log 是否都出現（而非 warning），並用 `app/config/md.yaml` 指向一個小型測試 vault 驗證同步流程。

---

## 待辦 / 下一步

- [x] `agents` 服務接上 GraphService gRPC client（新 `graph_query` Tool，比照 `rag_query`/`memory_read` 模式，2026-07-04，見 [agents/CLAUDE.md](../agents/CLAUDE.md)）
- [ ] 向 agent_registry 註冊 Agent Card（skill: `graph_search`），讓 gateway 可路由（`agent_critic` 也還沒做，可一起補）
- [ ] 針對真實 AGE 實例做一次端到端驗證（見上節）
- [ ] 排程一致性檢查（孤兒節點、重複實體、`source_file` 已刪除但節點還在）——設計文件「主動式知識整理」第 3 點，這次沒做
- [ ] Obsidian 檔案刪除時的圖譜/`graph_sync_state` 清理（目前只處理 Create/Write，Remove 事件未處理，留給上面的一致性檢查任務一併做）
