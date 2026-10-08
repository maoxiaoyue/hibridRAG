// models_view.go — 「Models」分頁（內網 / dev_090 專用）。
//
// 上半：模型目錄（agent_registry；名稱 + text/image/audio modalities + capabilities），
//       可「Sync from Ollama」重新探測。
// 下半：各 persona（Agent 1-4）一個模型下拉 + Set → 寫該 persona 的 ROLE.md（經 agents 內網 REST）。
//
// 只在 State.MDClient != nil（RegistryBaseURL 或 ModelAdminBaseURL 至少一個有設）時建立。
package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"rag_admin/app/models"
)

func modalityTags(m models.Modalities) string {
	var t []string
	if m.Text {
		t = append(t, "text")
	}
	if m.Image {
		t = append(t, "image")
	}
	if m.Audio {
		t = append(t, "audio")
	}
	if len(t) == 0 {
		return "-"
	}
	return strings.Join(t, "/")
}

func modelsTab(st *State) fyne.CanvasObject {
	mc := st.MDClient

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	setStatus := func(s string) { status.SetText(s) }

	var modelList []models.LLMModel
	var modelNames []string

	list := widget.NewList(
		func() int { return len(modelList) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			m := modelList[i]
			o.(*widget.Label).SetText(fmt.Sprintf("%s   [%s]   %s",
				m.Name, modalityTags(m.Modalities), strings.Join(m.Capabilities, ",")))
		},
	)
	listScroll := container.NewVScroll(list)
	listScroll.SetMinSize(fyne.NewSize(0, 220))

	personaBox := container.NewVBox()

	var loadPersonas func()

	// rebuildPersonas 依目前 modelNames 為每個 persona 建下拉 + Set + Edit。
	rebuildPersonas := func(personas []models.PersonaInfo) {
		objs := make([]fyne.CanvasObject, 0, len(personas))
		for _, p := range personas {
			p := p
			sel := widget.NewSelect(modelNames, nil)
			if p.Model != "" {
				sel.SetSelected(p.Model)
			}
			setBtn := widget.NewButton("Set", nil)
			setBtn.OnTapped = func() {
				model := sel.Selected
				if model == "" {
					setStatus("Pick a model for " + p.AgentID + " first")
					return
				}
				setBtn.Disable()
				setStatus("Setting " + p.AgentID + " → " + model + " ...")
				go func() {
					defer setBtn.Enable()
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					info, err := st.Gateway.SetPersonaModel(ctx, p.AgentID, model, "")
					if err != nil {
						setStatus("Set " + p.AgentID + " FAIL — " + err.Error())
						return
					}
					setStatus(fmt.Sprintf("Set %s → %s OK (takes effect immediately)", info.AgentID, info.Model))
				}()
			}

			// Edit：整份載入該 persona 的 SKILL.md 到彈窗編輯，Save 後整份覆寫 + reload，
			// 再重新拉一次 persona 清單（編輯內容裡可能順手改了 ## Model，讓下拉跟著更新）。
			editBtn := widget.NewButton("Edit", nil)
			editBtn.OnTapped = func() {
				editBtn.Disable()
				setStatus("Loading SKILL.md for " + p.AgentID + " ...")
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					content, err := st.Gateway.GetPersonaSkill(ctx, p.AgentID)
					editBtn.Enable()
					if err != nil {
						setStatus("Load SKILL.md " + p.AgentID + " FAIL — " + err.Error())
						return
					}
					setStatus("")
					showSkillEditDialog(st.Win, p.AgentID, content, func(newContent string) {
						setStatus("Saving SKILL.md for " + p.AgentID + " ...")
						go func() {
							sctx, scancel := context.WithTimeout(context.Background(), 30*time.Second)
							defer scancel()
							if err := st.Gateway.SetPersonaSkill(sctx, p.AgentID, newContent); err != nil {
								setStatus("Save SKILL.md " + p.AgentID + " FAIL — " + err.Error())
								return
							}
							setStatus("Saved SKILL.md for " + p.AgentID + " (takes effect immediately)")
							loadPersonas()
						}()
					})
				}()
			}

			label := widget.NewLabel(fmt.Sprintf("%s  (%s)", p.Name, p.AgentID))
			buttons := container.NewHBox(setBtn, editBtn)
			objs = append(objs, container.NewBorder(nil, nil, label, buttons, sel))
		}
		if len(objs) == 0 {
			objs = append(objs, widget.NewLabel("No personas found (need skills/{agent_id}/SKILL.md on the agents host)."))
		}
		personaBox.Objects = objs
		personaBox.Refresh()
	}

	loadPersonas = func() {
		if st.Gateway == nil {
			rebuildPersonas(nil)
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ps, err := st.Gateway.ListPersonas(ctx)
			if err != nil {
				setStatus("List personas FAIL — " + err.Error())
				return
			}
			rebuildPersonas(ps)
		}()
	}

	applyModels := func(ms []models.LLMModel) {
		modelList = ms
		modelNames = make([]string, 0, len(ms))
		for _, m := range ms {
			modelNames = append(modelNames, m.Name)
		}
		list.Refresh()
	}

	loadModels := func() {
		if !mc.HasRegistry() {
			setStatus("Registry URL not set — model catalog unavailable")
			loadPersonas()
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ms, err := mc.ListModels(ctx)
			if err != nil {
				setStatus("List models FAIL — " + err.Error())
				// persona 清單走完全獨立的 gRPC 路徑（ListPersonas via gateway），
				// registry 目錄失敗不該連帶讓 Agent 1-4 區塊也看不到——只是下拉的
				// 模型選項會是空的（modelNames 尚未填）。
				loadPersonas()
				return
			}
			applyModels(ms)
			setStatus(fmt.Sprintf("%d models", len(ms)))
			loadPersonas() // personas 的下拉依賴 modelNames，先載完模型才建
		}()
	}

	refreshBtn := widget.NewButton("Refresh", loadModels)
	syncBtn := widget.NewButton("Sync from Ollama", func() {
		setStatus("Syncing from Ollama...")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			ms, err := mc.SyncModels(ctx)
			if err != nil {
				setStatus("Sync FAIL — " + err.Error())
				return
			}
			applyModels(ms)
			setStatus(fmt.Sprintf("Synced %d models from Ollama", len(ms)))
			loadPersonas()
		}()
	})
	if !mc.HasRegistry() {
		syncBtn.Disable()
	}

	header := container.NewHBox(
		widget.NewLabelWithStyle("Model Catalog", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		refreshBtn, syncBtn,
	)
	personaHeader := widget.NewLabelWithStyle("Persona Models (Agent 1-4)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	loadModels() // 初次載入

	// persona 設定改走 gateway（ListPersonas/SetPersonaModel）——沒設 gateway 時只顯示目錄。
	if st.Gateway == nil {
		return container.NewBorder(header, status, nil, nil, listScroll)
	}

	top := container.NewVBox(header, listScroll, widget.NewSeparator(), personaHeader)
	return container.NewBorder(top, status, nil, nil, container.NewVScroll(personaBox))
}
