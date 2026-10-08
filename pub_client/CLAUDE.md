# agent_client - 工作總結

> 最後更新：2026-07-08（+ 知識圖譜分頁 Obsidian 化：force-directed graphWidget、Sync Vault 下載、搜尋關鍵節點）
> **注意**：本檔多數內容寫於 REST 直連時代（dev_pub_0.9 之前）；現行架構（單一 gateway gRPC + API Key + DuckDB 加密儲存）以 [README.md](README.md) 與根 CLAUDE.md 的 dev_pub_0.9 節為準。

## 2026-07-08：知識圖譜分頁 Obsidian 化（三件事）

1. **force-directed 圖**（`app/views/graph_widget.go`）：自訂 Fyne widget（Coulomb 斥力 + Hooke
   彈簧 + 向心力 30fps；拖曳節點/空白 pan/滾輪 zoom 以游標為錨；category hash 染色、中心
   `theme.PrimaryColor()`）取代舊放射狀 `graphCanvas`。控制列：Fit / Reset / Repel / Spring / Dist。
2. **Sync Vault**（`app/services/vault_sync.go` + Graph 分頁 Sync Vault 鈕）：`VaultManifest`
   （sha256 差異比對）→ `FetchVaultFiles`（server-streaming，第一個 receive-stream client 方法）
   單向 pull 到 `Config.VaultDir`（設定頁「Vault Folder」資料夾選擇器；空 = 停用）。本地多出的
   檔不動；server 路徑逃逸明確拒絕（`safeVaultLocalPath`）。下載完可直接用 Obsidian 開。
   測試：`vault_sync_test.go`（全新/增量/單檔變更/本地檔保留/逃逸拒絕）。
3. **搜尋關鍵節點**（`graph_view.go` 的 `keyNodeIndex`）：搜尋完自動選取 rank =
   相關性×(1+ln(1+degree)) 最高的實體（degree 為後端新回填的全圖關係數），圖譜直接跳到其鄰域；
   `graphWidget` 節點半徑也改用全圖 degree（與 Obsidian 的引用數語意一致）。

## 專案定位

Windows 桌面 client（Go + Fyne），是平台的**使用者入口**（架構圖中的 Entry Point）。
連線 `agents` 服務（:18080，自簽 TLS HTTP/2），提供五件事：

1. **同步對話** — 走 `POST /api/chat`，含「client 端工具回合制」（agent 決定呼叫、**本機執行**、結果回注續推理）
2. **文件攝取** — `POST /api/ingest/upload`（multipart：PDF / 圖片 / 文字）+ `GET /api/ingest/jobs/:id` 輪詢
3. **本地工具管理** — 使用者登錄可執行程式（allowlist），隨對話以 `client_tools` manifest 送給 agent
4. **內建 Coding Agent** — 工作目錄已設 **且** 對話頁切到 **Code 模式**才啟用（雙重開關，預設 Normal 模式關閉）：agent 可讀檔/列目錄/搜尋/編輯/寫檔，**寫入前強制彈 diff 給使用者審核**（比照 Claude Code 的 Read/Grep/Edit/Write 語意）；Normal 模式下 agent 完全看不到這些工具，只能用登錄工具或指令
5. **知識圖譜瀏覽/查詢**（2026-07-04）— 直連 `agent_graph` 服務（**不經過** `agents`），跟前四項是獨立的第二個後端連線：關鍵字搜尋 / 語意（向量）搜尋實體，點選結果查看區域鄰居與關係

- Module：`agent_client`（獨立 go.mod，避免 Fyne/CGO 依賴污染後端各服務）
- 依賴：`fyne.io/fyne/v2 v2.4.5` + `github.com/maoxiaoyue/hypgo v0.8.11` + `github.com/pmezard/go-difflib v1.0.0`（產 unified diff；原是 transitive 依賴，升級為 direct）
- Go：1.26+；建置需 CGO（Fyne GL driver），Windows 用 MinGW-w64 gcc

