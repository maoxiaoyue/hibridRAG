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

	"mobile_client/app/services"
	"mobile_client/app/store"
)

// settingsTab 連線設定：Gateway Address + API Key（dev_pub_0.9——client 只認識
// agent_gateway 一個位址，全部流量走 gRPC）。手機版沒有 Coding Workspace /
// Vault Folder（桌面版專屬功能）。
func settingsTab(st *State) fyne.CanvasObject {
	gatewayEntry := widget.NewEntry()
	gatewayEntry.SetText(st.Cfg.GatewayAddr)
	gatewayEntry.SetPlaceHolder("host:port, e.g. 192.168.50.103:9090")

	apiKeyEntry := widget.NewPasswordEntry()
	apiKeyEntry.SetText(st.Cfg.APIKey)
	apiKeyEntry.SetPlaceHolder("sak_... (bound to this device on first use)")

	// RAG ID 用可編輯下拉（SelectEntry）：可直接輸入新 ID，也可從用過的清單挑。
	agentEntry := widget.NewSelectEntry(st.Cfg.AgentIDOptions())
	agentEntry.SetText(st.Cfg.AgentID)
	agentEntry.SetPlaceHolder("agent-main")

	insecure := widget.NewCheck("Allow self-signed TLS", nil)
	insecure.SetChecked(st.Cfg.InsecureTLS)

	tempValue := widget.NewLabel(fmt.Sprintf("%.2f", st.Cfg.Temperature))
	tempSlider := widget.NewSlider(0, 1)
	tempSlider.Step = 0.05
	tempSlider.Value = float64(st.Cfg.Temperature)
	tempSlider.OnChanged = func(v float64) { tempValue.SetText(fmt.Sprintf("%.2f", v)) }
	tempRow := container.NewBorder(nil, nil, nil, tempValue, tempSlider)
	tempItem := widget.NewFormItem("Temperature", tempRow)
	tempItem.HintText = "Chat sampling temperature; 0 = use the server default"

	deviceLabel := widget.NewLabel("Device ID: " + shortID(st.DeviceID()))
	deviceLabel.Wrapping = fyne.TextWrapWord
	pathLabel := widget.NewLabel("Encrypted store: " + store.StorePath())
	pathLabel.Wrapping = fyne.TextWrapWord

	form := widget.NewForm(
		widget.NewFormItem("Gateway Address", gatewayEntry),
		widget.NewFormItem("API Key", apiKeyEntry),
		widget.NewFormItem("RAG ID", agentEntry),
		widget.NewFormItem("TLS", insecure),
		tempItem,
	)

	save := widget.NewButton("Save", func() {
		st.Cfg.GatewayAddr = strings.TrimSpace(gatewayEntry.Text)
		st.Cfg.APIKey = strings.TrimSpace(apiKeyEntry.Text)
		st.Cfg.AgentID = strings.TrimSpace(agentEntry.Text)
		st.Cfg.RememberAgentID(st.Cfg.AgentID)
		st.Cfg.InsecureTLS = insecure.Checked
		st.Cfg.Temperature = float32(tempSlider.Value)
		st.RebuildClient()
		st.SaveConfig()
		agentEntry.SetOptions(st.Cfg.AgentIDOptions())
		st.NotifyCfgChanged()
		dialog.ShowInformation("Saved", "Connection settings have been updated and saved.", st.Win)
	})
	save.Importance = widget.HighImportance

	// Test Connection：用表單目前輸入值打 gateway 的 Ping（同時驗 API Key 與裝置綁定）。
	testResult := widget.NewLabel("")
	testResult.Wrapping = fyne.TextWrapWord
	var testBtn *widget.Button
	testBtn = widget.NewButton("Test Connection", func() {
		gw := services.NewGatewayClient(
			strings.TrimSpace(gatewayEntry.Text), strings.TrimSpace(agentEntry.Text),
			strings.TrimSpace(apiKeyEntry.Text), st.DeviceID(), insecure.Checked)
		if gw == nil {
			testResult.SetText("Gateway Address is empty")
			return
		}
		testBtn.Disable()
		testResult.SetText("Testing...")
		go func() {
			defer testBtn.Enable()
			defer gw.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			latency, keyName, err := gw.Ping(ctx)
			if err != nil {
				testResult.SetText("Gateway: FAIL — " + err.Error())
				return
			}
			name := keyName
			if name == "" {
				name = "(auth disabled)"
			}
			testResult.SetText(fmt.Sprintf("Gateway: OK (%d ms, key: %s)", latency.Milliseconds(), name))
		}()
	})

	buttons := container.NewGridWithColumns(2, save, testBtn)
	return container.NewVScroll(container.NewVBox(form, buttons, testResult, deviceLabel, pathLabel))
}

// shortID 顯示用：長 id 只留前 12 碼。
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}
