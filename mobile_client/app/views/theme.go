package views

import (
	"bytes"
	"embed"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	gotextfont "github.com/go-text/typesetting/font"
)

// embeddedFonts 建置時內嵌的 CJK 字型（把 .ttf/.otf 丟進 app/views/fonts/ 即生效；
// 見該資料夾 README）。Android 上這是中文顯示最可靠的路徑——系統字型多為
// Fyne 解析不了的 .ttc 集合。
//
//go:embed fonts
var embeddedFonts embed.FS

// cjkTheme 包住 Fyne 內建主題，把「非等寬、非符號」的文字字型換成含 CJK 字形的字型。
type cjkTheme struct {
	fyne.Theme
	cjk fyne.Resource
}

// Font 對一般/粗體/斜體文字回傳 CJK 字型（確保中文一定有字形）；等寬與符號維持內建，
// 以保留程式碼區塊的等寬與內建 icon 字型。
func (t *cjkTheme) Font(s fyne.TextStyle) fyne.Resource {
	if s.Monospace || s.Symbol {
		return t.Theme.Font(s)
	}
	return t.cjk
}

// cjkFontCandidates 依序嘗試的系統中文字型（內嵌字型與 FYNE_FONT 優先於此清單）。
// 只有能被 go-text 解析的才會被採用（parseableFont 預檢），.ttc 會被自動跳過。
var cjkFontCandidates = []string{
	// Android
	`/system/fonts/NotoSansTC-Regular.otf`,
	`/system/fonts/NotoSansSC-Regular.otf`,
	`/system/fonts/NotoSansCJKtc-Regular.otf`,
	`/system/fonts/DroidSansFallback.ttf`,
	`/system/fonts/DroidSansFallbackFull.ttf`,
	`/system/fonts/NotoSansCJK-Regular.ttc`, // 多半解析失敗被跳過，姑且一試
	// Windows（桌面開發驗證用）
	`C:\Windows\Fonts\NotoSansTC-VF.ttf`,
	`C:\Windows\Fonts\simhei.ttf`,
	`C:\Windows\Fonts\kaiu.ttf`,
}

// parseableFont 用 Fyne 同版的 go-text 預解析字型位元組；解析不了（例如 .ttc）
// 回 false——避免把 Fyne 渲染層吃不下的字型設成主題字型。
func parseableFont(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	_, err := gotextfont.ParseTTF(bytes.NewReader(b))
	return err == nil
}

// loadCJKFont 回傳第一個可用的中文字型資源；全都找不到時回 nil（維持 Fyne 內建行為，
// 英文正常、中文顯示 □）。優先序：內嵌 fonts/ → FYNE_FONT → 系統候選清單。
func loadCJKFont() fyne.Resource {
	// 1. 內嵌字型
	if entries, err := embeddedFonts.ReadDir("fonts"); err == nil {
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			if !strings.HasSuffix(name, ".ttf") && !strings.HasSuffix(name, ".otf") {
				continue
			}
			if b, rerr := embeddedFonts.ReadFile("fonts/" + e.Name()); rerr == nil && parseableFont(b) {
				return fyne.NewStaticResource(e.Name(), b)
			}
		}
	}
	// 2. FYNE_FONT 環境變數 + 3. 系統候選
	paths := cjkFontCandidates
	if env := os.Getenv("FYNE_FONT"); env != "" {
		paths = append([]string{env}, paths...)
	}
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && parseableFont(b) {
			return fyne.NewStaticResource(filepath.Base(p), b)
		}
	}
	return nil
}

// applyCJKFont 若找得到中文字型就套用 cjkTheme，回傳是否成功套用。
func applyCJKFont(a fyne.App) bool {
	f := loadCJKFont()
	if f == nil {
		return false
	}
	a.Settings().SetTheme(&cjkTheme{Theme: theme.DefaultTheme(), cjk: f})
	return true
}
