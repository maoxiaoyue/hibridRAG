// temp_digest.go — 知識沉澱管線：對話/上傳先落 vault/temp/，背景 worker 閒置時
// 用 LLM 分類搬進 vault/<category>/（搬出 temp 後由 sync worker 攝取進圖譜），
// 每日整理時把過大的分類再切割成子分類。
//
// 為什麼放 agent_graph：它擁有 vault 檔案系統 + Ollama ChatClient（分類要用）+
// 既有背景 goroutine 生命週期（syncCtx）。temp/ 與 export 目錄一起被 sync worker
// 排除（見 config.VaultConfig.ExcludedDirNames），所以 temp 內容在被分類搬走前
// 不會進圖譜。
package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/maoxiaoyue/hypgo/pkg/logger"

	"agent_graph/app/config"
)

// VaultTemp 把文字寫成 vault/temp/ 底下的一則 markdown 筆記。對話與上傳的共同落點。
type VaultTemp struct {
	root    string // vault 根
	tempDir string // 暫存子目錄名（相對 root）
}

func NewVaultTemp(root, tempDir string) *VaultTemp {
	return &VaultTemp{root: root, tempDir: tempDir}
}

func (t *VaultTemp) Enabled() bool { return t != nil && t.root != "" }

// tempDirAbs 暫存目錄絕對路徑。
func (t *VaultTemp) tempDirAbs() string {
	return filepath.Join(t.root, t.tempDir)
}

// Write 把一則筆記寫進 vault/temp/{prefix}-{slug}.md（撞名加序號不覆蓋）。
// title 進 frontmatter 與檔名；source 標記來源（chat / graphic_input 等）。
func (t *VaultTemp) Write(title, body, source string, tsPrefix string) (string, error) {
	if !t.Enabled() {
		return "", fmt.Errorf("vault temp not configured")
	}
	dir := t.tempDirAbs()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	base := tsPrefix + "-" + slugTitle(title)
	path := filepath.Join(dir, base+".md")
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.md", base, i))
	}
	content := renderTempNote(title, body, source)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write temp note: %w", err)
	}
	return path, nil
}

func renderTempNote(title, body, source string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlInline(title))
	if source != "" {
		fmt.Fprintf(&b, "source: %s\n", yamlInline(source))
	}
	b.WriteString("status: unclassified\n")
	b.WriteString("---\n\n")
	if title != "" {
		fmt.Fprintf(&b, "# %s\n\n", title)
	}
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String()
}

// ── 分類 / 整理 worker ───────────────────────────────────────────────────────

// DigestWorker 背景管線：閒置分類 + 每日整理切割。
type DigestWorker struct {
	root      string
	tempDir   string
	exportDir string
	chat      *ChatClient
	cfg       config.DigestConfig
	log       *logger.Logger

	// onFiled 檔案被歸到某分類目錄後呼叫（main.go 接到 sync worker 的 SyncFile），
	// 讓分類完的筆記立刻進圖譜、不必等 fsnotify（新目錄 fsnotify 監聽不到）。
	onFiled func(ctx context.Context, path string) error
}

func NewDigestWorker(vault config.VaultConfig, chat *ChatClient, log *logger.Logger) *DigestWorker {
	return &DigestWorker{
		root:      vault.Path,
		tempDir:   vault.TempDirName(),
		exportDir: vault.ExportDirName(),
		chat:      chat,
		cfg:       vault.Digest,
		log:       log,
	}
}

// WithOnFiled 設定「檔案歸類後」回呼（通常接 sync worker 的 SyncFile，讓分類完
// 的筆記立即攝取進圖譜）。
func (w *DigestWorker) WithOnFiled(fn func(ctx context.Context, path string) error) *DigestWorker {
	w.onFiled = fn
	return w
}

func (w *DigestWorker) classifyInterval() time.Duration {
	if w.cfg.ClassifyIntervalSec <= 0 {
		return 60 * time.Second
	}
	return time.Duration(w.cfg.ClassifyIntervalSec) * time.Second
}

func (w *DigestWorker) maintainInterval() time.Duration {
	if w.cfg.MaintainIntervalSec <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(w.cfg.MaintainIntervalSec) * time.Second
}

func (w *DigestWorker) maxCategoryFiles() int {
	if w.cfg.MaxCategoryFiles <= 0 {
		return 50
	}
	return w.cfg.MaxCategoryFiles
}

// Run 啟動兩個 ticker：分類（頻繁）+ 每日整理。ctx 取消即退出。
func (w *DigestWorker) Run(ctx context.Context) {
	if w.chat == nil {
		w.log.Warningf("digest worker disabled: no LLM chat client for classification")
		return
	}
	classifyT := time.NewTicker(w.classifyInterval())
	maintainT := time.NewTicker(w.maintainInterval())
	defer classifyT.Stop()
	defer maintainT.Stop()
	w.log.Infof("knowledge-digest worker active: temp=%s classify=%s maintain=%s maxFiles=%d",
		filepath.Join(w.root, w.tempDir), w.classifyInterval(), w.maintainInterval(), w.maxCategoryFiles())

	for {
		select {
		case <-ctx.Done():
			return
		case <-classifyT.C:
			if n := w.classifyPass(ctx); n > 0 {
				w.log.Infof("digest: filed %d temp note(s) into categories", n)
			}
		case <-maintainT.C:
			w.maintainPass(ctx)
		}
	}
}

