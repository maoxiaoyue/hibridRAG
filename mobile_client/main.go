// mobile_client — Android 手機版 client（Go + Fyne），功能對照 pub_client：
//
//   - 與 agent 同步對話（Chat 分頁；附加圖片走 server 視覺辨識）
//   - 上傳 PDF / 圖片 / 文字做知識攝取（Upload 分頁；Android SAF 檔案選擇）
//   - 知識圖譜搜尋 / 區域圖視覺化 / 待審關係審核（Graph 分頁）
//   - Gateway 位址 + API Key 設定（Settings 分頁）
//
// 全部後端流量走 agent_gateway 單一 gRPC 連線（dev_pub_0.9 架構）。
// 與桌面版的差異：
//   - 儲存：DuckDB 在 Android 沒有 binding → AES-256-GCM 加密 JSON（app 私有目錄）
//   - device_id：Android 取不到 Windows MachineGuid → 首次啟動產生隨機 ID 持久化
//   - 本機工具 / 內建 Coding Agent：手機上不適用，不移植（agent 若要求工具一律回錯）
//
// 打包：fyne package -os android/arm64（需 Android NDK）；桌面 go build 可直接執行同一套 UI 供開發驗證。
package main

import "mobile_client/app/views"

func main() {
	views.Run()
}
