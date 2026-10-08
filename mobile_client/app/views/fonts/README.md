# 內嵌 CJK 字型（建議）

把一個「純 TTF / OTF」中文字型放進這個資料夾（例如 `NotoSansTC-Regular.ttf`），
重新建置後 app 會自動內嵌並優先使用——這是 Android 上中文顯示最可靠的路徑。

為什麼需要：Fyne 內建字型不含中文字形，而 Android 系統的中文字型多半是
`.ttc` 字型集合（`/system/fonts/NotoSansCJK-Regular.ttc`），Fyne 的
go-text `ParseTTF` 解析不了 TTC。app 啟動時會依序嘗試：

1. 這個資料夾內嵌的 `.ttf` / `.otf`（build 時打進 APK）
2. `FYNE_FONT` 環境變數（桌面除錯用）
3. 系統字型候選清單（Android `/system/fonts/`、Windows `C:\Windows\Fonts\`）

每個候選都會先用 go-text 預解析驗證，解析失敗的直接跳過（不會讓 app 崩潰）。

字型下載：https://fonts.google.com/noto/specimen/Noto+Sans+TC （下載後取
static 版 `NotoSansTC-Regular.ttf`，約 4–8MB，APK 會相應變大）。