// classifyPass 把 temp/ 內的每則 .md 分類、搬進 vault/<category>/。回傳成功歸類數。
func (w *DigestWorker) classifyPass(ctx context.Context) int {
	tempAbs := filepath.Join(w.root, w.tempDir)
	entries, err := os.ReadDir(tempAbs)
	if err != nil {
		return 0 // temp 還沒建立就當作沒東西
	}
	existing := w.listCategories()
	filed := 0
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		src := filepath.Join(tempAbs, e.Name())
		raw, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		title, body := splitTitleBody(string(raw), e.Name())
		cat, err := w.chat.ClassifyText(ctx, title, body, existing)
		if err != nil {
			w.log.Warningf("digest classify %s: %v", e.Name(), err)
			continue // 這輪失敗，下輪重試（檔案還在 temp）
		}
		if cat == "" {
			cat = "misc"
		}
		if newPath, err := w.fileInto(src, cat, e.Name()); err != nil {
			w.log.Warningf("digest file %s → %s: %v", e.Name(), cat, err)
		} else {
			filed++
			if !contains(existing, cat) {
				existing = append(existing, cat)
			}
			w.notifyFiled(ctx, newPath)
		}
	}
	return filed
}

// maintainPass 每日整理：分類目錄檔數超過上限時，把裡面的檔再細分成子分類。
func (w *DigestWorker) maintainPass(ctx context.Context) {
	for _, cat := range w.listCategories() {
		if ctx.Err() != nil {
			return
		}
		catAbs := filepath.Join(w.root, cat)
		files := mdFilesIn(catAbs)
		if len(files) <= w.maxCategoryFiles() {
			continue
		}
		w.log.Infof("digest: category %q has %d files (> %d) — splitting into sub-categories",
			cat, len(files), w.maxCategoryFiles())
		var subs []string
		for _, f := range files {
			if ctx.Err() != nil {
				return
			}
			raw, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			title, body := splitTitleBody(string(raw), filepath.Base(f))
			sub, err := w.chat.ClassifyText(ctx, title, body, subs)
			if err != nil || sub == "" || sub == cat {
				continue
			}
			dest := filepath.Join(cat, sub) // 相對分類名，fileInto 會建目錄
			if newPath, err := w.fileInto(f, dest, filepath.Base(f)); err == nil {
				if !contains(subs, sub) {
					subs = append(subs, sub)
				}
				w.notifyFiled(ctx, newPath)
			}
		}
	}
}

// fileInto 把 src 檔搬到 vault/<category>/<name>（撞名加序號）；回傳新路徑。
// category 可含子路徑（"cat/subcat"）。
func (w *DigestWorker) fileInto(src, category, name string) (string, error) {
	destDir := filepath.Join(w.root, filepath.FromSlash(category))
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	dest := filepath.Join(destDir, name)
	for i := 2; ; i++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		if filepath.Clean(dest) == filepath.Clean(src) {
			return src, nil // 已在目的地
		}
		dest = filepath.Join(destDir, fmt.Sprintf("%s-%d.md", stem, i))
	}
	if err := os.Rename(src, dest); err != nil {
		return "", err
	}
	return dest, nil
}

func (w *DigestWorker) notifyFiled(ctx context.Context, path string) {
	if w.onFiled == nil {
		return
	}
	if err := w.onFiled(ctx, path); err != nil {
		w.log.Warningf("digest: extract filed note %s: %v", path, err)
	}
}

// listCategories vault 根目錄下的分類目錄名（排除 temp / export / attachments /
// dot 目錄——那些不是分類）。
func (w *DigestWorker) listCategories() []string {
	entries, err := os.ReadDir(w.root)
	if err != nil {
		return nil
	}
	skip := map[string]bool{
		strings.ToLower(w.tempDir):   true,
		strings.ToLower(w.exportDir): true,
		"attachments":                true,
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || skip[strings.ToLower(e.Name())] {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// ── helpers ──────────────────────────────────────────────────────────────────

func mdFilesIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// splitTitleBody 從 .md 內容取 title（frontmatter title: 或 H1 或檔名）與 body。
func splitTitleBody(content, fallbackName string) (string, string) {
	title := ""
	body := content
	// 去掉 frontmatter 取 title
	if strings.HasPrefix(content, "---\n") {
		if end := strings.Index(content[4:], "\n---"); end >= 0 {
			fm := content[4 : 4+end]
			for _, line := range strings.Split(fm, "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "title:"); ok {
					title = strings.Trim(strings.TrimSpace(v), `"`)
				}
			}
			body = content[4+end+len("\n---"):]
		}
	}
	if title == "" {
		for _, line := range strings.Split(body, "\n") {
			if h, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
				title = strings.TrimSpace(h)
				break
			}
		}
	}
	if title == "" {
		title = strings.TrimSuffix(fallbackName, filepath.Ext(fallbackName))
	}
	return title, body
}

// slugTitle 把標題轉成檔名安全的 slug（保留 unicode 字母數字，其餘換 -，上限 60）。
func slugTitle(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if rs := []rune(out); len(rs) > 60 {
		out = strings.Trim(string(rs[:60]), "-")
	}
	if out == "" {
		out = "note"
	}
	return out
}

// SanitizeCategory 把 LLM 回的分類名正規化成目錄安全的 kebab-case（小寫、
// [a-z0-9-]、上限 40）。無效字元丟棄；空回 ""。
func SanitizeCategory(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_' || r == '/':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

func yamlInline(s string) string {
	s = strings.ReplaceAll(s, `"`, `'`)
	s = strings.ReplaceAll(s, "\n", " ")
	return `"` + s + `"`
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
