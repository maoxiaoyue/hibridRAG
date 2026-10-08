# graphic_input — 知識圖譜人工輸入服務（設計文件）

> 狀態：**已實作**（2026-07-06 設計 + 同日實作完成；單元測試綠、真機端到端待驗——需要可達的 agent_graph/PG/Ollama/vault）
> 監聽埠：`:8505` HTTP（HTTP/2+TLS）+ `:8513` gRPC（trusted_ips / CN 允許清單）
> 部署邊界：**內網（dev_090）專用**——dev_pub_0.9 外網版不部署此服務，gateway 的 `ClientGateway` 也**不會**新增對應轉發 RPC
> 實作時機：dev_pub_0.9 測試機部署驗證完成後、回到 dev_090 分支進行

---

## 動機與定位

知識圖譜（agent_graph）目前只有兩條寫入路徑：

1. **Obsidian vault 同步**（自動）：WikiLink 確定性關係直接 MERGE；LLM 抽取的低信心關係進 `proposed_relations` 待審
2. **proposed_relations 人工審核**（半自動）：client 的 Pending Relations 分頁核准後才 MERGE

`graphic_input` 補上人工輸入面，涵蓋兩種形態：

- **結構化直接輸入**：手動建實體/關係（表單/批次），不經筆記也不經待審（人工輸入本身就是審核）
- **文件輸入（雙路徑）**：內網 client 送來的 markdown / text / image / PDF，由本服務抽成文字後，依 client 指定劃分成 **Obsidian 版本**（落地 vault 筆記，走既有同步管線）與 **PG 版本**（直接進 agent_graph 的 PG：LLM 抽取 + entity_embeddings，不落 vault 檔案）

直連 `agent_graph`（不經過 `agents`——跟推理對話無關；不經過 gateway——內網專用）。

**為什麼是獨立服務而不是 client 直連 agent_graph**：抽文字（OCR/PDF）、驗證/重複偵測、雙路徑路由、批次匯入、輸入紀錄這些邏輯要落在 server 端，未來任何呼叫端（client 分頁、CLI、批次腳本）共用同一套護欄。

---

## scaffold 現況（hyp CLI 產生；**下表所列問題已於實作時全部處理**）

| 項目 | 現況 | 實作時要做的事 |
|------|------|----------------|
| `main.go` | 寫死 `:9090`（**與 gateway 撞 port**）、無 HTTP server | 改 `:8513` gRPC + 加 `:8505` HTTP（HypGo server） |
| `app/proto/graphic_inputpb/graphic_input.proto` | 通用 CRUD placeholder | 全部換成下方「gRPC 介面設計」 |
| pb 生成 | **未生成，目前整個 module 不能編譯** | protoc 生成（參照其他服務的 M-mapping 慣例） |
| `app/rpc/graphic_input_server.go` | placeholder handler | 換成實際實作 |
| config | scaffold 預設 | 加 database（log 用 PG）/ graph（agent_graph 位址）/ vault（Obsidian 路徑）/ ollama（vision OCR）/ trusted_ips |

---

## 文件雙路徑輸入（核心設計）

### 處理管線

```
內網 client 上傳（markdown / text / image / PDF + targets + tags）
  ▼
graphic_input
  1. 抽文字層（依型別，見矩陣）
  2. 路由層（依 client 指定的 targets，可單選或雙選）：
     ├─ obsidian → 包 frontmatter 寫成 vault 筆記檔
     │             → agent_graph 的 sync worker（fsnotify）自動接手
     │               （WikiLink 確定性 MERGE + LLM 抽取 confidence gate）
     └─ pg       → 呼叫 agent_graph 新增的 IngestText
                   → LLM 抽取（同一套 confidence gate）+ entity_embeddings
                     直寫 PG（AGE + pgvector），不留 vault 檔案
  3. 全部行為記 graphic_input_log
```

### 檔案型別 × 處理矩陣

