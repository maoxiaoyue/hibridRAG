// skill_edit_dialog.go — SKILL.md 全文編輯彈窗（Persona Models 分頁的 Edit 按鈕用）。
package views

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// showSkillEditDialog 彈出一個可編輯 SKILL.md 全文的視窗；按 Save 才呼叫 onSave（帶編輯
// 後的完整文字），按 Cancel 或關閉視窗則什麼都不做。整份覆寫比 Set 那顆按鈕（只改
// ## Model 段）範圍更寬——這是內網管理工具，直接編輯不設 diff 審核關卡。
func showSkillEditDialog(win fyne.Window, agentID, content string, onSave func(newContent string)) {
	editor := widget.NewMultiLineEntry()
	editor.SetText(content)
	editor.Wrapping = fyne.TextWrapOff

	scroll := container.NewScroll(editor)
	scroll.SetMinSize(fyne.NewSize(760, 480))

	d := dialog.NewCustomConfirm(
		fmt.Sprintf("Edit SKILL.md — %s", agentID), "Save", "Cancel",
		scroll,
		func(save bool) {
			if save {
				onSave(editor.Text)
			}
		},
		win,
	)
	d.Resize(fyne.NewSize(820, 580))
	d.Show()
}
