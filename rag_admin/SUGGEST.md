# agent_client 優化建議清單

> 建立日期：2026-07-05
> 依據：對 agent_client 全部功能（對話 / 上傳 / 工具 / 知識圖譜 / 設定 / Code 模式）的程式碼檢視
> 狀態標記：每項完成後請打勾並註記日期

## 優先推薦（小改動、直接消除現有痛點）

- [x] **P1. 對話 Stop 按鈕 + 防重複送出**（見 §1.1、§1.2）— 2026-07-05 完成
- [x] **P2. Code 模式寫入前 `.bak` 備援**（見 §2.2）— 2026-07-05 完成（含 list/search 過濾 `.bak`）
- [x] **P3. 設定頁 Test Connection 按鈕**（見 §6.1）— 2026-07-05 完成（測「目前輸入值」，不需先儲存）

---

## 1. 對話頁（chat_view.go / chat_service.go）

### 1.1 無法中斷請求 ✅（2026-07-05 完成）
- **現況**：`eng.Send` 用 `context.Background()`，使用者只能等到回覆或 180s 逾時（`api_service.go` 的 http.Client Timeout）。
- **建議**：加「Stop」按鈕 + `context.WithCancel`；送出時把 cancel func 存進閉包，Stop 按下即中斷本輪。改動小、收益大。

### 1.2 重複送出 ✅（2026-07-05 完成）
- **現況**：「Thinking...」期間 Send 按鈕與 Enter 仍有效，會開第二個 goroutine 疊加操作同一個 transcript。
- **建議**：送出期間 disable 輸入框與 Send 按鈕（或用 in-flight bool 擋住 `send()` 開頭），回覆後恢復並 refocus 輸入框。

### 1.3 純文字轉錄 ✅（2026-07-05 完成：RichText Markdown 渲染 + Copy Reply 鈕）
- **現況**：整個對話史是單一 `widget.Label` + `strings.Builder`，LLM 回覆的 code block / 列表 / 粗體全部糊成純文字。
- **建議**：改用 `widget.RichText` + `ParseMarkdown`（graph_view.go 已在用同款 API）；可順帶加「複製最後回覆」按鈕。

### 1.4 無串流 ✅（2026-07-05 完成 client 便宜版：狀態列每秒顯示「階段 (已等待秒數)」；後端真串流仍屬 P1-4）
- **現況**：同步等 final，長回覆期間只有靜態的「Thinking...」。
- **建議**：後端串流是 TODO4 的 P1-4（Streaming/Cost），屬後端範圍；client 端可先做便宜版——狀態列顯示已等待秒數（每秒 tick），讓使用者知道沒有卡死。

### 1.5 token 用量被丟棄 ✅（2026-07-05 完成：final 後狀態列顯示 prompt/completion/total）
- **現況**：`ChatResponse.Usage`（prompt/completion/total tokens）完全沒被讀取。
- **建議**：顯示在狀態列或回覆尾端小字，成本可見性。

### 1.6 Temperature 永遠是 0 ✅（2026-07-05 完成：設定頁滑桿 0–1，存 Config，0 = server 預設）
- **現況**：`ChatRequest.Temperature` 欄位存在但 client 從沒賦值。
- **建議**：設定頁或對話頁加滑桿（0–1），存進 Config。

### 1.7 對話不落地 ✅（2026-07-05 完成：每輪覆寫 sessions/{session_id}.md，對話頁 History 鈕開啟資料夾）
- **現況**：關視窗 transcript 即消失（server 端 Redis session 只留 40 輪 / 2h TTL）。
- **建議**（選配）：把 transcript 存 `%APPDATA%\agent_client\sessions\{session_id}.md`，加「開啟歷史」入口。

## 2. Code 模式（code_service.go / chat_view.go）