## 歷史沿革

已遷移至專案 `memory/` 工作日誌（依日期分檔，逐步決策過程與原因見各檔）：[memory/20260605_log.md](../memory/20260605_log.md)、[memory/20260701_log.md](../memory/20260701_log.md)、[memory/20260702_log.md](../memory/20260702_log.md)、[memory/20260704_log.md](../memory/20260704_log.md)。

## 目錄結構與職責

```
main.go                      # views.RegisterSchemas() → views.Run()
app/models/
  config.go                  # Config{BaseURL,AgentID,InsecureTLS,Tools[],WorkspaceRoot,GraphBaseURL} + %APPDATA%\agent_client\config.json 持久化
  chat.go                    # ChatRequest/Response（client_tools / continuation_id / tool_results）、Job DTO
  graph.go                   # GraphEntity/GraphRelation/GraphContext + 3 組 Request DTO（對應 agent_graph 的 REST 契約）
app/services/                # 業務邏輯層；views 不直接做 I/O
  api_service.go             # APIClient：Chat() / Upload()(multipart) / GetJob()；InsecureSkipVerify 支援自簽
  graph_client.go             # GraphClient：SearchEntities/GetLocalContext/HybridSearch，直連 agent_graph（獨立於 APIClient 之外的第二個後端連線）
  chat_service.go            # ChatEngine.Send()：對話回合制引擎（見下）；SessionID 延續
  tool_service.go            # ToolManifest / FindTool / ExecuteTool + substituteArgs（argv 樣板）+ launchBackground（視窗程式）
  code_service.go            # 內建 Coding Agent：BuiltinCodeTools / IsCodeTool / ExecuteCodeTool
                              #   （list/search/read/edit/write，diff 審核，路徑逃逸防護）
  errors.go                  # typed error catalog（hypgo/pkg/errors，category="client"）
app/views/                   # Fyne UI
  main_view.go               # State{App,Win,Cfg,Client,GraphClient,Log} + hypgo logger + AppTabs 五分頁
  chat_view.go               # 對話頁：Normal/Code 模式切換按鈕、送訊息 goroutine、RequireConfirm 確認框、diffApprove 閉包、狀態列、新對話
  upload_view.go             # 上傳頁：FileOpen 多選 + tags → Upload → pollJob（1.5s 間隔，3min 上限）
  tools_view.go              # 工具頁：List + Form CRUD（名稱/描述/命令/argv 樣板/JSON Schema/確認/類型(視窗)/逾時）
  graph_view.go              # 知識圖譜頁：搜尋框（關鍵字/語意雙按鈕）+ 結果 List + 詳情面板（中心實體/鄰居/關係）；GraphClient 為 nil 時顯示停用提示
  settings_view.go           # 設定頁：BaseURL / AgentID / GraphBaseURL / InsecureTLS / WorkspaceRoot(資料夾選擇器) → RebuildClient + Save
  diff_dialog.go             # showDiffApproval()：dialog.NewCustomConfirm 包唯讀等寬多行 diff 檢視
  schema.go                  # schema.RegisterDesktop() ×4（Protocol "desktop"）
app/config/config.yaml       # 只用 logger 區段（桌面 app 不開 HTTP server）
tools/genctx/main.go         # 產生 .hyp/context.yaml（manifest.AutoSync；.hyp/llm.yaml 可啟用 LLM 增強）
.hyp/                        # hypgo 工具鏈：config / llm / context / comment.yaml
```

## 核心流程：對話回合制（client 端工具）

