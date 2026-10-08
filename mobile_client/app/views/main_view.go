// Package views 組裝 Fyne 手機介面：Chat / Upload / Graph / Settings 四個分頁。
// 同一套 UI 在桌面 go build 直接執行即可開發驗證（Fyne 跨平台）。
package views

import (
	"context"
	"log"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"

	"mobile_client/app/models"
	"mobile_client/app/services"
	"mobile_client/app/store"
)

// State client 全域共享狀態。
type State struct {
	App     fyne.App
	Win     fyne.Window
	Cfg     *models.Config
	Gateway *services.GatewayClient // nil = 未設定 GatewayAddr，各分頁顯示未設定提示
	Log     *log.Logger

	cfgListeners []func() // 設定變更通知（例：對話頁的 RAG ID 快速切換器要刷新選項）
}

// DeviceID 回傳本機裝置指紋（一 Key 一裝置）。Android 上沒有穩定機器特徵可用，
// 首次啟動產生隨機 ID 持久化；重灌 app 視同新裝置。
func (s *State) DeviceID() string {
	if s.Cfg.DeviceID == "" {
		s.Cfg.DeviceID = services.RandomDeviceID()
		if err := store.SaveConfig(s.Cfg); err != nil {
			s.Log.Printf("persist device id: %v", err) // best-effort；存不進去下次換 id（視同換機）
		}
	}
	return s.Cfg.DeviceID
}

// OnCfgChanged 註冊設定變更 listener（分頁建構時呼叫，跨分頁同步 UI 狀態用）。
func (s *State) OnCfgChanged(fn func()) { s.cfgListeners = append(s.cfgListeners, fn) }

// NotifyCfgChanged 廣播設定已變更。
func (s *State) NotifyCfgChanged() {
	for _, fn := range s.cfgListeners {
		fn()
	}
}

// RebuildClient 依目前設定重建 gateway 連線（設定變更後呼叫）。
func (s *State) RebuildClient() {
	if s.Gateway != nil {
		_ = s.Gateway.Close()
	}
	s.Gateway = services.NewGatewayClient(
		s.Cfg.GatewayAddr, s.Cfg.AgentID, s.Cfg.APIKey, s.DeviceID(), s.Cfg.InsecureTLS)
}

// SaveConfig 持久化設定並回報錯誤。
func (s *State) SaveConfig() {
	if err := store.SaveConfig(s.Cfg); err != nil {
		s.Log.Printf("failed to save config: %v", err)
		dialog.ShowError(err, s.Win)
	}
}

// Run 啟動 GUI（阻塞直到視窗關閉）。
func Run() {
	logger := log.Default() // stderr；Android 上進 logcat

	a := app.NewWithID("com.singleagent.mobileclient")
	// 加密儲存放 app 私有目錄（Android 沙箱 / 桌面為 fyne app 設定目錄）。
	if root := a.Storage().RootURI(); root != nil && root.Path() != "" {
		store.Init(root.Path())
	}

	cfg, loadErr := store.LoadConfig()
	if loadErr != nil {
		logger.Printf("failed to load config store, using defaults: %v", loadErr)
		cfg = models.DefaultConfig()
	}

	// Fyne 內建字型無中文字形，需另載 CJK 字型（否則中文顯示成 □/�）。
	if !applyCJKFont(a) {
		logger.Printf("no usable CJK font found; 中文可能無法正常顯示（可內嵌字型，見 app/views/fonts/README.md）")
	}

	w := a.NewWindow("Agent RAG")

	st := &State{App: a, Win: w, Cfg: cfg, Log: logger}
	st.RebuildClient()

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Chat", theme.MailComposeIcon(), chatTab(st)),
		container.NewTabItemWithIcon("Upload", theme.UploadIcon(), uploadTab(st)),
		container.NewTabItemWithIcon("Graph", theme.SearchIcon(), graphTab(st)),
		container.NewTabItemWithIcon("Settings", theme.SettingsIcon(), settingsTab(st)),
	)
	if fyne.CurrentDevice().IsMobile() {
		tabs.SetTabLocation(container.TabLocationBottom) // 手機慣例：底部導覽列
	} else {
		tabs.SetTabLocation(container.TabLocationTop)
		w.Resize(fyne.NewSize(420, 780)) // 桌面驗證時模擬手機直式比例
	}

	w.SetContent(tabs)

	if loadErr != nil {
		dialog.ShowError(loadErr, w)
	}

	// 開視窗後對 gateway 做健康檢查，連不上就跳警示「RAG主機已關機」。
	// 等視窗進入前景後才做（dialog 需在 painter/字型就緒後彈出，中文才不會變豆腐）。
	var healthOnce sync.Once
	a.Lifecycle().SetOnEnteredForeground(func() {
		healthOnce.Do(func() {
			if st.Gateway == nil {
				return // 尚未設定 gateway 位址，不做檢查
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				defer cancel()
				if err := st.Gateway.HealthCheck(ctx); err != nil {
					dialog.ShowError(err, w) // err == services.ErrHostDown → "RAG主機已關機"
				}
			}()
		})
	})

	logger.Printf("mobile_client starting gateway_addr=%s agent_id=%s", cfg.GatewayAddr, cfg.AgentID)
	w.ShowAndRun()
}
