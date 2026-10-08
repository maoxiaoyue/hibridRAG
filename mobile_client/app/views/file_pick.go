// file_pick.go — 檔案選擇（Fyne dialog）。
//
// Android 上選檔走 SAF（Storage Access Framework），拿到的是 content:// URI
// 而非檔案路徑——內容必須在選檔當下經 reader 讀進記憶體，之後所有流程
// （附圖辨識 / 上傳攝取）都以 (name, data) 傳遞，不碰 os.Open。
package views

import (
	"fmt"
	"io"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	fynestorage "fyne.io/fyne/v2/storage"
)

// maxPickBytes 單檔上限（對齊 gateway UploadIngest 的 64MB 上限）。
const maxPickBytes = 64 << 20

// pickFile 開檔案選擇器，成功時以 (檔名, 內容) 呼叫 cb（在 UI callback goroutine）。
// 取消選擇時不呼叫 cb。exts 例 []string{".png", ".jpg"}；空 = 不過濾。
func pickFile(st *State, exts []string, cb func(name string, data []byte)) {
	fd := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, st.Win)
			return
		}
		if rc == nil {
			return // 使用者取消
		}
		defer rc.Close()
		data, rerr := io.ReadAll(io.LimitReader(rc, maxPickBytes+1))
		if rerr != nil {
			dialog.ShowError(fmt.Errorf("read %s: %w", rc.URI().Name(), rerr), st.Win)
			return
		}
		if len(data) > maxPickBytes {
			dialog.ShowError(fmt.Errorf("%s exceeds the %d MB limit", rc.URI().Name(), maxPickBytes>>20), st.Win)
			return
		}
		cb(rc.URI().Name(), data)
	}, st.Win)
	if len(exts) > 0 {
		fd.SetFilter(fynestorage.NewExtensionFileFilter(exts))
	}
	fd.Show()
}
