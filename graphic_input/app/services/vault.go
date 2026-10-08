// vault.go — 「Obsidian 版本」路徑：把輸入內容落地成 vault 筆記。
//
// 落點 vault/inbox/{YYYYMMDD}-{slug}.md（人工輸入集中在 inbox/ 與原生筆記區隔）；
// 圖片原檔另存 vault/attachments/ 並在筆記內以 ![[...]] embed。寫入即觸發
// agent_graph 的 fsnotify 同步，本服務不需另行通知。
//
// 注意：agent_graph 的 fsnotify 不遞迴 watch 新目錄——inbox/、attachments/ 若是
// 首次建立，要等 agent_graph 重啟（啟動時 walk 全 vault）才會被 watch；本服務
// 啟動時主動預建這兩個目錄，把「新目錄盲區」收斂到部署期一次性事件。
package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// VaultWriter 寫 Obsidian vault。path 空 = 停用（obsidian 路徑回錯）。
type VaultWriter struct {
	root           string
	inboxDir       string
	attachmentsDir string
	tempDir        string
}

// NewVaultWriter root 空字串回傳停用的 writer（Enabled()=false）。tempDir 是知識
// 沉澱管線的暫存子目錄（須與 agent_graph 的 md.yaml vault.temp_dir 一致）；空 → "temp"。
func NewVaultWriter(root, inboxDir, attachmentsDir, tempDir string) *VaultWriter {
	if inboxDir == "" {
		inboxDir = "inbox"
	}
	if attachmentsDir == "" {
		attachmentsDir = "attachments"
	}
	if tempDir == "" {
		tempDir = "temp"
	}
	return &VaultWriter{root: strings.TrimSpace(root), inboxDir: inboxDir, attachmentsDir: attachmentsDir, tempDir: tempDir}
}

// TempDirName 暫存子目錄名。
func (v *VaultWriter) TempDirName() string { return v.tempDir }

// Enabled obsidian 路徑是否可用。
func (v *VaultWriter) Enabled() bool { return v != nil && v.root != "" }

// EnsureDirs 預建 inbox/ 與 attachments/（見套件註解的 fsnotify 盲區說明）。
func (v *VaultWriter) EnsureDirs() error {
	if !v.Enabled() {
		return nil
	}
	for _, d := range []string{v.inboxDir, v.attachmentsDir} {
		if err := os.MkdirAll(filepath.Join(v.root, d), 0o755); err != nil {
			return fmt.Errorf("mkdir vault %s: %w", d, err)
		}
	}
	return nil
}

// NoteOptions 一則筆記的輸入。
type NoteOptions struct {
	Title        string // 通常 = 原始檔名去副檔名
	Body         string // 抽出的純文字（markdown 原文直接放）
	Actor        string
	OriginalFile string
	Tags         []string
	Attachment   []byte // 非 nil = 圖片原檔，存 attachments/ 並在筆記 embed
	AttachName   string // 圖片原始檔名
	Dir          string // 落點子目錄；空 = inbox。target=temp 時填 tempDir
}

// WriteNote 落地一則筆記，回傳 vault 相對路徑。
func (v *VaultWriter) WriteNote(opt NoteOptions) (string, error) {
	if !v.Enabled() {
		return "", fmt.Errorf("obsidian path disabled (vault.path not configured)")
	}
	if err := v.EnsureDirs(); err != nil {
		return "", err
	}

	title := strings.TrimSpace(opt.Title)
	if title == "" {
		title = "untitled"
	}

	// 落點子目錄：預設 inbox；target=temp 時走 tempDir（未分類，等 digest worker 挑走）。
	dir := opt.Dir
	if dir == "" {
		dir = v.inboxDir
	}
	if err := os.MkdirAll(filepath.Join(v.root, dir), 0o755); err != nil {
		return "", fmt.Errorf("mkdir vault %s: %w", dir, err)
	}

	// 檔名：{YYYYMMDD}-{slug}.md；撞名時加 -2、-3…（不覆蓋既有筆記）
	base := time.Now().Format("20060102") + "-" + Slug(title)
	relPath := filepath.Join(dir, base+".md")
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(v.root, relPath)); os.IsNotExist(err) {
			break
		}
		relPath = filepath.Join(dir, fmt.Sprintf("%s-%d.md", base, i))
	}

	// 圖片原檔進 attachments/
	embedLine := ""
	if len(opt.Attachment) > 0 {
		attachName := time.Now().Format("20060102-150405") + "-" + Slug(strings.TrimSuffix(opt.AttachName, filepath.Ext(opt.AttachName))) + filepath.Ext(opt.AttachName)
		attachRel := filepath.Join(v.attachmentsDir, attachName)
		if err := os.WriteFile(filepath.Join(v.root, attachRel), opt.Attachment, 0o644); err != nil {
			return "", fmt.Errorf("write attachment: %w", err)
		}
		embedLine = "\n![[" + attachName + "]]\n"
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("source: graphic_input\n")
	if opt.Actor != "" {
		sb.WriteString("actor: " + yamlEscape(opt.Actor) + "\n")
	}
	sb.WriteString("created: " + time.Now().Format(time.RFC3339) + "\n")
	if opt.OriginalFile != "" {
		sb.WriteString("original_file: " + yamlEscape(opt.OriginalFile) + "\n")
	}
	if len(opt.Tags) > 0 {
		sb.WriteString("tags: [" + strings.Join(opt.Tags, ", ") + "]\n")
	}
	sb.WriteString("---\n\n")
	sb.WriteString("# " + title + "\n")
	sb.WriteString(embedLine)
	sb.WriteString("\n" + strings.TrimSpace(opt.Body) + "\n")

	full := filepath.Join(v.root, relPath)
	if err := os.WriteFile(full, []byte(sb.String()), 0o644); err != nil {
		return "", fmt.Errorf("write note: %w", err)
	}
	return filepath.ToSlash(relPath), nil
}

// Slug 產生檔名安全的 slug：保留 unicode 字母/數字（中文標題常見，
// 同 agents 的 slugify 理由），其餘字元合併成單一 '-'。
func Slug(s string) string {
	var sb strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			sb.WriteRune(unicode.ToLower(r))
			lastDash = false
		default:
			if !lastDash && sb.Len() > 0 {
				sb.WriteRune('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(sb.String(), "-")
	if out == "" {
		return "untitled"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func yamlEscape(s string) string {
	if strings.ContainsAny(s, ":#{}[],&*!|>'\"%@`") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}
