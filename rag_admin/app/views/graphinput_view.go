// graphinput_view.go — 「Graph Input」分頁：知識圖譜人工輸入（內網 dev_090 專用）。
//
// Config.GraphInputBaseURL 留空時 main_view 完全不建這個分頁（外網 dev_pub_0.9
// 預設如此）。三個子分頁：Documents（文件雙路徑上傳）/ Structured（實體/關係
// 表單 + 重複偵測）/ History（輸入紀錄）。
package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"rag_admin/app/models"
)

func graphInputTab(st *State) fyne.CanvasObject {
	sub := container.NewAppTabs(
		container.NewTabItem("Documents", giDocsView(st)),
		container.NewTabItem("Structured", giStructuredView(st)),
		container.NewTabItem("History", giHistoryView(st)),
	)
	sub.SetTabLocation(container.TabLocationTop)
	return sub
}

// ── Documents：文件雙路徑上傳 ────────────────────────────────────────────────

func giDocsView(st *State) fyne.CanvasObject {
	var selected []string

	listLabel := widget.NewLabel("No files selected")
	listLabel.Wrapping = fyne.TextWrapWord
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord

	refreshList := func() {
		if len(selected) == 0 {
			listLabel.SetText("No files selected")
			return
		}
		listLabel.SetText(strings.Join(selected, "\n"))
	}

	// 用 Windows 檔案總管（comdlg32 原生，多選）——與 Chat/Upload 分頁一致。
	pick := widget.NewButton("Add Files...", func() {
		paths, ok := nativeOpenFiles("Select documents to ingest",
			[][2]string{
				{"Documents & images", "*.md;*.markdown;*.txt;*.pdf;*.png;*.jpg;*.jpeg;*.webp;*.gif;*.bmp"},
				{"All files", "*.*"},
			}, true)
		if !ok || len(paths) == 0 {
			return
		}
		selected = append(selected, paths...)
		refreshList()
	})
	clearBtn := widget.NewButton("Clear", func() {
		selected = nil
		refreshList()
	})

	// 路由選項：預設 temp（先落 vault/temp，由 digest worker 分類，符合「一樣先放 temp」）。
	// obsidian=直接進 inbox 筆記；pg=立即抽取進圖譜；both=兩者（抽取兩次）。
	targetSel := widget.NewSelect([]string{"temp", "obsidian", "pg", "both"}, nil)
	targetSel.Selected = "temp"
	targetHint := widget.NewLabel("temp: staged for auto-classification (recommended) / obsidian: vault inbox note / pg: extract into graph now / both: costs double extraction")
	targetHint.Wrapping = fyne.TextWrapWord

	tagsEntry := widget.NewEntry()
	tagsEntry.SetPlaceHolder("Tags (comma-separated, optional; obsidian frontmatter)")

	// 唯讀輸出用 Label 而非 disabled Entry——disabled Entry 的背景矩形在主題切換時
	// 不重繪（會殘留舊主題的白/深底），Label 背景透明、隨主題正確重繪。
	results := widget.NewLabel("")
	results.Wrapping = fyne.TextWrapWord

	var uploadBtn *widget.Button
	uploadBtn = widget.NewButton("Upload", func() {
		if st.GIClient == nil {
			return
		}
		if len(selected) == 0 {
			dialog.ShowError(fmt.Errorf("add at least one file first"), st.Win)
			return
		}
		files := append([]string{}, selected...)
		tags := splitComma(tagsEntry.Text)
		target := targetSel.Selected

		uploadBtn.Disable()
		status.SetText("Uploading & processing (extraction may take a while)...")
		go func() {
			defer uploadBtn.Enable()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			docResults, err := st.GIClient.UploadDocs(ctx, target, tags, files)
			if err != nil {
				status.SetText("Upload failed: " + err.Error())
				return
			}
			var sb strings.Builder
			okCount := 0
			for _, r := range docResults {
				if r.Error != "" {
					fmt.Fprintf(&sb, "✗ %s — %s\n", r.File, r.Error)
					continue
				}
				okCount++
				fmt.Fprintf(&sb, "✓ %s (%d chars)", r.File, r.ExtractedChars)
				if r.ObsidianPath != "" {
					fmt.Fprintf(&sb, " → %s", r.ObsidianPath)
				}
				if r.PGEntities > 0 || r.PGRelationsMerged > 0 || r.PGRelationsProposed > 0 {
					fmt.Fprintf(&sb, " → pg: %d entities, %d merged, %d proposed, %d embedded",
						r.PGEntities, r.PGRelationsMerged, r.PGRelationsProposed, r.PGEmbeddings)
				}
				sb.WriteString("\n")
			}
			results.SetText(sb.String())
			status.SetText(fmt.Sprintf("Done: %d/%d files processed", okCount, len(docResults)))
		}()
	})

	controls := container.NewVBox(
		container.NewHBox(pick, clearBtn, widget.NewLabel("target"), targetSel, uploadBtn),
		targetHint,
		tagsEntry,
		status,
	)
	split := container.NewVSplit(container.NewVScroll(listLabel), container.NewVScroll(results))
	split.Offset = 0.35
	return container.NewBorder(controls, nil, nil, nil, split)
}

