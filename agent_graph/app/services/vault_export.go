// vault_export.go — 圖譜 → Obsidian vault 的 .md 鏡像匯出。
//
// 每個 Entity 節點對應 vault 內 <export_dir>/<sanitized-name>.md 一個檔：
// frontmatter（category/tags/source/aliases）+ 描述 + 出向關係的 [[wikilink]] 清單。
// 在 Obsidian 開這個 vault 時，graph view 看到的節點/邊會與 Kùzu 圖譜一致
//（wikilink = 邊；aliases 讓被 sanitize 過的檔名仍可用原名連結）。
//
// 同步語意（「每次建立，檢查最近的更動並同步」）：
//   - 每個圖譜寫入操作（facade 的 Upsert*/ApproveProposed/IngestText 與 sync
//     worker 的 SyncFile）成功後 Notify 受影響的實體
//   - 背景迴圈 debounce 之後從「當下圖譜狀態」重新 render 受影響的檔——render
//     是確定性的，內容沒變就不落盤（增量同步）
//   - 啟動時 NotifyAll 做一次全量 reconcile：render 全部實體 + 刪除圖譜已無
//     對應實體的殘留 .md（export 目錄整個視為生成物，刪除是安全的）
//
// 防迴圈：export 目錄由 internal/sync 的 vault watcher/掃描明確排除，
// 鏡像檔永遠不會被當成筆記重新攝取。
package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	gosync "sync"
	"time"

	"github.com/maoxiaoyue/hypgo/pkg/logger"

	"agent_graph/app/models"
)

// exportDebounce 寫入通知到實際匯出的合併等待——連續多筆寫入（IngestText 一次
// 抽十幾個實體）只觸發一次匯出。
const exportDebounce = 2 * time.Second

// reconcileKey pending set 內的特殊鍵：全量 reconcile（含清 stale 檔）。
const reconcileKey = "\x00*"

type VaultExporter struct {
	graph GraphBackend
	root  string // vault 根目錄
	dir   string // 匯出子目錄名（相對 root）
	log   *logger.Logger

	mu      gosync.Mutex
	pending map[string]bool
	wake    chan struct{}
}

func NewVaultExporter(graph GraphBackend, vaultRoot, exportDir string, log *logger.Logger) *VaultExporter {
	return &VaultExporter{
		graph:   graph,
		root:    vaultRoot,
		dir:     exportDir,
		log:     log,
		pending: make(map[string]bool),
		wake:    make(chan struct{}, 1),
	}
}

// ExportPath 匯出目錄絕對路徑（<vault>/<export_dir>）。
func (e *VaultExporter) ExportPath() string { return filepath.Join(e.root, e.dir) }

// Notify 標記實體待匯出。非阻塞；空白名稱忽略。
func (e *VaultExporter) Notify(names ...string) {
	e.mu.Lock()
	added := false
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			continue
		}
		e.pending[n] = true
		added = true
	}
	e.mu.Unlock()
	if added {
		e.signal()
	}
}

// NotifyAll 標記一次全量 reconcile（render 全部 + 刪 stale 檔）。
func (e *VaultExporter) NotifyAll() {
	e.mu.Lock()
	e.pending[reconcileKey] = true
	e.mu.Unlock()
	e.signal()
}

func (e *VaultExporter) signal() {
	select {
	case e.wake <- struct{}{}:
	default: // 已有待處理訊號
	}
}

// Run 背景匯出迴圈；ctx 取消即退出。呼叫端負責用 goroutine 啟動。
func (e *VaultExporter) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.wake:
		}
		// debounce：收攏短時間內的連續寫入
		select {
		case <-ctx.Done():
			return
		case <-time.After(exportDebounce):
		}

		e.mu.Lock()
		batch := e.pending
		e.pending = make(map[string]bool)
		e.mu.Unlock()
		if len(batch) == 0 {
			continue
		}

		full := batch[reconcileKey]
		delete(batch, reconcileKey)
		var only map[string]bool
		if !full {
			only = batch
		}
		if err := e.export(ctx, only, full); err != nil {
			e.log.Warningf("vault export: %v", err)
		}
	}
}

// export 從當下圖譜狀態 render 鏡像檔。only 非 nil = 只處理這些實體；
// full = render 全部並刪除 stale 檔。內容未變的檔不重寫（增量同步）。
func (e *VaultExporter) export(ctx context.Context, only map[string]bool, full bool) error {
	if e.graph == nil || !e.graph.Enabled() {
		return nil
	}
	entities, err := e.graph.ListEntities(ctx)
	if err != nil {
		return fmt.Errorf("list entities: %w", err)
	}
	relations, err := e.graph.ListRelations(ctx)
	if err != nil {
		return fmt.Errorf("list relations: %w", err)
	}

	outgoing := make(map[string][]models.RelationResult, len(entities))
	for _, r := range relations {
		outgoing[r.Source] = append(outgoing[r.Source], r)
	}

	exportDir := e.ExportPath()
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return fmt.Errorf("create export dir: %w", err)
	}

	written, unchanged := 0, 0
	expected := make(map[string]bool, len(entities))
	for _, ent := range entities {
		fileName := entityFileBase(ent.Name) + ".md"
		expected[fileName] = true
		if only != nil && !only[ent.Name] {
			continue
		}
		content := renderEntityNote(ent, outgoing[ent.Name])
		path := filepath.Join(exportDir, fileName)
		old, rerr := os.ReadFile(path)
		if rerr == nil && bytes.Equal(old, content) {
			unchanged++
			continue
		}
		if werr := os.WriteFile(path, content, 0o644); werr != nil {
			e.log.Warningf("vault export write %s: %v", path, werr)
			continue
		}
		written++
	}

	removed := 0
	if full {
		removed = e.removeStale(exportDir, expected)
	}
	if written > 0 || removed > 0 {
		e.log.Infof("vault export: %d written, %d unchanged, %d stale removed (%s)",
			written, unchanged, removed, exportDir)
	}
	return nil
}

