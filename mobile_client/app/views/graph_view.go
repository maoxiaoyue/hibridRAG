package views

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"mobile_client/app/models"
)

// graphTab 知識圖譜頁。桌面版是左右分割（HSplit）版面；手機直式螢幕改成
// 內層 AppTabs 四個子頁：Results（搜尋+結果）/ Graph（力導向圖）/
// Details（實體詳情+關係）/ Review（待審關係審核）。
// 點選結果會載入區域圖並自動切到 Graph 子頁。
// st.Gateway 為 nil（設定頁 Gateway Address 留空）時顯示停用提示，不建查詢 UI。
func graphTab(st *State) fyne.CanvasObject {
	if st.Gateway == nil {
		return container.NewCenter(widget.NewLabel(
			"Knowledge Graph is disabled — set the Gateway Address on the Settings tab"))
	}

	var results []models.GraphEntity

	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapWord

	searchEntry := widget.NewEntry()
	searchEntry.SetPlaceHolder("Search entities...")

	hopsSel := widget.NewSelect([]string{"1", "2", "3"}, nil)
	hopsSel.Selected = "1"

	var resultList *widget.List
	var sub *container.AppTabs // 下方宣告後回填；loadContext 成功時切到 Graph 子頁

	// ── Details 子頁內容 ──
	centerLabel := widget.NewRichTextFromMarkdown("*Select a result to see details*")
	centerLabel.Wrapping = fyne.TextWrapWord

	var loadContext func(entityName string)

	// 力導向圖 widget（觸控版：點節點選取、拖節點/pan、+/- 縮放）。
	gw := newGraphWidget(func(name string) { loadContext(name) })
	graphHint := container.NewCenter(widget.NewLabel(
		"Search and select a result\nto see its neighborhood graph"))
	graphOverlay := container.NewStack(gw, graphHint)

	relationsList := widget.NewList(
		func() int { return 0 },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Wrapping = fyne.TextWrapWord
			return l
		},
		func(widget.ListItemID, fyne.CanvasObject) {},
	)
	var relations []models.GraphRelation

	showContext := func(ctxResult models.GraphContext) {
		center := ctxResult.Center
		var b strings.Builder
		fmt.Fprintf(&b, "## %s\n", orPlaceholder(center.Name, "(unnamed)"))
		if center.Category != "" {
			fmt.Fprintf(&b, "**Category**: %s\n\n", center.Category)
		}
		if center.Description != "" {
			fmt.Fprintf(&b, "%s\n\n", center.Description)
		}
		if center.SourceFile != "" {
			fmt.Fprintf(&b, "*Source: %s*\n", center.SourceFile)
		}
		centerLabel.ParseMarkdown(b.String())

		relations = ctxResult.Relations
		relationsList.Length = func() int { return len(relations) }
		relationsList.UpdateItem = func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < len(relations) {
				r := relations[id]
				obj.(*widget.Label).SetText(fmt.Sprintf("%s —[%s]→ %s", r.Source, r.Type, r.Target))
			}
		}
		relationsList.Refresh()

		if graphHint.Visible() {
			graphHint.Hide()
		}
		gw.SetContext(ctxResult)
	}

	loadContext = func(entityName string) {
		statusLabel.SetText("Loading...")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ctxResult, err := st.Gateway.GetLocalContext(ctx, entityName, selectedInt(hopsSel, 1), 20)
			if err != nil {
				statusLabel.SetText("Load failed: " + err.Error())
				return
			}
			statusLabel.SetText("")
			showContext(ctxResult)
			if sub != nil && sub.SelectedIndex() == 0 {
				sub.SelectIndex(1) // 從 Results 子頁選取後自動切到 Graph
			}
		}()
	}

	resultList = widget.NewList(
		func() int { return len(results) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < len(results) {
				e := results[id]
				text := e.Name
				if e.Category != "" {
					text += " (" + e.Category + ")"
				}
				obj.(*widget.Label).SetText(text)
			}
		},
	)
	resultList.OnSelected = func(id widget.ListItemID) {
		if id < len(results) {
			loadContext(results[id].Name)
		}
	}

	runSearch := func(hybrid bool) {
		query := strings.TrimSpace(searchEntry.Text)
		if query == "" {
			return
		}
		statusLabel.SetText("Searching...")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			var entities []models.GraphEntity
			var err error
			if hybrid {
				var res models.GraphHybridSearchResult
				res, err = st.Gateway.HybridSearch(ctx, query, 5, selectedInt(hopsSel, 1))
				entities = res.Seeds
			} else {
				entities, err = st.Gateway.SearchEntities(ctx, query, 20)
			}
			if err != nil {
				statusLabel.SetText("Search failed: " + err.Error())
				return
			}
			results = entities
			resultList.Refresh()
			// 自動選取「最接近搜尋結果的關鍵節點」：相關性 × 連結度最高的實體，
			// 選取即觸發 loadContext，圖譜直接跳到該節點的鄰域。
			if key := keyNodeIndex(results); key >= 0 {
				resultList.Select(key)
				statusLabel.SetText(fmt.Sprintf("%d results — key node: %s (degree %d)",
					len(results), results[key].Name, results[key].Degree))
			} else {
				statusLabel.SetText(fmt.Sprintf("%d results", len(results)))
			}
		}()
	}

	searchEntry.OnSubmitted = func(string) { runSearch(false) }
	keywordBtn := widget.NewButton("Keyword", func() { runSearch(false) })
	hybridBtn := widget.NewButton("Semantic", func() { runSearch(true) })
	hybridBtn.Importance = widget.MediumImportance

	searchRow := container.NewBorder(nil, nil, nil,
		container.NewHBox(widget.NewLabel("hops"), hopsSel), searchEntry)
	buttonsRow := container.NewGridWithColumns(2, keywordBtn, hybridBtn)
	resultsPage := container.NewBorder(
		container.NewVBox(searchRow, buttonsRow), statusLabel, nil, nil, resultList)

	// ── Graph 子頁：控制列（Fit / Reset / 縮放）+ 力導向圖 ──
	fitBtn := widget.NewButton("Fit", gw.ZoomFit)
	resetBtn := widget.NewButton("Reset", gw.ResetLayout)
	zoomInBtn := widget.NewButton("+", func() { gw.ZoomBy(1.25) })
	zoomOutBtn := widget.NewButton("-", func() { gw.ZoomBy(0.8) })
	controlsBar := container.NewHBox(fitBtn, resetBtn, zoomInBtn, zoomOutBtn)
	graphPage := container.NewBorder(controlsBar, nil, nil, nil, graphOverlay)

	// ── Details 子頁 ──
	detailsPage := container.NewVSplit(
		container.NewVScroll(centerLabel),
		container.NewBorder(widget.NewLabel("Relations"), nil, nil, nil, relationsList),
	)

	// 進入分頁先載入幾筆實體，避免一開始空白（空 query 命中全部，取前 N）。
	go func() {
		statusLabel.SetText("Loading entities...")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		entities, err := st.Gateway.SearchEntities(ctx, "", 15)
		if err != nil {
			statusLabel.SetText("Load failed: " + err.Error())
			return
		}
		results = entities
		if len(results) == 0 {
			statusLabel.SetText("No entities yet")
		} else {
			statusLabel.SetText(fmt.Sprintf("%d entities (showing latest)", len(results)))
		}
		resultList.Refresh()
	}()

	sub = container.NewAppTabs(
		container.NewTabItem("Results", resultsPage),
		container.NewTabItem("Graph", graphPage),
		container.NewTabItem("Details", detailsPage),
		container.NewTabItem("Review", graphReviewView(st)),
	)
	sub.SetTabLocation(container.TabLocationTop)
	return sub
}

