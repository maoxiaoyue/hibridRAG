// image_attach.go — 對話附件圖片的縮圖 cell（左上角 X 取消、點縮圖看原尺寸）。
// 手機版附件是 in-memory (name, data)——Android SAF 選檔沒有檔案路徑。
package views

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const attachThumbSize = 64

// attachment 一張待送出的附加圖片。
type attachment struct {
	name string
	data []byte
}

// tappableImage 可點擊的縮圖（canvas.Image 本身不接受點擊事件）。
type tappableImage struct {
	widget.BaseWidget
	img   *canvas.Image
	onTap func()
}

func newTappableImage(res fyne.Resource, onTap func()) *tappableImage {
	img := canvas.NewImageFromResource(res)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(attachThumbSize, attachThumbSize))
	t := &tappableImage{img: img, onTap: onTap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tappableImage) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(t.img) }

func (t *tappableImage) Tapped(_ *fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

// buildAttachThumb 一個附件縮圖 cell：點縮圖看原尺寸；左上角 X 取消附加。
func buildAttachThumb(win fyne.Window, att attachment, onRemove func()) fyne.CanvasObject {
	res := fyne.NewStaticResource(att.name, att.data)
	thumb := newTappableImage(res, func() { showFullImage(win, att.name, res) })

	xBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), onRemove)
	xBtn.Importance = widget.LowImportance
	// X 疊在縮圖左上角（HBox+VBox spacer 把它推到角落；非 tappable 的 spacer 不擋縮圖點擊）。
	overlay := container.NewVBox(container.NewHBox(xBtn, layout.NewSpacer()), layout.NewSpacer())

	return container.NewStack(thumb, overlay)
}

// showFullImage 放大檢視圖片（以視窗大小為上限，超出時捲動）。
func showFullImage(win fyne.Window, name string, res fyne.Resource) {
	img := canvas.NewImageFromResource(res)
	img.FillMode = canvas.ImageFillOriginal // 用原圖尺寸當 min size
	d := dialog.NewCustom(name, "Close", container.NewScroll(img), win)
	sz := win.Canvas().Size()
	d.Resize(fyne.NewSize(sz.Width*0.95, sz.Height*0.9)) // 上限；原圖更大時用捲動
	d.Show()
}
