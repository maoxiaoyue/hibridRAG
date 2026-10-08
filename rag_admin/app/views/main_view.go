// Package views 組裝 Fyne 桌面介面：對話 / 上傳 / 工具 / 設定 四個分頁。
package views

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"

	hypconfig "github.com/maoxiaoyue/hypgo/pkg/config"
	"github.com/maoxiaoyue/hypgo/pkg/logger"

	"rag_admin/app/models"
	"rag_admin/app/services"
)

// State client 全域共享狀態。
// dev_pub_0.9：三個 REST client 收斂成單一 Gateway（gRPC → agent_gateway）。
type State struct {
	App      fyne.App
	Win      fyne.Window
	Cfg      *models.Config
	Gateway  *services.GatewayClient    // nil = 未設定 GatewayAddr，各分頁顯示未設定提示
	GIClient *services.GraphInputClient // nil = 未設定 GraphInputBaseURL（外網版預設），Graph Input 分頁不建
	MDClient *services.ModelAdminClient // nil = 未設定 Registry/ModelAdmin URL（外網預設），Models 分頁不建
	Log      *logger.Logger

	deviceID string // 一 Key 一裝置的本機裝置指紋（啟動時解析一次）

	cfgListeners []func() // 設定變更通知（例：對話頁的 RAG ID 快速切換器要刷新選項）
}

// DeviceID 回傳本機裝置指紋（Test Connection 等需要臨時建 client 的地方用）。
func (s *State) DeviceID() string {
	if s.deviceID == "" {
		s.deviceID = resolveDeviceID(s.Cfg)
	}
	return s.deviceID
}

// OnCfgChanged 註冊設定變更 listener（分頁建構時呼叫，跨分頁同步 UI 狀態用）。
func (s *State) OnCfgChanged(fn func()) { s.cfgListeners = append(s.cfgListeners, fn) }

// NotifyCfgChanged 廣播設定已變更（設定頁 Save、或任何改動 Cfg 的地方之後呼叫）。
func (s *State) NotifyCfgChanged() {
	for _, fn := range s.cfgListeners {
		fn()
	}
}

// RebuildClient 依目前設定重建 gateway 連線（設定變更後呼叫）。
func (s *State) RebuildClient() {
	if s.deviceID == "" {
		s.deviceID = resolveDeviceID(s.Cfg)
	}
	if s.Gateway != nil {
		_ = s.Gateway.Close()
	}
	s.Gateway = services.NewGatewayClient(
		s.Cfg.GatewayAddr, s.Cfg.AgentID, s.Cfg.APIKey, s.deviceID, s.Cfg.InsecureTLS)
	s.GIClient = services.NewGraphInputClient(s.Cfg.GraphInputBaseURL, s.Cfg.InsecureTLS)
	// Model Admin（persona 設定，直連 agents 內網 REST）暫時停用——傳 "" → HasAgents=false，
	// Models 分頁只顯示模型目錄。要在內網啟用 persona 設定時再把 agents REST 位址傳回來。
	s.MDClient = services.NewModelAdminClient(s.Cfg.RegistryBaseURL, "", s.Cfg.InsecureTLS)
}

// resolveDeviceID 取「一 Key 一裝置」的裝置指紋：優先由機器特徵推導（穩定、
// 不落地）；取不到時退回隨機 id 並持久化到 config（換機視同新裝置）。
func resolveDeviceID(cfg *models.Config) string {
	if id, ok := services.StableDeviceID(); ok {
		return id
	}
	if cfg.DeviceID == "" {
		cfg.DeviceID = services.RandomDeviceID()
		_ = cfg.Save() // best-effort；存不進去頂多下次換 id（視同換機）
	}
	return cfg.DeviceID
}

// SaveConfig 持久化設定並回報錯誤。
func (s *State) SaveConfig() {
	if err := s.Cfg.Save(); err != nil {
		s.Log.Errorf("failed to save config: %v", err)
		dialog.ShowError(err, s.Win)
	}
}

// newAppLogger 依 app/config/config.yaml 的 logger 區段建立 *logger.Logger；
// 設定檔缺失或載入失敗時退回內建預設（不阻擋啟動）。
func newAppLogger() *logger.Logger {
	appCfg, err := hypconfig.LoadConfig("app/config/config.yaml")
	if err != nil {
		appCfg = &hypconfig.Config{}
	}
	appCfg.ApplyDefaults()

	log, err := logger.New(appCfg.Logger.GetLevel(), appCfg.Logger.GetOutput(), nil, appCfg.Logger.IsColorized())
	if err != nil {
		return logger.NewLogger()
	}
	if out := appCfg.Logger.GetOutput(); out != "" && out != "stdout" && out != "stderr" {
		_ = log.SetFile(out)
	}
	return log
}

// Run 啟動 GUI（阻塞直到視窗關閉）。
func Run() {
	log := newAppLogger()
	defer log.Close()

	services.MigrateLegacySessions() // 一次性：舊 agent_client\sessions → rag_admin
	cfg, loadErr := models.LoadConfig()
	if loadErr != nil {
		log.Warnf("failed to load user config, using defaults: %v", loadErr)
	}

	a := app.New()
	// 套用主題：CJK 字型（否則中文顯示成 □/�）+ 使用者選的深/淺色模式。
	applyTheme(a, cfg.ThemeMode)
	if cachedCJKFont() == nil {
		log.Warnf("no CJK font found on this machine; 中文可能無法正常顯示")
	}
	w := a.NewWindow("RAG Admin")

	st := &State{App: a, Win: w, Cfg: cfg, Log: log}
	st.RebuildClient()

	tabItems := []*container.TabItem{
		container.NewTabItem("Chat", chatTab(st)),
		container.NewTabItem("Upload", uploadTab(st)),
		container.NewTabItem("Tools", toolsTab(st)),
		container.NewTabItem("Knowledge Graph", graphTab(st)),
	}
	// Graph Input 分頁只在內網（dev_090）設定了 GraphInputBaseURL 時建立——
	// 外網版 client 連分頁都看不到（不只是停用）。設定後需重啟生效。
	if st.GIClient != nil {
		tabItems = append(tabItems, container.NewTabItem("Graph Input", graphInputTab(st)))
	}
	// Models 分頁只在內網（設了 Registry/ModelAdmin URL）時建立；外網版預設不建。
	if st.MDClient != nil {
		tabItems = append(tabItems, container.NewTabItem("Models", modelsTab(st)))
	}
	tabItems = append(tabItems, container.NewTabItem("Settings", settingsTab(st)))
	tabs := container.NewAppTabs(tabItems...)
	tabs.SetTabLocation(container.TabLocationTop)

	w.SetContent(tabs)
	w.Resize(fyne.NewSize(940, 700))

	if loadErr != nil {
		dialog.ShowError(loadErr, w)
	}
	log.Info("agent_client starting", "gateway_addr", cfg.GatewayAddr, "agent_id", cfg.AgentID)
	w.ShowAndRun()
}
