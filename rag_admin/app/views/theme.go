package views

import (
	"image/color"
	"os"
	"path/filepath"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// 主題模式（存進 Config.ThemeMode；空字串視同 ThemeSystem）。
const (
	ThemeSystem = "system" // 跟隨作業系統的深/淺色偏好
	ThemeDark   = "dark"
	ThemeLight  = "light"
)

// cjkTheme 包住 Fyne 內建主題，做三件事：
//  1. 把「非等寬、非符號」文字字型換成含 CJK 字形的系統字型（否則中文顯示成 □/�）；
//  2. 縮小文字字級到接近 Claude Desktop 的觀感；
//  3. （可選）強制深色或淺色 variant，無視作業系統設定——ThemeSystem 時不強制。
//
// 為什麼字型換法這樣：Fyne v2.4.5 內建字型不含中文字形，且其文字渲染走 go-text 的
// font.ParseTTF——ParseTTF 不支援 .ttc 字型集合（msjh.ttc/msyh.ttc/mingliu.ttc 皆解析失敗）。
// 這裡載入系統的「純 TTF」中文字型即可解決。
type cjkTheme struct {
	fyne.Theme
	cjk fyne.Resource // nil = 這台機器找不到中文字型，維持內建字型

	forceVariant bool
	variant      fyne.ThemeVariant
}

// Font 對一般/粗體/斜體文字回傳 CJK 字型（確保中文一定有字形）；等寬與符號維持內建，
// 以保留程式碼區塊的等寬與內建 icon 字型。cjk 為 nil 時全部走內建。
func (t *cjkTheme) Font(s fyne.TextStyle) fyne.Resource {
	if t.cjk == nil || s.Monospace || s.Symbol {
		return t.Theme.Font(s)
	}
	return t.cjk
}

// Color 在 forceVariant 時無視傳入的 variant，改用固定值——這是「強制深/淺色」的關鍵：
// Fyne 的 theme.BackgroundColor() 等 helper 會帶著「目前作業系統 variant」呼叫進來，
// 我們在這裡覆寫成固定 variant，深/淺色就整個平台一致（含 graphWidget 的手繪顏色）。
func (t *cjkTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if t.forceVariant {
		v = t.variant
	}
	return t.Theme.Color(name, v)
}

// textSizeScale 把文字字級縮小到接近 Claude Desktop 的觀感（Fyne 預設 14 偏大）。
const textSizeScale = 0.85

// Size 只縮小「文字類」尺寸（不動 padding / icon / scrollbar，避免版面過擠）。
func (t *cjkTheme) Size(name fyne.ThemeSizeName) float32 {
	base := t.Theme.Size(name)
	switch name {
	case theme.SizeNameText, theme.SizeNameCaptionText,
		theme.SizeNameHeadingText, theme.SizeNameSubHeadingText:
		return base * textSizeScale
	}
	return base
}

// cjkFontCandidates 依序嘗試的系統中文字型。只列「純 TTF」——Fyne 的 ParseTTF 不吃 .ttc，
// 故 msjh.ttc / msyh.ttc / mingliu.ttc 一律不放。
var cjkFontCandidates = []string{
	`C:\Windows\Fonts\NotoSansTC-VF.ttf`, // Noto Sans 繁中（sans UI，最佳觀感）
	`C:\Windows\Fonts\simhei.ttf`,        // 黑體（涵蓋 CJK，各版 Windows 幾乎都有）
	`C:\Windows\Fonts\kaiu.ttf`,          // 標楷體（繁中，最後備援）
}

// loadCJKFont 回傳第一個可讀取的中文字型資源；全都找不到時回 nil（維持 Fyne 內建行為）。
// 可用環境變數 FYNE_FONT 覆寫（須指向一個 TTF，非 TTC）。
func loadCJKFont() fyne.Resource {
	paths := cjkFontCandidates
	if env := os.Getenv("FYNE_FONT"); env != "" {
		paths = append([]string{env}, paths...)
	}
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			return fyne.NewStaticResource(filepath.Base(p), b)
		}
	}
	return nil
}

// 字型只需在啟動時讀一次；切換主題時重建 theme 就不用再讀檔。
var (
	cjkFontOnce sync.Once
	cjkFontRes  fyne.Resource
)

func cachedCJKFont() fyne.Resource {
	cjkFontOnce.Do(func() { cjkFontRes = loadCJKFont() })
	return cjkFontRes
}

// applyTheme 依 mode（ThemeSystem/ThemeDark/ThemeLight）重建並套用 app 主題。
// 啟動時與設定頁切換主題時都呼叫這支；立即生效（Fyne 會重繪）。
func applyTheme(a fyne.App, mode string) {
	ct := &cjkTheme{Theme: theme.DefaultTheme(), cjk: cachedCJKFont()}
	switch mode {
	case ThemeDark:
		ct.forceVariant, ct.variant = true, theme.VariantDark
	case ThemeLight:
		ct.forceVariant, ct.variant = true, theme.VariantLight
	}
	a.Settings().SetTheme(ct)
}

// themeModeLabels UI 下拉選項 ↔ Config 值 對照。
var themeModeLabels = []string{"Follow System", "Dark", "Light"}

func themeModeToLabel(mode string) string {
	switch mode {
	case ThemeDark:
		return "Dark"
	case ThemeLight:
		return "Light"
	default:
		return "Follow System"
	}
}

func labelToThemeMode(label string) string {
	switch label {
	case "Dark":
		return ThemeDark
	case "Light":
		return ThemeLight
	default:
		return ThemeSystem
	}
}