```
chatTab（chat_view.go）維護 codeMode bool（預設 false = Normal 模式）：
  workspaceRoot := ""
  if codeMode { workspaceRoot = cfg.WorkspaceRoot }   // Normal 模式一律傳空字串
  eng := NewChatEngine(client, cfg.Tools, workspaceRoot, confirm, statusFn, diffApprove)

ChatEngine.Send(userText)
  │ client_tools = ToolManifest(cfg.Tools) ++ BuiltinCodeTools(workspaceRoot)  // Normal 模式時後半截為空
  │ req = {agent_id, session_id, messages:[user], client_tools}
  ▼
loop (≤12 回合):
  APIClient.Chat(req) → ChatResponse
  ├─ status == "final"       → 回傳 Content（結束）
  └─ status == "tool_calls"  → runTools(calls)：每個 call 先判斷 IsCodeTool(name)
        是 → ExecuteCodeTool(ctx, workspaceRoot, call, diffApprove)（見下方 Coding Agent 專節）
        否 → 沿用使用者登錄工具流程：
          FindTool 未登錄        → ToolResult{IsError, E3003}
          RequireConfirm 被拒    → ToolResult{IsError, E3004}
          否則 ExecuteTool：
            - spec.Background == false（一般命令列工具）：exec.CommandContext（argv 樣板
              {{param}} 代入、不經 shell、timeout 預設 30s、stdout+stderr 合併、
              錯誤包進 result 不 panic）
            - spec.Background == true（視窗程式）：launchBackground()，cmd.Start() 後
              立即回傳「已啟動 PID N」，不等待結束、不套 timeout（否則 GUI 程式
              會被判定逾時砍掉）
     req = {agent_id, session_id, continuation_id, tool_results}   # resume（不重送 client_tools）
```

對應後端：`agents/app/controllers/agent_chat.go`（`ChatSync` + 記憶體 continuation store，TTL 30min）。
**後端契約**（`agents/app/models/chat.go`）改動時，`app/models/chat.go` 必須同步。
**已確認約束**：`client_tools` 只在每輪對話的第一次請求送出，resume 不會重新解析——manifest 合併邏輯必須放在 `Send()` 組首次 request 的地方（已如此實作），不能只放在 resume 分支。

## Typed Error Catalog（errors.go）

| Code | 意義 |
|------|------|
| E3001 | 尚未設定連線（views 在 Client==nil 時彈） |
| E3002 | 對話回合超過上限（12） |
| E3003 | client 沒有登錄此工具 |
| E3004 | 使用者拒絕執行此工具 |
| E3005 | 上傳未選檔案 |

網路 / 解碼等不可預期 I/O 錯誤仍用 `fmt.Errorf` 包裝。

## 安全設計（本地工具執行）

- **allowlist**：只執行使用者在工具頁明確登錄的項目
- **無 shell 插值**：參數以 argv 陣列 `{{param}}` 替換（`substituteArgs`），未解析佔位會被剝除
- **RequireConfirm**：勾選後每次執行前 `dialog.ShowConfirm`
- **硬性 timeout**：每工具可設，預設 30s（`context.WithTimeout`）；`Background=true` 的視窗程式不適用（見下）

## 視窗（GUI）工具（`ToolSpec.Background`）

一般工具用 `exec.CommandContext` **等待程式結束**才拿到 stdout/stderr。GUI 程式（記事本、小畫家、瀏覽器…）不會自己退出，等到 timeout 就被強制砍掉、回傳失敗——所以它們無法用一般工具模式開啟。

登錄工具頁勾選「視窗程式（只負責開啟，不等待關閉）」→ `ToolSpec.Background=true` → `ExecuteTool` 走 `launchBackground()`：`cmd.Start()` 啟動、獨立 goroutine `cmd.Wait()` 回收子行程避免 zombie、**不綁 ctx 也不套 timeout**，立即回傳 `已啟動 <command>（PID N，背景執行中，不等待結束）`。

測試：`tool_service_test.go` 的 `TestExecuteTool_Background_ReturnsImmediately`（用 `timeout /t 5` 模擬長駐程式，斷言 2s 內就拿到回傳）、`TestExecuteTool_Background_LaunchError`（命令不存在時的錯誤路徑）。

## 內建 Coding Agent（code_service.go）