// selectedInt 把 Select 目前選值解析成 int；解析失敗回 def。
func selectedInt(s *widget.Select, def int) int {
	n := 0
	for _, c := range s.Selected {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return def
	}
	return n
}

// ── Review 子分頁（待審關係審核）─────────────────────────────────────────────

func graphReviewView(st *State) fyne.CanvasObject {
	var items []models.GraphProposedRelation
	selected := -1

	statusLabel := widget.NewLabel("Tap Refresh to load pending relations")
	statusLabel.Wrapping = fyne.TextWrapWord

	var list *widget.List
	list = widget.NewList(
		func() int { return len(items) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Wrapping = fyne.TextWrapWord
			return l
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < len(items) {
				it := items[id]
				from := it.ExtractedBy
				if it.SourceFile != "" {
					from += " @ " + it.SourceFile
				}
				obj.(*widget.Label).SetText(fmt.Sprintf("%s —[%s]→ %s   (conf %.2f, %s)",
					it.SourceEntity, it.RelationType, it.TargetEntity, it.Confidence, from))
			}
		},
	)
	list.OnSelected = func(id widget.ListItemID) { selected = id }

	var refreshBtn, approveBtn, rejectBtn *widget.Button
	setBusy := func(busy bool) {
		for _, b := range []*widget.Button{refreshBtn, approveBtn, rejectBtn} {
			if busy {
				b.Disable()
			} else {
				b.Enable()
			}
		}
	}

	refresh := func() {
		setBusy(true)
		statusLabel.SetText("Loading pending relations...")
		go func() {
			defer setBusy(false)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			got, err := st.Gateway.ListProposed(ctx, "", 100)
			if err != nil {
				statusLabel.SetText("Load failed: " + err.Error())
				return
			}
			items = got
			selected = -1
			list.UnselectAll()
			list.Refresh()
			statusLabel.SetText(fmt.Sprintf("%d pending relations", len(items)))
		}()
	}

	review := func(approve bool) {
		if selected < 0 || selected >= len(items) {
			statusLabel.SetText("Select a relation first")
			return
		}
		idx := selected
		it := items[idx]
		setBusy(true)
		verb, done := "Rejecting", "Rejected"
		if approve {
			verb, done = "Approving", "Approved (merged into graph)"
		}
		statusLabel.SetText(fmt.Sprintf("%s %s —[%s]→ %s ...", verb, it.SourceEntity, it.RelationType, it.TargetEntity))
		go func() {
			defer setBusy(false)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var err error
			if approve {
				_, err = st.Gateway.ApproveProposed(ctx, it.ID)
			} else {
				_, err = st.Gateway.RejectProposed(ctx, it.ID)
			}
			if err != nil {
				statusLabel.SetText("Review failed: " + err.Error())
				return
			}
			items = append(items[:idx], items[idx+1:]...)
			selected = -1
			list.UnselectAll()
			list.Refresh()
			statusLabel.SetText(fmt.Sprintf("%s: %s —[%s]→ %s (%d left)",
				done, it.SourceEntity, it.RelationType, it.TargetEntity, len(items)))
		}()
	}

	refreshBtn = widget.NewButton("Refresh", refresh)
	approveBtn = widget.NewButton("Approve", func() { review(true) })
	approveBtn.Importance = widget.HighImportance
	rejectBtn = widget.NewButton("Reject", func() { review(false) })
	rejectBtn.Importance = widget.DangerImportance

	toolbar := container.NewGridWithColumns(3, refreshBtn, approveBtn, rejectBtn)
	return container.NewBorder(toolbar, statusLabel, nil, nil, list)
}

func orPlaceholder(s, placeholder string) string {
	if strings.TrimSpace(s) == "" {
		return placeholder
	}
	return s
}

// keyNodeIndex 挑「最接近搜尋結果的關鍵節點」：相關性 ×（1 + ln(1+全圖 degree)）
// 最高者。語意搜尋用向量相似度當相關性；關鍵字搜尋沒有分數（score=0）視為同分，
// 等同純比連結度。無結果回 -1。
func keyNodeIndex(entities []models.GraphEntity) int {
	best, bestRank := -1, -1.0
	for i, e := range entities {
		score := e.Score
		if score <= 0 {
			score = 1
		}
		rank := score * (1 + math.Log1p(float64(e.Degree)))
		if rank > bestRank {
			best, bestRank = i, rank
		}
	}
	return best
}