// ── Structured：實體/關係表單 + 重複偵測 ─────────────────────────────────────

func giStructuredView(st *State) fyne.CanvasObject {
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord

	// 實體表單
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Entity name")
	catEntry := widget.NewEntry()
	catEntry.SetPlaceHolder("Category (optional)")
	descEntry := widget.NewMultiLineEntry()
	descEntry.SetPlaceHolder("Description (optional; needed for semantic search)")

	saveEntity := func(force bool) {
		if st.GIClient == nil {
			return
		}
		entity := models.GIEntityInput{
			Name:        strings.TrimSpace(nameEntry.Text),
			Category:    strings.TrimSpace(catEntry.Text),
			Description: strings.TrimSpace(descEntry.Text),
		}
		if entity.Name == "" {
			dialog.ShowError(fmt.Errorf("entity name is required"), st.Win)
			return
		}
		status.SetText("Saving entity...")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			resp, err := st.GIClient.UpsertEntity(ctx, entity, force)
			if err != nil {
				status.SetText("Save failed: " + err.Error())
				return
			}
			if !resp.Written {
				var names []string
				for _, c := range resp.Candidates {
					names = append(names, c.Name)
				}
				status.SetText("Similar entities exist: " + strings.Join(names, ", ") +
					" — use one of these names to merge, or Force Create to add anyway")
				return
			}
			msg := "Entity saved"
			if !resp.EmbeddingSynced {
				msg += " (embedding NOT synced — semantic search will miss it)"
			}
			status.SetText(msg)
		}()
	}

	saveBtn := widget.NewButton("Save Entity", func() { saveEntity(false) })
	forceBtn := widget.NewButton("Force Create", func() { saveEntity(true) })
	checkBtn := widget.NewButton("Check Duplicates", func() {
		if st.GIClient == nil {
			return
		}
		name := strings.TrimSpace(nameEntry.Text)
		if name == "" {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			candidates, err := st.GIClient.CheckDuplicates(ctx, name)
			if err != nil {
				status.SetText("Check failed: " + err.Error())
				return
			}
			if len(candidates) == 0 {
				status.SetText("No similar entities")
				return
			}
			var names []string
			for _, c := range candidates {
				names = append(names, c.Name)
			}
			status.SetText("Similar: " + strings.Join(names, ", "))
		}()
	})

	entityForm := widget.NewForm(
		widget.NewFormItem("Name", nameEntry),
		widget.NewFormItem("Category", catEntry),
		widget.NewFormItem("Description", descEntry),
	)

	// 關係表單
	srcEntry := widget.NewEntry()
	srcEntry.SetPlaceHolder("Source entity")
	typeEntry := widget.NewEntry()
	typeEntry.SetPlaceHolder("Relation type (default: related_to)")
	tgtEntry := widget.NewEntry()
	tgtEntry.SetPlaceHolder("Target entity")

	relBtn := widget.NewButton("Save Relation", func() {
		if st.GIClient == nil {
			return
		}
		src, tgt := strings.TrimSpace(srcEntry.Text), strings.TrimSpace(tgtEntry.Text)
		if src == "" || tgt == "" {
			dialog.ShowError(fmt.Errorf("source and target are required"), st.Win)
			return
		}
		status.SetText("Saving relation...")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := st.GIClient.UpsertRelation(ctx, src, tgt, strings.TrimSpace(typeEntry.Text)); err != nil {
				status.SetText("Save failed: " + err.Error())
				return
			}
			status.SetText(fmt.Sprintf("Relation saved: %s —[%s]→ %s", src, orDefault(typeEntry.Text, "related_to"), tgt))
		}()
	})

	relForm := widget.NewForm(
		widget.NewFormItem("Source", srcEntry),
		widget.NewFormItem("Type", typeEntry),
		widget.NewFormItem("Target", tgtEntry),
	)

	// CSV 批次
	csvKindSel := widget.NewSelect([]string{"entity", "relation"}, nil)
	csvKindSel.Selected = "entity"
	csvEntry := widget.NewMultiLineEntry()
	csvEntry.SetPlaceHolder("entity CSV: name,category,description\nrelation CSV: source,type,target")
	batchBtn := widget.NewButton("Batch Import", func() {
		if st.GIClient == nil || strings.TrimSpace(csvEntry.Text) == "" {
			return
		}
		status.SetText("Importing batch...")
		kind, csvText := csvKindSel.Selected, csvEntry.Text
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			resp, err := st.GIClient.BatchImportCSV(ctx, kind, csvText)
			if err != nil {
				status.SetText("Batch failed: " + err.Error())
				return
			}
			var fails []string
			for _, r := range resp.Rows {
				if r.Result != "ok" {
					fails = append(fails, fmt.Sprintf("row %d: %s", r.Index, r.Result))
				}
			}
			msg := fmt.Sprintf("Batch done: %d ok, %d failed", resp.OK, resp.Failed)
			if len(fails) > 0 {
				msg += "\n" + strings.Join(fails, "\n")
			}
			status.SetText(msg)
		}()
	})

	content := container.NewVBox(
		widget.NewLabelWithStyle("Entity", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		entityForm,
		container.NewHBox(checkBtn, saveBtn, forceBtn),
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Relation", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		relForm,
		container.NewHBox(relBtn),
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Batch Import (CSV)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewHBox(widget.NewLabel("kind"), csvKindSel, batchBtn),
		csvEntry,
		status,
	)
	return container.NewVScroll(content)
}

// ── History：輸入紀錄 ────────────────────────────────────────────────────────

func giHistoryView(st *State) fyne.CanvasObject {
	var entries []models.GIInputLog
	status := widget.NewLabel("Click Refresh to load input history")

	list := widget.NewList(
		func() int { return len(entries) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < len(entries) {
				e := entries[id]
				obj.(*widget.Label).SetText(fmt.Sprintf("%s  %s/%s  %s  [%s]",
					e.CreatedAt, e.Action, orDefault(e.Target, "-"), e.Result, e.Actor))
			}
		},
	)

	var refreshBtn *widget.Button
	refreshBtn = widget.NewButton("Refresh", func() {
		if st.GIClient == nil {
			return
		}
		refreshBtn.Disable()
		status.SetText("Loading...")
		go func() {
			defer refreshBtn.Enable()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			got, err := st.GIClient.ListLog(ctx, 100)
			if err != nil {
				status.SetText("Load failed: " + err.Error())
				return
			}
			entries = got
			list.Refresh()
			status.SetText(fmt.Sprintf("%d entries", len(entries)))
		}()
	})

	return container.NewBorder(container.NewHBox(refreshBtn), status, nil, nil, list)
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
