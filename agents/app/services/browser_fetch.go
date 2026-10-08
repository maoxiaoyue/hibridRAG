// browser_fetch.go — 透過遠端 headless 瀏覽器（browserless）抓取動態網頁純文字。
//
// 核心邏輯移植自獨立工具 black_browser（module orblackBrowser）：連 browserless 的
// WebSocket、導向目標頁、等 body 渲染完成、取 title + 純文字。web_browse 工具用它上網查資料。
package services

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// BrowserFetcher 針對單一 browserless 端點抓取網頁。
type BrowserFetcher struct {
	wsURL      string
	navTimeout time.Duration
}

// NewBrowserFetcher wsURL 例 "ws://localhost:33000"；navTimeoutSec<=0 → 45。
func NewBrowserFetcher(wsURL string, navTimeoutSec int) *BrowserFetcher {
	if navTimeoutSec <= 0 {
		navTimeoutSec = 45
	}
	return &BrowserFetcher{
		wsURL:      strings.TrimSpace(wsURL),
		navTimeout: time.Duration(navTimeoutSec) * time.Second,
	}
}

// FetchedPage 抓取結果。
type FetchedPage struct {
	URL      string
	Title    string
	BodyText string
}

// Fetch 導向 url、等 body 可見後取回 title 與純文字內容。
// 記 log 以便追蹤：起 fetch 時印 URL，結束時印耗時 + 抓到多少字節 or 錯誤。
// 靜默 fail 對 debug 太吃力，這裡選擇多幾行 log 換觀測性。
func (f *BrowserFetcher) Fetch(ctx context.Context, url string) (*FetchedPage, error) {
	if f.wsURL == "" {
		return nil, fmt.Errorf("browser ws_url not configured")
	}

	log.Printf("[web_browse] fetch start url=%s", url)
	started := time.Now()

	// NoModifyURL：直接用給定的 ws:// 位址，不去打 /json/version 改寫（browserless
	// 回的格式與 chromedp 預設不同，會報 "failed to modify wsURL"）。
	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(ctx, f.wsURL, chromedp.NoModifyURL)
	defer cancelAlloc()
	bctx, cancelB := chromedp.NewContext(allocCtx)
	defer cancelB()
	tctx, cancelT := context.WithTimeout(bctx, f.navTimeout)
	defer cancelT()

	var title, body string
	if err := chromedp.Run(tctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.Text(`body`, &body, chromedp.ByQuery),
	); err != nil {
		log.Printf("[web_browse] fetch fail url=%s elapsed=%s err=%v", url, time.Since(started), err)
		return nil, fmt.Errorf("browser fetch %s: %w", url, err)
	}
	log.Printf("[web_browse] fetch ok url=%s elapsed=%s title=%q body_bytes=%d",
		url, time.Since(started), title, len(body))
	return &FetchedPage{URL: url, Title: strings.TrimSpace(title), BodyText: strings.TrimSpace(body)}, nil
}