// removeStale 刪除 export 目錄內圖譜已無對應實體的 .md。只碰第一層
//（exporter 從不寫子目錄）且只碰 .md——目錄整個是生成物，這仍是保守範圍。
func (e *VaultExporter) removeStale(exportDir string, expected map[string]bool) int {
	entries, err := os.ReadDir(exportDir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, ent := range entries {
		if ent.IsDir() || !strings.EqualFold(filepath.Ext(ent.Name()), ".md") || expected[ent.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(exportDir, ent.Name())); err != nil {
			e.log.Warningf("vault export remove stale %s: %v", ent.Name(), err)
			continue
		}
		removed++
	}
	return removed
}

// renderEntityNote 把一個實體 render 成 Obsidian 筆記。輸出是確定性的
//（關係排序、無時間戳），同一圖譜狀態永遠 render 出同一 bytes——增量比對靠這個。
func renderEntityNote(ent models.EntityResult, outgoing []models.RelationResult) []byte {
	var b strings.Builder
	fileBase := entityFileBase(ent.Name)

	b.WriteString("---\n")
	if fileBase != ent.Name {
		// 檔名被 sanitize 過：aliases 讓 Obsidian 內 [[原名]] 仍解析到這個檔
		fmt.Fprintf(&b, "aliases: [%s]\n", yamlQuote(ent.Name))
	}
	if ent.Category != "" {
		fmt.Fprintf(&b, "category: %s\n", yamlQuote(ent.Category))
		fmt.Fprintf(&b, "tags: [%s]\n", yamlQuote("kb/"+tagSafe(ent.Category)))
	}
	if ent.SourceFile != "" {
		fmt.Fprintf(&b, "source: %s\n", yamlQuote(ent.SourceFile))
	}
	b.WriteString("---\n")

	if ent.Description != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(ent.Description))
		b.WriteString("\n")
	}

	if len(outgoing) > 0 {
		sort.Slice(outgoing, func(i, j int) bool {
			if outgoing[i].Target != outgoing[j].Target {
				return outgoing[i].Target < outgoing[j].Target
			}
			return outgoing[i].Type < outgoing[j].Type
		})
		b.WriteString("\n## Relations\n\n")
		for _, r := range outgoing {
			targetBase := entityFileBase(r.Target)
			link := "[[" + targetBase + "]]"
			if targetBase != r.Target {
				link = "[[" + targetBase + "|" + r.Target + "]]"
			}
			if r.Type != "" {
				fmt.Fprintf(&b, "- %s（%s）\n", link, r.Type)
			} else {
				fmt.Fprintf(&b, "- %s\n", link)
			}
		}
	}
	return []byte(b.String())
}

// entityFileBase 把實體名轉成合法且 wikilink 可用的檔名（不含副檔名）。
// Windows 保留字元與 Obsidian wikilink 語法字元（#^[]|）換成 _；被改動過的
// 名稱附上原名 hash 後綴，避免 "a/b" 與 "a:b" sanitize 後互撞。
func entityFileBase(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*', '#', '^', '[', ']':
			return '_'
		}
		if r < 0x20 {
			return '_'
		}
		return r
	}, name)
	cleaned = strings.Trim(cleaned, " .")
	if rs := []rune(cleaned); len(rs) > 80 {
		cleaned = string(rs[:80])
	}
	if cleaned == "" {
		cleaned = "untitled"
	}
	if cleaned != name {
		sum := sha256.Sum256([]byte(name))
		cleaned += "-" + hex.EncodeToString(sum[:4])
	}
	return cleaned
}

// yamlQuote 雙引號包裹 + 跳脫，讓任意字串安全進 YAML frontmatter。
func yamlQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return `"` + s + `"`
}

// tagSafe 把 category 轉成 Obsidian tag 可用字元（空白→_；# 開頭去掉）。
func tagSafe(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '#', ',', ';':
			return '_'
		}
		return r
	}, s)
}

// walkMD 列出 root 下所有 .md 相對路徑（forward slash），跳過 dot 開頭目錄
//（.obsidian/.trash/.git）。VaultFS 與測試共用。
func walkMD(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 單檔錯誤跳過
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}
