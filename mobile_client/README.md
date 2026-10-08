# mobile_client — Android 手機版 client（Go + Fyne）

pub_client（Windows 桌面版）的 Android 移植：同一套 dev_pub_0.9 外網架構——
全部後端流量收斂到 `agent_gateway` 單一 gRPC 連線（TLS + API Key 一 Key 一裝置綁定）。
同一份程式碼在桌面 `go build` 直接執行即可開發驗證（Fyne 跨平台）。

## 功能對照（vs pub_client）

| 功能 | pub_client（桌面） | mobile_client（Android） |
|------|:---:|:---:|
| 同步對話（Chat，含 ChatStatus 階段輪詢、Stop、token 用量） | ✅ | ✅ |
| 附加圖片視覺辨識（ChatImage） | ✅ | ✅（SAF 選圖，in-memory） |
| RAG ID 快速切換（多 persona） | ✅ | ✅ |
| 對話 session 歷史（加密儲存 + 瀏覽器） | ✅ DuckDB | ✅ AES-GCM JSON（上限 100 條自動修剪） |
| 文件上傳攝取 + job 輪詢 + Verify 檢索驗證 | ✅ | ✅（SAF 選檔，單檔 ≤64MB） |
| 知識圖譜關鍵字/語意搜尋、force-directed 區域圖、關鍵節點自動選取 | ✅ | ✅（觸控版：點選/拖曳/pan，+/- 縮放鈕） |
| 待審關係審核（Approve / Reject） | ✅ | ✅ |
| 本機工具（allowlist execv） | ✅ | ❌ 不適用（Android 沙箱） |
| 內建 Coding Agent（read/edit/write + diff 審核） | ✅ | ❌ 不移植 |
| Sync Vault（下載 Obsidian vault） | ✅ | ❌ 不移植（手機上沒有 Obsidian vault 資料夾語意） |

伺服端若因故回 `tool_calls`，engine 會逐一回「此裝置不支援本機工具」的錯誤結果並續推理，
不會卡死整輪對話。

## 平台替代（與桌面版的三個關鍵差異）

| 桌面版 | Android 版 | 原因 |
|--------|-----------|------|
| DuckDB 加密庫（`ATTACH ... ENCRYPTION_KEY`） | `app/store`：AES-256-GCM 加密 JSON（`config.enc` / `sessions.enc`，app 私有目錄，key 為隨機 32 bytes `store.key`） | go-duckdb 沒有 Android binding |
| device_id = SHA-256(Windows MachineGuid + username) | 首次啟動產生 `and_` 隨機 ID 持久化 | 純 Go 取不到 Android 穩定機器特徵（ANDROID_ID 需 JNI）；重灌 app 視同新裝置，換機用 `gateway apikey unbind` |
| 選檔用原生 Win32 對話框（檔案路徑） | Fyne dialog → SAF `content://` URI，內容當場讀進記憶體（`app/views/file_pick.go`） | Android 沙箱沒有可自由 os.Open 的路徑 |

## 中文字型

Fyne 內建字型不含中文字形，Android 系統中文字型多為 `.ttc`（Fyne 解析不了）。
啟動時依序嘗試：**內嵌字型**（把 `.ttf`/`.otf` 放進 `app/views/fonts/`，建置時打進 APK——最可靠，
見該資料夾 README）→ `FYNE_FONT` → 系統字型候選（每個候選先用 go-text 預解析驗證，失敗跳過）。

## 建置

```powershell
# 桌面驗證（Windows，需 CGO + MinGW-w64 gcc）
cd mobile_client
go build -o mobile_client.exe .
go test ./...

# Android APK（需 Android NDK；本機已驗證 NDK 29 + fyne CLI v1.7.2 + fyne v2.5.5）
go install fyne.io/tools/cmd/fyne@latest
$env:ANDROID_NDK_HOME = "C:\Users\<you>\AppData\Local\Android\Sdk\ndk\<version>"
fyne package -os android/arm64        # → Agent_RAG.apk（約 36MB）
# 舊 32-bit 手機另打：fyne package -os android/arm
```

> fyne v2.4.x 在 NDK 26+ 會因 `ALooper_pollAll` 被移除而編譯失敗——本 module 用 v2.5.5。

安裝：手機開「安裝未知來源 App」後 `adb install Agent_RAG.apk`，或直接傳檔案點開安裝。

## 設定與首次連線

1. Settings 分頁：填 Gateway Address（`host:port`，例 `test-server:9090`）與 API Key
   （部署機 `gateway apikey create` 簽發）；自簽憑證時勾 Allow self-signed TLS
2. Test Connection：打 gateway `Ping`，同時驗 API Key——首次成功呼叫即完成裝置綁定（TOFU）
3. 手機需連到能達 gateway 的網路（外網部署時只需能達 `:9090`；防火牆規劃見
   [deploy/README-deploy.md](../deploy/README-deploy.md)）

## 目錄結構

```
main.go                      # views.Run()
FyneApp.toml                 # app id / icon / 版本（fyne package 讀取）
Icon.png                     # app icon（512x512）
app/models/                  # Config / Chat / Graph DTO（純資料結構）
app/services/
  gateway_client.go          # 單一 gRPC 連線（ClientGateway 全 RPC；上傳走 in-memory chunk）
  chat_service.go            # 對話回合制引擎（無本機工具版；tool_calls 一律回錯續推理）
  device_id.go               # 隨機裝置 ID
  errors.go                  # 業務錯誤（標準 errors，不帶 hypgo）
app/store/store.go           # AES-256-GCM 加密 JSON 儲存（config + sessions）
app/views/
  main_view.go               # State + 四分頁（手機底部導覽列；桌面模擬手機直式視窗）
  chat_view.go               # 對話頁（icon 按鈕列、附圖縮圖列、狀態輪詢）
  upload_view.go             # 上傳頁（SAF 選檔累積多檔）
  graph_view.go              # 圖譜頁（內層 AppTabs：Results / Graph / Details / Review）
  graph_widget.go            # force-directed 圖（觸控版：Tappable + Draggable + ZoomBy）
  sessions_view.go           # 對話歷史瀏覽器
  settings_view.go           # 連線設定 + Test Connection
  file_pick.go               # SAF/URI 選檔 → (name, data)
  image_attach.go            # 附件縮圖 cell（in-memory resource）
  theme.go                   # CJK 字型（內嵌優先 + go-text 預解析驗證）
  fonts/                     # （可選）放 .ttf/.otf 內嵌 CJK 字型
pb/gatewaypb/                # 由 agent_gateway/proto/client_gateway.proto 生成（拷貝）
```

## 已知限制

- 未在實體 Android 手機上做過端到端驗證（等部署機 + 手機實測；Fyne GUI 無法無頭驗證）
- Fyne 檔案選擇器無多選——Upload 頁用「Add File 累積」替代
- 力導向圖無 pinch 縮放（Fyne 2.5 未提供雙指手勢事件），用 +/- 按鈕
- APK 未簽發行金鑰（fyne package 預設 debug 簽章）；上架需另配 keystore
- 正式上線前 InsecureTLS 應改 CA pinning（與桌面版相同的待辦）