### 2.1 模式狀態不一致（小 bug）✅（2026-07-05 完成：send() 偵測 root 被清空即自動退回 Normal 並提示）
- **現況**：切到 Code 後若回設定頁清空工作目錄，`codeMode` 仍為 true、按鈕仍顯示 Code mode，但實際送出時 `workspaceRoot=""` → 碼工具靜默消失。
- **建議**：`send()` 開頭檢查 `codeMode && root == ""` 就自動退回 Normal 並在狀態列提示；或設定頁儲存時通知 chat 分頁同步狀態。

### 2.2 寫入無備援（安全性最值得補的一項）✅（2026-07-05 完成，見 P2）
- **現況**：diff 核准後直接 `os.WriteFile` 覆寫，沒有 undo。
- **建議**：寫入前把舊內容留成 `.bak`（或 session 內記住舊內容，支援「還原上一次修改」）。注意 `.bak` 檔要加進 `skipDirs`/列目錄過濾，避免 LLM 又把備份檔讀回去。

### 2.3 diff 無顏色 ✅（2026-07-05 完成：RichText 染色——新增綠/刪除紅/hunk 藍/檔頭粗體，長行不折行走水平捲動）
- **現況**：diff 審核彈窗是純文字唯讀 Entry。
- **建議**：改 RichText 把 `+` 行染綠、`-` 行染紅，審核體驗差很多。

### 2.4 搜尋/讀檔限制偏緊 ✅（2026-07-05 完成：maxReadBytes 8000→24000；code_search 加 glob 參數，如 *.go）
- **現況**：`code_search` 只有不分大小寫子字串比對；`maxReadBytes=8000` 偏小，模型讀大檔要多輪。
- **建議**：`maxReadBytes` 放寬到 16–32KB；`code_search` 支援簡單 glob 過濾（如 `*.go`）。正則可暫緩（LLM 常寫壞正則，錯誤處理成本高）。

### 2.5 AGENTS.md 只在首輪載入 ✅（2026-07-05 完成：新對話首輪於轉錄註記「AGENTS.md loaded: N bytes」；載入時機維持原設計）
- **現況**：同一 session 中途修改 AGENTS.md 或換工作目錄，不會重新載入（刻意設計，避免每輪重複吃 context）。
- **建議**：維持現狀即可；若要改，可在「New Chat」時於狀態列提示「已載入 AGENTS.md（N bytes）」讓使用者知道有無生效。

## 3. 上傳頁（upload_view.go / api_service.go）

### 3.1 整檔進記憶體 ✅（2026-07-05 完成：io.Pipe 串流 multipart + 送出前先 Stat 驗證檔案存在）
- **現況**：`Upload` 用 `bytes.Buffer` 組 multipart，大 PDF 會整份載入 RAM。
- **建議**：改 `io.Pipe` + goroutine 串流寫 multipart，記憶體佔用與檔案大小脫鉤。

### 3.2 多工上傳互踩 ✅（2026-07-05 完成：上傳期間 disable 按鈕）
- **現況**：連按兩次上傳會有兩個 `pollJob` goroutine 交錯覆寫同一個 status label。
- **建議**：加 in-flight 旗標（上傳期間 disable 按鈕），或每個 job 一行獨立狀態列表。

### 3.3 完成後無驗證入口 ✅（2026-07-05 完成：Verify 鈕直連 agent_embedding POST /api/search（新 EmbeddingBaseURL 設定，留空停用），用檔名+tags 查命中數）
- **現況**：job completed 後流程就斷了，無法確認檢索得到。
- **建議**：完成時顯示「Verify」按鈕，用檔名/tag 打一次 search 顯示命中數。

## 4. 工具頁（tools_view.go / tool_service.go）

### 4.1 名稱重複不檢查 ✅（2026-07-05 完成：Save 時檢查重名（排除自己）即報錯）
- **現況**：儲存時不擋重名，`FindTool` 只取第一個命中，第二個同名工具永遠不會被執行。
- **建議**：`collectTool` 驗證階段檢查與既有工具（排除自己）重名即報錯。

