package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// Result 是最終輸出的 JSON 結構
type Result struct {
	URL       string      `json:"url"`
	Title     string      `json:"title"`
	BodyText  string      `json:"bodyText"`
	HTML      string      `json:"html"`
	ChartData interface{} `json:"chartData,omitempty"`
	Metadata  Metadata    `json:"metadata"`
	Snapshots []Snapshot  `json:"snapshots"`
}

// Metadata 記錄抓取的後設資料
type Metadata struct {
	FetchedAt       string `json:"fetchedAt"`
	DurationSeconds int    `json:"durationSeconds"`
	IntervalSeconds int    `json:"intervalSeconds"`
	SnapshotCount   int    `json:"snapshotCount"`
}

// Snapshot 記錄每次快照的時間與內容
type Snapshot struct {
	Timestamp string      `json:"timestamp"`
	BodyText  string      `json:"bodyText"`
	ChartData interface{} `json:"chartData,omitempty"`
}

func main() {
	// 1. 定義並解析命令列參數
	urlPtr := flag.String("url", "", "要瀏覽的目標網址 (必填)")
	wsURLPtr := flag.String("ws-url", "", "browserless WebSocket 位址 (env BROWSERLESS_URL；預設 ws://localhost:33000)")
	durationPtr := flag.Int("duration", 0, "動態內容監聽秒數 (預設 0，載入完成後立刻抓取)")
	intervalPtr := flag.Int("interval", 1, "快照間隔秒數 (預設 1)")
	jsPtr := flag.String("js", "", "在頁面載入後執行的 JavaScript 表達式，回傳值會存入 chartData")
	jsFilePtr := flag.String("js-file", "", "包含 JavaScript 腳本的檔案路徑，效果同 --js")
	jsWaitPtr := flag.Int("js-wait", 0, "執行 JS 前的額外等待毫秒數，讓圖表庫有時間初始化 (預設 0)")
	jsEachSnapshotPtr := flag.Bool("js-each-snapshot", false, "是否在每次快照時也執行 JS (預設 false，僅最終抓取時執行)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `orblackBrowser — 透過遠端 Chrome (browserless) 抓取動態網頁並提取 JS 圖表數據

用法:
  orblackBrowser -url <網址> [選項]

必要參數:
  -url <網址>
        要瀏覽的目標網址

基本選項:
  -duration <秒>
        動態內容監聽秒數；0 表示載入完成後立刻抓取 (預設: 0)
  -interval <秒>
        動態監聽時的快照間隔秒數 (預設: 1)

JS 數據提取選項:
  -js <JS 表達式>
        頁面載入後執行的 JavaScript 表達式。
        回傳值（任意型別）會序列化為 JSON 存入輸出的 chartData 欄位。
        範例: -js "({price: window.chart.data.last})"
  -js-file <檔案路徑>
        從本地檔案讀取 JS 腳本，效果同 -js（兩者擇一使用）
  -js-wait <毫秒>
        執行 JS 前的額外等待時間（毫秒），讓圖表庫有時間完成初始化 (預設: 0)
  -js-each-snapshot
        在每次動態快照時也執行 JS，結果存入各快照的 chartData 欄位 (預設: false)

其他:
  -h, --help
        顯示此說明

輸出格式 (JSON, stdout):
  {
    "url":       "目標網址",
    "title":     "頁面標題",
    "bodyText":  "純文字內容",
    "html":      "完整 outerHTML",
    "chartData": { ... },   ← JS 回傳值（僅當 -js / -js-file 有提供時出現）
    "metadata":  { "fetchedAt", "durationSeconds", "intervalSeconds", "snapshotCount" },
    "snapshots": [ { "timestamp", "bodyText", "chartData" }, ... ]
  }

範例:
  # 基本抓取
  orblackBrowser -url https://example.com

  # 動態監聽 30 秒，每 5 秒快照
  orblackBrowser -url https://example.com -duration 30 -interval 5

  # 提取 ECharts 圖表第一組 series 數據（等待 2 秒讓圖初始化）
  orblackBrowser -url https://example.com/chart -js-wait 2000 \
    -js "(function(){ var c = Object.values(echarts.instances)[0]; return c ? c.getOption().series : null; }())"

  # 從腳本檔案讀取 JS，並在每次快照時執行
  orblackBrowser -url https://example.com -duration 60 -interval 10 \
    -js-file ./extract.js -js-each-snapshot

`)
	}

	flag.Parse()

	// 檢查是否確實傳入了網址
	if *urlPtr == "" {
		log.Fatal("錯誤：請提供目標網址。使用範例: ./orblackBrowser -url https://www.example.com")
	}

	targetURL := *urlPtr
	duration := *durationPtr
	interval := *intervalPtr

	if interval <= 0 {
		interval = 1
	}

	// 解析 JS 腳本
	jsScript, err := resolveJS(*jsPtr, *jsFilePtr)
	if err != nil {
		log.Fatalf("JS 腳本載入失敗: %v", err)
	}

	// wsURL 解析順序：--ws-url > env BROWSERLESS_URL > 預設 localhost:33000。
	// 這樣 CI／部署腳本可以走 env、開發者臨時測試可以走 --ws-url、
	// 兩者都沒設時退回內建預設維持舊行為。
	wsURL := strings.TrimSpace(*wsURLPtr)
	if wsURL == "" {
		wsURL = strings.TrimSpace(os.Getenv("BROWSERLESS_URL"))
	}
	if wsURL == "" {
		wsURL = "ws://localhost:33000"
	}

	// 2. 建立遠端分配器。NoModifyURL：browserless 的 /json/version 回傳格式和
	// chromedp 預設 rewrite 邏輯不合（會噴 "failed to modify wsURL"），
	// 用給定 ws:// 直接 dial。
	allocCtx, cancel := chromedp.NewRemoteAllocator(context.Background(), wsURL, chromedp.NoModifyURL)
	defer cancel()

	// 3. 建立 chromedp 上下文，將 chromedp 自身的 log 導向 stderr
	ctx, cancel := chromedp.NewContext(allocCtx,
		chromedp.WithErrorf(func(s string, args ...interface{}) {
			fmt.Fprintf(os.Stderr, "[chromedp] "+s+"\n", args...)
		}),
	)
	defer cancel()

	// 4. 設定整體任務的超時時間 = duration + 120 秒緩衝
	totalTimeout := time.Duration(duration+120) * time.Second
	ctx, cancel = context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	fmt.Fprintf(os.Stderr, "準備連線至遠端瀏覽器，目標網址: %s\n", targetURL)

	// 5. 導向目標網址並等待 body 渲染完成
	if err := chromedp.Run(ctx,
		chromedp.Navigate(targetURL),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
	); err != nil {
		log.Fatalf("頁面載入失敗: %v", err)
	}

	fmt.Fprintf(os.Stderr, "頁面載入完成\n")

	// 6. 動態監聽迴圈：持續 n 秒，每隔 interval 秒做一次快照
	var snapshots []Snapshot

	if duration > 0 {
		fmt.Fprintf(os.Stderr, "開始動態監聽 %d 秒 (每 %d 秒快照)...\n", duration, interval)

		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()

		deadline := time.After(time.Duration(duration) * time.Second)
		snapshotIdx := 0

	loop:
		for {
			select {
			case <-deadline:
				break loop
			case <-ticker.C:
				snapshotIdx++
				var text string
				if err := chromedp.Run(ctx,
					chromedp.Text(`body`, &text, chromedp.ByQuery),
				); err != nil {
					fmt.Fprintf(os.Stderr, "快照 #%d 失敗: %v\n", snapshotIdx, err)
					continue
				}
				snap := Snapshot{
					Timestamp: time.Now().UTC().Format(time.RFC3339),
					BodyText:  text,
				}

				// 若啟用 js-each-snapshot 且有 JS 腳本，也在每次快照時執行
				if *jsEachSnapshotPtr && jsScript != "" {
					snapChartData, jsErr := runJS(ctx, jsScript, 0)
					if jsErr != nil {
						fmt.Fprintf(os.Stderr, "快照 #%d JS 執行失敗: %v\n", snapshotIdx, jsErr)
					} else {
						snap.ChartData = snapChartData
					}
				}

				snapshots = append(snapshots, snap)
				fmt.Fprintf(os.Stderr, "快照 #%d 完成 (%d bytes)\n", snapshotIdx, len(text))
			case <-ctx.Done():
				fmt.Fprintf(os.Stderr, "上下文已取消，停止監聽\n")
				break loop
			}
		}
	}

	// 7. 最終抓取：取得完整的 title、body text、outerHTML
	var pageTitle string
	var finalBodyText string
	var fullHTML string

	if err := chromedp.Run(ctx,
		chromedp.Title(&pageTitle),
		chromedp.Text(`body`, &finalBodyText, chromedp.ByQuery),
		chromedp.OuterHTML(`html`, &fullHTML, chromedp.ByQuery),
	); err != nil {
		log.Fatalf("最終抓取失敗: %v", err)
	}

	fmt.Fprintf(os.Stderr, "最終抓取完成\n")

	// 8. 執行自定義 JS 腳本（若有提供）
	var chartData interface{}
	if jsScript != "" {
		fmt.Fprintf(os.Stderr, "執行 JS 腳本提取圖表數據...\n")
		chartData, err = runJS(ctx, jsScript, *jsWaitPtr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "JS 執行失敗: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "JS 執行完成\n")
		}
	}

	// 9. 組裝 JSON 結果並輸出到 stdout
	result := Result{
		URL:       targetURL,
		Title:     pageTitle,
		BodyText:  finalBodyText,
		HTML:      fullHTML,
		ChartData: chartData,
		Metadata: Metadata{
			FetchedAt:       time.Now().UTC().Format(time.RFC3339),
			DurationSeconds: duration,
			IntervalSeconds: interval,
			SnapshotCount:   len(snapshots),
		},
		Snapshots: snapshots,
	}

	// 確保 snapshots 不是 null
	if result.Snapshots == nil {
		result.Snapshots = []Snapshot{}
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		log.Fatalf("JSON 序列化失敗: %v", err)
	}
}

// resolveJS 從 --js 或 --js-file 取得 JS 腳本字串
func resolveJS(jsInline, jsFile string) (string, error) {
	if jsFile != "" {
		data, err := os.ReadFile(jsFile)
		if err != nil {
			return "", fmt.Errorf("無法讀取 JS 檔案 %q: %w", jsFile, err)
		}
		return string(data), nil
	}
	return jsInline, nil
}

// runJS 在 chromedp 上下文中執行 JS，等待 waitMs 毫秒後執行，回傳 interface{}
func runJS(ctx context.Context, script string, waitMs int) (interface{}, error) {
	if waitMs > 0 {
		select {
		case <-time.After(time.Duration(waitMs) * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	var raw []byte
	// 將 JS 表達式包裝：先 JSON.stringify 結果，再回傳字串，避免 chromedp 的型別轉換限制
	wrappedScript := fmt.Sprintf(`(function(){ var r = (%s); return JSON.stringify(r); })()`, script)

	var resultStr string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(wrappedScript, &resultStr),
	); err != nil {
		// fallback：直接執行不包裝
		if err2 := chromedp.Run(ctx,
			chromedp.Evaluate(script, &raw),
		); err2 != nil {
			return nil, fmt.Errorf("JS 執行失敗: %w", err)
		}
		var out interface{}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return out, nil
	}

	// 解析 JSON 字串
	var out interface{}
	if err := json.Unmarshal([]byte(resultStr), &out); err != nil {
		// 若不是合法 JSON（例如純字串），直接回傳原始字串
		return resultStr, nil
	}
	return out, nil
}
