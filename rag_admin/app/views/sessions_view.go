// sessions_view.go — 對話 session 瀏覽器（History 按鈕）：先列最近幾條的標題，
// 點某條才開該對話的完整內容（取代舊的「直接開 sessions 資料夾」作法）。
package views

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"rag_admin/app/services"
)

// showSessionBrowser 彈出最近對話的標題清單；點一條開內容。
func showSessionBrowser(st *State) {
	metas, err := services.ListSessions(20)
	if err != nil {
		dialog.ShowError(fmt.Errorf("load sessions: %w", err), st.Win)
		return
	}
	if len(metas) == 0 {
		dialog.ShowInformation("Chat Sessions", "No saved conversations yet.", st.Win)
		return
	}

	list := widget.NewList(
		func() int { return len(metas) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < len(metas) {
				m := metas[id]
				obj.(*widget.Label).SetText(m.UpdatedAt.Format("01-02 15:04") + "  " + m.Title)
			}
		},
	)
	d := dialog.NewCustom("Chat Sessions — click a title to open", "Close", container.NewVScroll(list), st.Win)
	list.OnSelected = func(id widget.ListItemID) {
		if id < len(metas) {
			showSessionContent(st, metas[id])
		}
		list.UnselectAll()
	}
	d.Resize(fyne.NewSize(560, 460))
	d.Show()
}

// showSessionContent 顯示單一 session 的完整轉錄（Markdown 渲染）。
func showSessionContent(st *State, m services.SessionMeta) {
	transcript, err := services.ReadTranscript(m.SessionID)
	if err != nil {
		dialog.ShowError(fmt.Errorf("read transcript: %w", err), st.Win)
		return
	}
	viewer := widget.NewRichTextFromMarkdown(transcript)
	viewer.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustom(m.Title, "Close", container.NewVScroll(viewer), st.Win)
	d.Resize(fyne.NewSize(820, 560))
	d.Show()
}