使用者問「agent_client 可以幫我編輯、修改程式嗎？」，選擇要「像 Claude Code 一樣的完整改碼流程」。與工具頁的使用者登錄工具不同，這組是 **client 內建**、比照 Claude Code 的 Read/Grep/Edit/Write 語意：

| 工具 | 語意 | 審核 |
|---|---|:---:|
| `code_list_files` | 列出相對目錄下的檔案/子目錄（非遞迴，跳過 `.git/.hyp/node_modules/vendor/bin/obj/.vs/.idea`） | 否 |
| `code_search` | 遞迴文字搜尋（不分大小寫子字串），略過 >2MB 檔案，上限 50 筆 | 否 |
| `code_read_file` | 讀檔，`offset`/`limit` 行數窗口，上限 `maxReadBytes=8000` bytes 並註明截斷 | 否 |
| `code_edit_file` | `old_string`→`new_string` 精確替換；找不到或不唯一（未帶 `replace_all`）明確回錯 | **是** |
| `code_write_file` | 整檔覆寫/新建（`os.MkdirAll` 建父目錄） | **是** |

**啟用開關（雙重）**：`BuiltinCodeTools(workspaceRoot)` 在 `workspaceRoot==""` 時回傳 `nil`——manifest 完全不含這些工具。
1. 設定頁必須先選一個工作目錄
2. 對話頁的 **Normal／Code 模式切換按鈕**（`chat_view.go` 的 `codeMode` 狀態，預設 Normal）必須切到 Code

兩者缺一都會讓 `workspaceRoot` 在傳給 `NewChatEngine` 時被清成空字串（Normal 模式下 `chat_view.go` 直接寫死傳 `""`，不管設定頁實際有沒有值）——這是使用者明確要求的行為：「Normal 模式不會進入編輯狀態，只會使用工具或執行指令」，避免只設過一次工作目錄就永遠處於「可編輯」狀態。按鈕本身：`widget.Button`，`Importance` 依模式切換（`MediumImportance`/`WarningImportance`）做視覺區分；點切到 Code 但工作目錄未設時彈 `dialog.ShowInformation` 提示去設定頁。

**路徑安全**（`resolveInWorkspace`）：`filepath.Join(absRoot, rel)` 後用 `filepath.Rel` 檢查結果是否仍在 root 內；真的逃逸（如 `../outside.txt`）**明確回錯**給 LLM，而非靜默鉗制到別的路徑——這樣 LLM 知道自己給錯路徑、可以自己修正，且不會誤以為寫到了它以為的位置。

**diff 審核閘門**（`applyWithDiff`，`code_edit_file`/`code_write_file` 共用）：
1. 讀出舊內容（新檔視為空字串）
2. `difflib.UnifiedDiff{A,B,Context:3}` + `difflib.GetUnifiedDiffString` 產生 unified diff
3. 呼叫 `DiffApproveFunc(path, diffText) bool`——client 端由 `chat_view.go` 的閉包接到 `views/diff_dialog.go` 的 `showDiffApproval()`：`dialog.NewCustomConfirm` 包一個唯讀等寬 `widget.Entry` 顯示 diff，「套用」/「拒絕」兩鈕，同步阻塞（channel）等使用者回應——與既有 `RequireConfirm` 確認框走同一種模式（在 `Send()` 的背景 goroutine 呼叫 `dialog.Show*` 是已驗證可行的作法）
4. 核准才 `os.MkdirAll` + `os.WriteFile`；拒絕回傳 `IsError=true, Content="使用者拒絕套用此修改"`，LLM 可據此調整方案

**二進位/非文字檔防護**：所有讀寫路徑都先 `utf8.Valid(data)` 檢查，非法 UTF-8（多半是二進位檔）一律明確拒絕，不會把垃圾位元組塞進 JSON 對話內容。