| 型別 | 抽文字 | Obsidian 路徑 | PG 路徑 |
|------|--------|---------------|---------|
| `.md` | 不需要 | 原文正規化 frontmatter 後寫入 vault（WikiLink 得以保留，同步時走確定性關係） | 原文 → `IngestText` |
| `.txt` | 不需要 | 包成 .md（title=檔名） | 原文 → `IngestText` |
| 圖片（png/jpg/webp…） | **Ollama vision OCR**（比照 agents 的 `VisionExtractor`；graphic_input 直接打 Ollama，同 agent_graph 慣例） | OCR 文字包成 .md；**原圖存 vault `attachments/` 並在筆記內 embed**（保留人工回看原圖的能力） | OCR 文字 → `IngestText` |
| PDF | `ledongthuc/pdf` 抽純文字（**掃描型 PDF 不 OCR**，同 agents 既有限制，明確回錯） | 抽出文字包成 .md（frontmatter 註記來源檔名） | 文字 → `IngestText` |

### 兩條路徑的語意差異（給使用者的選擇依據）

| | Obsidian 版本 | PG 版本 |
|--|---------------|---------|
| 產物 | vault 內留下永久筆記（人可讀可編輯，之後改動會再同步） | 只有圖譜資料，不留檔案 |
| 進圖時機 | 非同步（fsnotify 觸發 sync worker） | 立即（同步呼叫回報結果） |
| WikiLink | 支援（.md 原文保留） | 不適用（純文字抽取） |
| 適合 | 要保留原始知識文件、之後會人工維護的內容 | 一次性匯入、不需要筆記形態的資料 |

**`targets` 同時選兩個（both）的注意事項**：同一份內容會被 LLM 抽取兩次（PG 路徑立即一次、vault 同步再一次）——MERGE 冪等所以圖譜節點/邊不會重複，但抽取成本×2 且 `proposed_relations` 可能出現重複待審條目。第一版 UI 預設單選，both 列為進階選項並在介面註明此成本；去重優化（例如 PG 路徑完成後把內容 hash 預填進 `graph_sync_state`）留待有實際需求再做。

### Obsidian 寫入細節

- vault 路徑：與 agent_graph 的 `app/config/md.yaml` `vault.path` 指向**同一個目錄**（graphic_input 需寫入權限——同機部署或共享磁碟；config 各自設定、部署時保持一致）
- 落點：`vault/inbox/{YYYYMMDD}-{slug(title)}.md`——人工輸入集中在 `inbox/` 子目錄，與原生筆記區隔，方便人工整理歸檔
- frontmatter：`source: graphic_input`、`actor`、`created`、`original_file`（原始上傳檔名）
- **注意**：agent_graph 的 fsnotify **不支援遞迴**（要手動 walk 加子目錄，見 agent_graph/CLAUDE.md）——`inbox/` 若是新建目錄，要確認 sync worker 啟動時已 walk 到它，或 graphic_input 首次建目錄後提示重啟 agent_graph／實作 agent_graph 對新子目錄的動態 watch（實作時二擇一）
- 寫入即觸發同步，graphic_input 不需要另行通知 agent_graph

### PG 直寫細節（agent_graph 端配套）

- agent_graph 新增 `IngestText`（gRPC + REST `POST /api/graph/ingest_text`）：`text + source + actor` → **重用 sync worker 既有的 LLM 抽取邏輯**（高信心自動 MERGE / 低信心進 `proposed_relations`，同一套 confidence gate）+ 實體 embedding 寫 `entity_embeddings`
- **抽取邏輯只存在 agent_graph 一份**——graphic_input 不自己打 LLM 做實體抽取（只打 Ollama vision 做 OCR），避免兩份抽取程式碼漂移

---

## API 設計

### REST（`:8505`，供 dev_090 client「Graph Input」分頁）

| Method | Path | 說明 |
|--------|------|------|
| `GET` | `/health` | 健康檢查（含 agent_graph 連通、vault 可寫、Ollama vision 可用狀態） |
| `POST` | `/api/input/upload` | **文件上傳**（multipart：`files[]` + `targets`=obsidian\|pg\|both + `tags`）；逐檔回報（抽文字結果、路由結果、vault 路徑或抽取統計） |
| `POST` | `/api/input/entity` | 結構化：建立/更新單一實體（含重複偵測回饋） |
| `POST` | `/api/input/relation` | 結構化：建立單一關係 |
| `POST` | `/api/input/check` | 重複偵測預查：回相似實體候選（呼叫 agent_graph `SearchEntities`） |
| `POST` | `/api/input/batch` | 結構化批次匯入（CSV/JSON），逐筆驗證回報 |
| `GET` | `/api/input/log?limit=` | 輸入歷史 |

