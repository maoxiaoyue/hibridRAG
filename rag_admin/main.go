// rag_admin — 本地/管理用 Windows 桌面 client（原 agent_client），連線到 agents/RAG 平台。
// 與外網版 pub_client 區分：rag_admin 供內網管理（含 Models / Graph Input 等管理面）。
//
// 功能：
//   - 設定後端 URL/port（設定頁）
//   - 與 agent 同步對話，並在 agent 要求時於本機執行已登錄的工具（對話頁 + 工具頁）
//   - 上傳 PDF / 圖片 / 文字做知識攝取（上傳頁）
//
// 建置需求：CGO（Fyne 預設 GL driver），Windows 上需 gcc（MinGW-w64）。
package main

import "rag_admin/app/views"

func main() {
	views.RegisterSchemas()
	views.Run()
}
