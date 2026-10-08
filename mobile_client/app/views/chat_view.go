package views

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"mobile_client/app/services"
	"mobile_client/app/store"
)

// chatTab 對話頁：送訊息、顯示回覆（Markdown 渲染）、附加圖片（server 視覺辨識）。
// 手機版沒有本機工具 / Code 模式（桌面版專屬）。
func chatTab(st *State) fyne.CanvasObject {
	history := widget.NewRichTextFromMarkdown("")
	history.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(history)

	// sb 累積整份轉錄的 Markdown 原文——既是畫面渲染來源，也是轉錄存檔的內容。
	var sb strings.Builder
	appendMD := func(md string) {
		sb.WriteString(md)
		sb.WriteString("\n\n")
		history.ParseMarkdown(sb.String())
		scroll.ScrollToBottom()
	}

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord

	input := widget.NewEntry()
	input.SetPlaceHolder("Type a message...")

	// 附加圖片：send 時逐張送 server 做視覺辨識（抽文字併進本輪訊息 + 同時 ingest 進 RAG）。
	var attached []attachment
	attachRow := container.NewHBox()
	attachScroll := container.NewHScroll(attachRow)
	attachScroll.SetMinSize(fyne.NewSize(0, attachThumbSize+20))
	attachScroll.Hide()
	var refreshAttach func()
	refreshAttach = func() {
		attachRow.Objects = attachRow.Objects[:0]
		for i := range attached {
			i := i
			cell := buildAttachThumb(st.Win, attached[i], func() {
				attached = append(attached[:i], attached[i+1:]...)
				refreshAttach()
			})
			attachRow.Objects = append(attachRow.Objects, cell)
		}
		attachRow.Refresh()
		if len(attached) == 0 {
			attachScroll.Hide()
		} else {
			attachScroll.Show()
		}
	}

	// 跨多次送出保留 session id（同一對話脈絡）。
	sessionID := ""
	lastReply := ""

	copyBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), nil)
	copyBtn.OnTapped = func() {
		if lastReply != "" {
			st.Win.Clipboard().SetContent(lastReply)
			status.SetText("Reply copied to clipboard")
		}
	}
	copyBtn.Disable()

	historyBtn := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		showSessionBrowser(st)
	})

	// saveTranscript 每輪結束後整份轉錄 upsert 進加密 store（best-effort，失敗只記 log）。
	saveTranscript := func() {
		if sessionID == "" {
			return
		}
		if err := store.SaveSession(sessionID, st.Cfg.AgentID, sb.String()); err != nil {
			st.Log.Printf("save session: %v", err)
		}
	}

	// RAG ID 快速切換器：多 persona 部署時不用進設定頁；切換即開新對話脈絡。
	agentSel := widget.NewSelect(st.Cfg.AgentIDOptions(), nil)
	agentSel.Selected = st.Cfg.AgentID
	agentSel.OnChanged = func(id string) {
		if id == "" || id == st.Cfg.AgentID {
			return
		}
		st.Cfg.AgentID = id
		st.Cfg.RememberAgentID(id)
		st.RebuildClient()
		st.SaveConfig()
		sessionID = ""
		appendMD(fmt.Sprintf("*(switched to RAG ID %q — new conversation)*", id))
		status.SetText("Switched RAG ID to " + id)
	}
	// 設定頁 Save 之後同步選項與目前值（直接設欄位不觸發 OnChanged）。
	st.OnCfgChanged(func() {
		agentSel.Options = st.Cfg.AgentIDOptions()
		agentSel.Selected = st.Cfg.AgentID
		agentSel.Refresh()
	})

	// 送出期間狀態：cancelSend 非 nil 代表有一輪請求在跑（擋重複送出 + 供 Stop 中斷）。
	var cancelSend context.CancelFunc
	var sendBtn *widget.Button

	stopBtn := widget.NewButtonWithIcon("", theme.MediaStopIcon(), func() {
		if cancelSend != nil {
			cancelSend()
		}
	})
	stopBtn.Disable()

	var send func()
	send = func() {
		if cancelSend != nil { // 上一輪還在跑（輸入框已 disable，此為保險）
			return
		}
		text := strings.TrimSpace(input.Text)
		if text == "" && len(attached) == 0 {
			return
		}
		if st.Gateway == nil {
			dialog.ShowError(services.ErrNoConnection, st.Win)
			return
		}
		// 擷取本輪附加圖片並清空（下一輪重新附加）。
		imgs := attached
		attached = nil
		refreshAttach()
		input.SetText("")
		display := text
		if len(imgs) > 0 {
			names := make([]string, len(imgs))
			for i, a := range imgs {
				names[i] = a.name
			}
			note := "*(附加圖片: " + strings.Join(names, ", ") + ")*"
			if display == "" {
				display = note
			} else {
				display = display + "\n\n" + note
			}
		}
		appendMD("**You:** " + display)

		// phase 保存目前階段文字（Thinking... 等），由 statusFn 更新、
		// 計時器 goroutine 每秒讀出並補上已等待秒數。
		var phase atomic.Value
		phase.Store("Thinking...")
		statusFn := func(m string) {
			phase.Store(m)
			status.SetText(m)
		}

		// 新對話首輪：client 端先產生 session id，讓狀態輪詢（ChatStatus）首輪就有 key 可查。
		newConversation := sessionID == ""
		if newConversation {
			sessionID = services.NewSessionID()
		}

		eng := services.NewChatEngine(st.Gateway, statusFn)
		if newConversation {
			eng.SetPreferredSessionID(sessionID)
		} else {
			eng.SetSession(sessionID)
		}
		eng.SetTemperature(st.Cfg.Temperature)

		ctx, cancel := context.WithCancel(context.Background())
		cancelSend = cancel
		input.Disable()
		sendBtn.Disable()
		stopBtn.Enable()

		// 便宜版串流回饋：每秒把「階段 (已等待秒數)」寫進狀態列，長回應期間看得出沒卡死。
		tickerDone := make(chan struct{})
		stopTicker := sync.OnceFunc(func() { close(tickerDone) })
		go func() {
			start := time.Now()
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-tickerDone:
					return
				case <-t.C:
					// 每秒向 gateway 問「這個 session 目前哪個 Agent 在處理」；
					// active 時把階段寫進 phase，非 active 維持本地階段。
					if sessionID != "" && st.Gateway != nil {
						pctx, pcancel := context.WithTimeout(context.Background(), 2*time.Second)
						if stage, active, perr := st.Gateway.ChatStatus(pctx, sessionID); perr == nil && active && stage != "" {
							phase.Store(stage)
						}
						pcancel()
					}
					status.SetText(fmt.Sprintf("%s (%ds)", phase.Load().(string), int(time.Since(start).Seconds())))
				}
			}
		}()

		go func() {
			defer func() {
				stopTicker()
				cancel()
				cancelSend = nil
				stopBtn.Disable()
				sendBtn.Enable()
				input.Enable()
			}()
			status.SetText("Thinking...")
			// 附加圖片：先送 server 做視覺辨識，把抽出的文字併進本輪訊息。
			sentText := text
			if len(imgs) > 0 {
				var b strings.Builder
				for i, a := range imgs {
					statusFn(fmt.Sprintf("Recognizing image %d/%d...", i+1, len(imgs)))
					txt, cerr := st.Gateway.ChatImage(ctx, st.Cfg.AgentID, a.name, a.data)
					if cerr != nil {
						appendMD(fmt.Sprintf("*(image recognition failed: %s — %v)*", a.name, cerr))
						continue
					}
					fmt.Fprintf(&b, "[附加圖片: %s]\n%s\n\n", a.name, txt)
				}
				sentText = b.String() + text
			}
			reply, err := eng.Send(ctx, sentText)
			stopTicker() // 先停計時器再寫終局狀態，避免最後一 tick 蓋掉結果
			sessionID = eng.SessionID()
			if err != nil {
				if errors.Is(err, context.Canceled) {
					status.SetText("Stopped")
					appendMD("*(request stopped by user)*")
				} else {
					status.SetText("")
					st.Log.Printf("chat failed: %v", err)
					appendMD("**Error:** " + err.Error())
				}
				saveTranscript()
				return
			}
			lastReply = reply
			copyBtn.Enable()
			if u := eng.Usage(); u != nil {
				status.SetText(fmt.Sprintf("tokens: %d prompt + %d completion = %d total",
					u.PromptTokens, u.CompletionTokens, u.TotalTokens))
			} else {
				status.SetText("")
			}
			appendMD("**Assistant:**\n\n" + reply)
			saveTranscript()
		}()
	}
	input.OnSubmitted = func(string) { send() }

	attachBtn := widget.NewButtonWithIcon("", theme.MediaPhotoIcon(), func() {
		pickFile(st, []string{".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp"},
			func(name string, data []byte) {
				attached = append(attached, attachment{name: name, data: data})
				refreshAttach()
			})
	})

	sendBtn = widget.NewButtonWithIcon("", theme.MailSendIcon(), send)
	sendBtn.Importance = widget.HighImportance
	newConv := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		sessionID = ""
		lastReply = ""
		attached = nil
		refreshAttach()
		copyBtn.Disable()
		sb.Reset()
		history.ParseMarkdown("")
		status.SetText("Started a new conversation")
	})

	// 頂列：RAG ID 切換 + New / History / Copy（icon 按鈕，配合手機窄版面）。
	topBar := container.NewBorder(nil, nil, nil,
		container.NewHBox(newConv, historyBtn, copyBtn), agentSel)
	inputBar := container.NewBorder(nil, nil, attachBtn, container.NewHBox(sendBtn, stopBtn), input)
	bottom := container.NewVBox(attachScroll, status, inputBar)

	return container.NewBorder(topBar, bottom, nil, nil, scroll)
}
