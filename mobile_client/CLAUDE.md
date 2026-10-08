# mobile_client — 工作筆記

> 建立：2026-07-09（pub_client 功能的 Android 移植）
> 功能對照 / 平台替代 / 建置指南見 [README.md](README.md)；本檔只記開發者須知。

## 定位

pub_client（Windows Fyne 桌面版）的 Android 版本，dev_pub_0.9 外網架構的第二個
end-user client。**獨立 go.mod**（不與 pub_client 共用 module——桌面版帶著
DuckDB/hypgo 依賴，Android 編不過去；共用的是 proto 契約與程式碼「形狀」）。

## 與 pub_client 的同步義務

- `pb/gatewaypb/` 是從 `pub_client/pb/gatewaypb/` **拷貝**的生成碼。
  `agent_gateway/proto/client_gateway.proto` 契約變更時，兩份都要重新生成。
- `gateway_client.go` / `chat_service.go` / `graph_view.go` / `graph_widget.go`
  邏輯與桌面版平行——修桌面版 bug 時檢查這裡要不要跟。

## 刻意不同之處（不要「順手同步」回桌面版行為）

| 主題 | 這裡的做法 | 為什麼 |
|------|-----------|--------|
| 儲存 | `app/store` AES-256-GCM JSON | go-duckdb 無 Android binding |
| device_id | 隨機持久化（`and_` 前綴） | 無 MachineGuid 等價物 |
| 本機工具 / Coding Agent / Vault | 完全不存在 | Android 沙箱 / 無使用情境；engine 對 tool_calls 回錯續推理 |
| 選檔 | `file_pick.go`（URI reader → in-memory） | SAF 沒有檔案路徑 |
| graph 互動 | Tappable + Draggable（桌面版是 desktop.Mouseable） | 觸控事件模型不同 |
| Fyne 版本 | **v2.5.5**（桌面版 v2.4.5） | v2.4.x 的 Android driver 用了 NDK 26+ 已移除的 `ALooper_pollAll` |
| 錯誤 | 標準 `errors` | 不帶 hypgo，APK 精簡 |

## 建置備忘

- APK：`fyne package -os android/arm64`（NDK 29 + fyne CLI v1.7.2 驗證過）；
  產物 `Agent_RAG.apk` 已 gitignore
- 升級 Fyne 到 v2.6+ 前注意：2.6 起 UI 更新必須在 main thread（`fyne.Do`），
  本 codebase 沿用 2.4/2.5 的「goroutine 直接 SetText」慣例，升級需全面加包裝
- 中文字型：Android 系統 `.ttc` Fyne 吃不了；要可靠顯示中文請放一個 TTF 進
  `app/views/fonts/`（`theme.go` 會 go:embed + go-text 預解析驗證）

## 驗證狀態（2026-07-09）

`go build` / `go vet` / `go test ./...` 全綠（Windows）；`fyne package -os android/arm64`
成功產出 APK。**尚未**：實體手機端到端（等部署機起服務後 adb install 實測）。
