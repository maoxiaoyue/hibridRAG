package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maoxiaoyue/hypgo/pkg/logger"

	"agent_graph/app/config"
)

func TestSanitizeCategory(t *testing.T) {
	cases := map[string]string{
		"Machine Learning":   "machine-learning",
		"  Web Dev  ":        "web-dev",
		"foo/bar_baz":        "foo-bar-baz",
		"UPPER":              "upper",
		"!!!":                "",
		"a--b":               "a-b",
		strings.Repeat("x", 60): strings.Repeat("x", 40),
	}
	for in, want := range cases {
		if got := SanitizeCategory(in); got != want {
			t.Errorf("SanitizeCategory(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugTitle(t *testing.T) {
	if got := slugTitle("Hello, World!"); got != "Hello-World" {
		t.Errorf("slug = %q", got)
	}
	if got := slugTitle("知識圖譜 test"); got != "知識圖譜-test" {
		t.Errorf("CJK slug = %q", got)
	}
	if got := slugTitle("   "); got != "note" {
		t.Errorf("blank slug = %q", got)
	}
}

func TestSplitTitleBody(t *testing.T) {
	content := "---\ntitle: \"My Note\"\nsource: \"chat\"\n---\n\n# My Note\n\nbody text here\n"
	title, body := splitTitleBody(content, "20260710-x.md")
	if title != "My Note" {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(body, "body text here") || strings.Contains(body, "source:") {
		t.Errorf("body should be post-frontmatter: %q", body)
	}
	// H1 fallback when no frontmatter title
	title2, _ := splitTitleBody("# Heading Only\n\ntext", "fallback.md")
	if title2 != "Heading Only" {
		t.Errorf("H1 fallback = %q", title2)
	}
	// filename fallback
	title3, _ := splitTitleBody("no title anywhere", "20260710-my-file.md")
	if title3 != "20260710-my-file" {
		t.Errorf("filename fallback = %q", title3)
	}
}

func TestVaultTempWrite(t *testing.T) {
	root := t.TempDir()
	vt := NewVaultTemp(root, "temp")

	p1, err := vt.Write("Q about Go", "How do I do X?\n\nYou do Y.", "chat", "20260710")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if filepath.Dir(p1) != filepath.Join(root, "temp") {
		t.Errorf("temp note not in temp dir: %s", p1)
	}
	raw, _ := os.ReadFile(p1)
	for _, want := range []string{"title: \"Q about Go\"", "source: \"chat\"", "status: unclassified", "How do I do X?"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("temp note missing %q:\n%s", want, raw)
		}
	}
	// 撞名不覆蓋
	p2, _ := vt.Write("Q about Go", "another", "chat", "20260710")
	if p1 == p2 {
		t.Error("collision should produce a distinct filename")
	}

	// 停用
	if NewVaultTemp("", "temp").Enabled() {
		t.Error("empty-root temp should be disabled")
	}
}

func testDigestLogger(t *testing.T) *logger.Logger {
	t.Helper()
	l, err := logger.New("error", "console", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestDigestFileIntoAndListCategories(t *testing.T) {
	root := t.TempDir()
	// 造 temp + 既有分類目錄 + 系統目錄
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "temp"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "programming"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "entities"), 0o755))     // export dir — 不算分類
	must(os.MkdirAll(filepath.Join(root, "attachments"), 0o755))  // 系統目錄
	must(os.MkdirAll(filepath.Join(root, ".obsidian"), 0o755))    // dot 目錄
	src := filepath.Join(root, "temp", "20260710-note.md")
	must(os.WriteFile(src, []byte("# note\n\ntext"), 0o644))

	w := NewDigestWorker(config.VaultConfig{
		Path: root, TempDir: "temp", ExportDir: "entities",
	}, nil, testDigestLogger(t))

	// listCategories 只回真正的分類目錄
	cats := w.listCategories()
	if len(cats) != 1 || cats[0] != "programming" {
		t.Fatalf("listCategories = %v, want [programming]", cats)
	}

	// fileInto 把 temp 檔搬進分類目錄
	newPath, err := w.fileInto(src, "web-dev", "20260710-note.md")
	if err != nil {
		t.Fatalf("fileInto: %v", err)
	}
	if newPath != filepath.Join(root, "web-dev", "20260710-note.md") {
		t.Errorf("filed to wrong place: %s", newPath)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be moved out of temp")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("filed note missing: %v", err)
	}
	// 現在 web-dev 也算一個分類
	if !contains(w.listCategories(), "web-dev") {
		t.Error("web-dev should now be a category")
	}
}