### 4.2 無匯出/匯入 ⏭️（2026-07-05 使用者決定不做）
- **現況**：工具定義只存在 `%APPDATA%\agent_client\config.json`，換機器要手動搬。
- **建議**：工具頁加「Export / Import JSON」兩鈕（只搬 `Tools` 陣列，不含連線設定）。

### 4.3 無 Test Run ✅（2026-07-05 完成：Test Run 鈕用表單目前內容跑一次——JSON 參數彈窗 → 本地執行 → 結果彈窗）
- **現況**：登錄完要進對話讓 LLM 呼叫才知道工具能不能跑。
- **建議**：表單旁加「Test Run」：彈出參數輸入（依 Schema 欄位生成或直接給 JSON 編輯框），本地跑一次顯示 stdout/stderr。

## 5. 知識圖譜頁（graph_view.go / graph_client.go）

### 5.1 查詢參數寫死 ✅（2026-07-05 完成：搜尋列加 top-k（5/10/20）與 hops（1/2/3）Select）
- **現況**：`HybridSearch` 的 `topK=5`、`hops=1`；`GetLocalContext` 的 `hops=1, limit=20` 都寫死在 view。
- **建議**：搜尋列旁加兩個小 Select（top-k、hops），預設維持現值。

### 5.2 純文字列表、無視覺化 ✅（2026-07-05 完成：放射狀 node-edge 畫布取代鄰居列表，點鄰居節點跳轉重新查詢；>16 鄰居以註記代替）
- **現況**：中心實體 + 鄰居 + 關係都是 List 文字。
- **建議**：用 Fyne canvas 畫簡易 node-edge 圖（中心節點 + 1-hop 放射狀排列，點節點可跳轉重新查詢）。工作量中等，體驗提升明顯。

### 5.3 缺 proposed_relations 審核 UI（平台級流程斷點，建議優先）✅（2026-07-05 完成：agent_graph 補 list/approve/reject 三端點 + client 知識圖譜頁新增 Pending Relations 子分頁）
- **現況**：agent_graph 的 LLM 抽取關係中，低信心者進 `proposed_relations` 待審——但目前**沒有任何前端**能列出/核准/拒絕它們，佇列只進不出。
- **建議**：知識圖譜頁加「Pending Relations」子區（或獨立分頁）：列表 + Approve / Reject 按鈕。需要 agent_graph 端補對應的 list/approve REST 端點（後端範圍）。

## 6. 設定頁 / 其他（settings_view.go / main_view.go）

### 6.1 加 Test Connection 按鈕 ✅（2026-07-05 完成，見 P3）
- **現況**：URL 設錯要到第一次對話/搜尋才發現。
- **建議**：儲存鈕旁加「Test」：分別 GET agents 與 agent_graph 的 `/health`，狀態列顯示兩個 ✓/✗ 與延遲毫秒數。

### 6.2 RAG ID 無多組切換 ✅（2026-07-05 完成：Config.KnownAgentIDs + 設定頁 SelectEntry 可編輯下拉 + 對話頁頂部快速切換器（切換即開新對話脈絡）；State 加 OnCfgChanged/NotifyCfgChanged 跨分頁同步）
- **現況**：單一自由文字欄位；多 persona 部署（每 persona 一個 agent_id）時切換要手打。
- **建議**：改成可儲存的下拉（Config 加 `KnownAgentIDs []string`），對話頁頂部也可放快速切換器。

### 6.3 logger 設定路徑相對 CWD
- **現況**：`newAppLogger` 讀相對路徑 `app/config/config.yaml`——exe 從其他目錄啟動時靜默 fallback 到內建預設。
- **建議**：改成相對 `os.Executable()` 所在目錄解析，找不到再 fallback。

### 6.4 Fyne 升版風險（既有記載，重申）
- **現況**：views 內從 goroutine 直接呼叫 widget 更新，Fyne v2.4.5 可行。
- **注意**：升 v2.5+ 需全面改 `fyne.Do()` 包裝，屆時是一次跨檔案的機械性改動，建議升版時一次處理。