### gRPC（`:8513`，`graphic_input.v1.GraphicInputService`）

```proto
service GraphicInputService {
  // 文件雙路徑輸入（client-streaming：首則 meta（targets/tags），後續 file chunk，
  // 形狀比照 agents 的 UploadIngest）
  rpc UploadDocuments(stream DocChunk) returns (UploadResult);

  // 結構化直接輸入
  rpc UpsertEntity(UpsertEntityReq) returns (UpsertEntityResp);      // 含 similar_candidates 回饋
  rpc UpsertRelation(UpsertRelationReq) returns (UpsertRelationResp);
  rpc CheckDuplicates(CheckDuplicatesReq) returns (CheckDuplicatesResp);
  rpc BatchImport(stream BatchRow) returns (BatchResult);

  rpc ListInputLog(ListInputLogReq) returns (ListInputLogResp);
}
```

（`UpsertEntityReq` 帶 `force bool`——重複偵測有候選時預設拒絕寫入，`force=true` 才強制新建。`UploadResult` 逐檔回報：`file / extracted_chars / obsidian_path / pg_entities / pg_relations / pg_proposed / error`。）

---

## agent_graph 端配套（前置工作，先做這些）

| 介面 | 說明 |
|------|------|
| REST `POST /api/graph/entity` + gRPC `UpsertEntity` | `AGEClient.UpsertEntity` + **同步寫 entity_embeddings**（沿用 sync worker 的 embed 邏輯）——否則手動輸入的實體 `hybrid_search` 永遠找不到 |
| REST `POST /api/graph/relation` + gRPC `UpsertRelation` | `AGEClient.UpsertRelation` 直接 MERGE（自動補 stub 端點） |
| REST `POST /api/graph/ingest_text` + gRPC `IngestText` | **新增**：純文字進 → 重用 sync worker 的 LLM 抽取（confidence gate 護欄不變）+ entity embeddings；回抽取統計（entities/relations/proposed 數量） |

**邊界不變**：以上寫入端點**不加進** `agents` 的 `graph_query` 工具（不給 LLM 直接改圖譜的能力）；gateway `ClientGateway` 也不轉發（外網 client 永遠沒有直接寫圖譜的能力）。

---

## 服務職責

1. **抽文字**：PDF（ledongthuc/pdf）、圖片（Ollama vision OCR）；md/txt 原文直用
2. **雙路徑路由**：依 `targets` 分派 Obsidian（vault 筆記）與 PG（IngestText 直寫）
3. **輸入驗證與正規化**：實體名去空白/大小寫策略、關係型別白名單（沿用 sync worker 的型別集合）
4. **重複偵測**（結構化輸入）：寫入前先 `SearchEntities` 給合併候選，`force=true` 才強制新建
5. **批次匯入**（結構化）：CSV/JSON 逐筆驗證、逐筆回報，單筆失敗不中斷
6. **輸入紀錄**：`graphic_input_log`（含文件上傳與結構化輸入），提供 undo 參考（真正的圖譜 rollback 不做——AGE 沒有好用的事務語意）
7. **認證**：內網部署 + `trusted_ips`（沿用 dev_pub_0.9 的信任 IP 機制）；`actor` 由呼叫端自報（client 帶 hostname/username）

## 資料模型（自有 PG，沿用 192.168.50.201 實例）

```sql
graphic_input_log:
  id TEXT PK, actor TEXT,
  action TEXT,        -- upload_doc | upsert_entity | upsert_relation | batch_import
  target TEXT,        -- obsidian | pg | both | -（結構化輸入）
  payload JSONB,      -- 文件：檔名/型別/抽字統計/vault 路徑；結構化：寫入內容原文
  result TEXT,        -- ok | rejected_duplicate | error:<msg>
  created_at TIMESTAMPTZ
```

## 依賴

- agent_graph（gRPC `:8512`：SearchEntities / UpsertEntity / UpsertRelation / IngestText）
- Obsidian vault 檔案系統寫入權限（與 agent_graph 的 `vault.path` 同一目錄）
- Ollama（vision OCR；直連，同 agent_graph 慣例）
- PostgreSQL（`graphic_input_log`）
- 不依賴 agents / gateway / registry