**已知限制**（誠實告知使用者，寫在 README）：
- 本地小模型（`gemma4:e4b`，8B）在多檔案/複雜重構上遠不如雲端大模型可靠，適合單檔小改動
- 後端 `config.yaml` 的 `tools.max_iterations: 8` 是每輪對話的伺服器端工具迴圈上限，複雜任務可能不夠跑完；此為後端調參，不在 agent_client 範圍內
- 只支援文字檔讀寫；不支援真正的多檔案 diff/refactor 規劃（一次只處理一個工具呼叫）

測試：`code_service_test.go`（18 cases，`t.TempDir()` 隔離）——路徑逃逸防護、manifest 開關、list/search/read（含截斷與二進位拒絕）、edit（找不到/不唯一/核准/拒絕/replace_all）、write（新建含父目錄/拒絕不寫入）、diff 格式基本檢查。

## 建置 / 測試 / 工具

```powershell
go build -o agent_client.exe .   # 需 CGO + gcc（MinGW-w64；本機已驗證 go1.26 + gcc15）
go test ./...                    # tool_service_test.go + code_service_test.go
go run ./tools/genctx            # 重新產生 .hyp/context.yaml
```

## E2E 驗證紀錄（2026-06-05）

已遷移至 [memory/20260605_log.md](../memory/20260605_log.md)（「E2E 驗證（2026-06-05，DB 在線）」節）。

## 已知限制 / 注意事項

- 工具回合制只在後端 LLM 為 `ToolCallerLLM`（目前 Ollama provider）時生效；其他 provider 退回純對話
- 圖片攝取需後端 `ingest.vision.enabled=true`；掃描型（純圖片）PDF 不 OCR
- Fyne GUI 無法在無頭環境驗證；改動 UI 後需在實機開視窗確認
- `chat_view` 的 sessionID 保存在分頁閉包中；「新對話」按鈕清空之
- views 內 widget 更新從 goroutine 直接呼叫（Fyne 的 `SetText` 等是 thread-safe 的 v2 API）；若升級 Fyne 主版本需重新確認
- 內建 Coding Agent 未設工作目錄時完全關閉；已設定時任何在該目錄內的文字檔都可被 LLM 讀到/改到（讀/列/搜尋不需審核）——工作目錄請選使用者信任的範圍（例如單一專案根目錄），不要選整個磁碟根
- 知識圖譜分頁（2026-07-04）：`GraphClient` 的 HTTP 往返邏輯已用 `httptest` 驗證（`graph_client_test.go`），但 Fyne UI 本身無法在無頭環境驗證（同上一條的既有限制）——實機開視窗前無法保證版面/互動細節完全符合預期；`HybridSearch`（語意搜尋）依賴 `agent_graph` 端 pgvector 是否啟用，未啟用時會在狀態列顯示錯誤訊息而非靜默失敗

## 關鍵檔案速查

| 領域 | 檔案 |
|------|------|
| 進入點 | [main.go](main.go) |
| 設定 + 持久化 | [app/models/config.go](app/models/config.go) |
| 後端 DTO 契約 | [app/models/chat.go](app/models/chat.go) ←→ `agents/app/models/chat.go` |
| HTTP client（agents） | [app/services/api_service.go](app/services/api_service.go) |
| HTTP client（agent_graph） | [app/services/graph_client.go](app/services/graph_client.go) |
| 回合制引擎 | [app/services/chat_service.go](app/services/chat_service.go) |
| 本地工具執行 | [app/services/tool_service.go](app/services/tool_service.go) |
| 內建 Coding Agent | [app/services/code_service.go](app/services/code_service.go) |
| 錯誤目錄 | [app/services/errors.go](app/services/errors.go) |
| UI 組裝 | [app/views/main_view.go](app/views/main_view.go) |
| 知識圖譜頁 | [app/views/graph_view.go](app/views/graph_view.go) |
| diff 審核彈窗 | [app/views/diff_dialog.go](app/views/diff_dialog.go) |
| Desktop schema | [app/views/schema.go](app/views/schema.go) |
| Manifest 產生器 | [tools/genctx/main.go](tools/genctx/main.go) |