## 與 client 的關係

- dev_090 版 agent_client 加「Graph Input」分頁：文件上傳區（多選檔案 + targets 單選/both + tags）＋ 結構化表單（實體/關係 + 重複候選選擇）＋ 批次匯入 ＋ 輸入歷史列表
- 外網版（dev_pub_0.9）client **不含此分頁**（build tag 或 config 開關，實作時再定）

## 實作順序

1. agent_graph 配套：UpsertEntity / UpsertRelation / **IngestText**（含 entity_embeddings 同步與抽取邏輯重用）——前置
2. 本服務基礎：改 port → 換 proto/生成 pb → trusted_ips 認證 → graphic_input_log
3. 結構化輸入：entity/relation/check/batch
4. 文件雙路徑：抽文字（PDF/OCR）→ Obsidian 落地（inbox/ + frontmatter + attachments）→ PG 直寫（IngestText）
5. dev_090 client「Graph Input」分頁


---

## 實作紀錄（2026-07-06）

| 檔案 | 內容 |
|------|------|
| `main.go`（重寫） | HTTP :8505（hypgo server）+ gRPC :8513（trusted_ips/CN 允許清單，同 dev_pub_0.9 mtls 機制）；PG（graphic_input_log AutoMigrate）與 agent_graph 皆硬依賴（連不上直接 exit） |
| `proto/graphic_input.proto`（取代 placeholder） | `GraphicInputService`：UploadDocuments（client-streaming）/ UpsertEntity（force + candidates 回饋）/ UpsertRelation / CheckDuplicates / BatchImport（client-streaming）/ ListInputLog |
| `config/config.{go,yaml}` | server/grpc/graph（mTLS client 欄位）/vault/vision/database/logger |
| `app/services/graph_client.go` | agent_graph gRPC client（SearchEntities/UpsertEntity/UpsertRelation/IngestText） |
| `app/services/extract.go` | md/txt 原文（UTF-8 驗證）、PDF（ledongthuc/pdf bytes 版；掃描型明確回錯）、圖片（Ollama vision OCR，移植 agents VisionExtractor） |
| `app/services/vault.go` | `inbox/{YYYYMMDD}-{slug}.md` 落地（撞名加序號不覆蓋）、frontmatter（source/actor/created/original_file/tags）、圖片原檔進 `attachments/` + `![[...]]` embed、`EnsureDirs` 啟動預建目錄（收斂 fsnotify 新目錄盲區） |
| `app/services/input_service.go` | 核心：重複偵測（排除完全同名）、force 語意、CSV/rows 批次（FieldsPerRecord=-1 允許變欄數）、雙路徑路由（單檔失敗不中斷）、graphic_input_log best-effort 寫入 |
| `app/controllers/{rest,grpc}.go` | 7 REST 端點 + 6 gRPC 方法，全部委派 InputService |
| 測試 | `input_service_test.go`：Slug/ClassifyFile/ExtractText（含 UTF-8 拒絕與 vision 停用）/VaultWriter（寫入、frontmatter、附件、撞名、停用）/ParseBatch CSV |

**agent_graph 端配套**（同日實作）：facade 加 `Chat`+`ConfidenceThreshold` 欄位與 `UpsertEntityDirect`（含 `syncEmbedding` best-effort，回 `EmbeddingSynced`）/`UpsertRelationDirect`/`IngestText`（重用 `ChatClient.ExtractEntitiesRelations`，confidence gate 與 sync worker 同語意）；REST `POST /api/graph/{entity,relation,ingest_text}` + gRPC 同名 3 RPC。gencert 服務清單加入 graphic_input。

**client 端**（同日實作）：`Config.GraphInputBaseURL`（空 = 分頁完全不建，外網版預設）+ `graphinput_client.go`（REST，multipart 串流上傳，actor=hostname/username）+ 「Graph Input」分頁（Documents/Structured/History 三個子分頁）。設定頁加 Graph Input URL 欄位（重啟生效）。

**未驗證**（環境限制）：真機端到端需要可達的 agent_graph（`192.168.50.104` PG/Ollama）與實際 vault 目錄；LLM 抽取品質、OCR 品質、vault 寫入觸發 agent_graph 同步的完整鏈路都要部署後驗證。
