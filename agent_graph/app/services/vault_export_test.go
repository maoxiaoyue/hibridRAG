package services

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maoxiaoyue/hypgo/pkg/logger"

	"agent_graph/app/models"
)

// stubGraph 只餵 ListEntities/ListRelations 的極簡 GraphBackend——exporter
// 只用這兩個方法；其他方法不會被呼叫，panic 以抓誤用。
type stubGraph struct {
	entities  []models.EntityResult
	relations []models.RelationResult
}

func (s *stubGraph) Enabled() bool { return true }
func (s *stubGraph) ListEntities(context.Context) ([]models.EntityResult, error) {
	return s.entities, nil
}
func (s *stubGraph) ListRelations(context.Context) ([]models.RelationResult, error) {
	return s.relations, nil
}
func (s *stubGraph) UpsertEntity(context.Context, models.EntityResult) error {
	panic("not used by exporter")
}
func (s *stubGraph) UpsertRelation(context.Context, string, string, string) error {
	panic("not used by exporter")
}
func (s *stubGraph) SearchEntities(context.Context, string, int) ([]models.EntityResult, error) {
	panic("not used by exporter")
}
func (s *stubGraph) GetLocalContext(context.Context, string, int, int) (*models.LocalContextResponse, error) {
	panic("not used by exporter")
}
func (s *stubGraph) ExecuteCypher(context.Context, string, map[string]any, []string) ([]map[string]any, error) {
	panic("not used by exporter")
}

func testLogger(t *testing.T) *logger.Logger {
	t.Helper()
	log, err := logger.New("debug", "console", io.Discard, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	return log
}

func TestEntityFileBase(t *testing.T) {
	// 正常名稱原樣保留（wikilink 直接用原名）
	if got := entityFileBase("HypGo"); got != "HypGo" {
		t.Errorf("plain name changed: %q", got)
	}
	// 中文不動
	if got := entityFileBase("知識圖譜"); got != "知識圖譜" {
		t.Errorf("CJK name changed: %q", got)
	}
	// 非法字元替換 + hash 後綴
	got := entityFileBase("a/b:c")
	if strings.ContainsAny(got, `/\:*?"<>|#^[]`) {
		t.Errorf("sanitized name still has invalid chars: %q", got)
	}
	if !strings.HasPrefix(got, "a_b_c-") {
		t.Errorf("expected a_b_c-<hash>, got %q", got)
	}
	// 不同原名 sanitize 後不互撞
	if entityFileBase("a/b") == entityFileBase("a:b") {
		t.Error("distinct names collided after sanitization")
	}
	// 同名穩定
	if entityFileBase("a/b") != entityFileBase("a/b") {
		t.Error("fileBase not deterministic")
	}
	if got := entityFileBase("   "); !strings.HasPrefix(got, "untitled-") {
		t.Errorf("blank name: %q", got)
	}
}

func TestRenderEntityNote(t *testing.T) {
	ent := models.EntityResult{
		Name: "HypGo", Category: "framework", Description: "Go web framework",
		SourceFile: "notes/hypgo.md",
	}
	rels := []models.RelationResult{
		{Source: "HypGo", Target: "hidb", Type: "uses"},
		{Source: "HypGo", Target: "a/b", Type: "links_to"},
	}
	out := string(renderEntityNote(ent, rels))

	for _, want := range []string{
		"category: \"framework\"",
		"tags: [\"kb/framework\"]",
		"source: \"notes/hypgo.md\"",
		"Go web framework",
		"- [[hidb]]（uses）",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered note missing %q:\n%s", want, out)
		}
	}
	// sanitize 過的目標要用 [[fileBase|原名]] 形式
	wantAlias := "[[" + entityFileBase("a/b") + "|a/b]]"
	if !strings.Contains(out, wantAlias) {
		t.Errorf("rendered note missing aliased link %q:\n%s", wantAlias, out)
	}
	// 無 aliases 行（名稱未被改動）
	if strings.Contains(out, "aliases:") {
		t.Errorf("unexpected aliases for clean name:\n%s", out)
	}
	// 確定性
	if out != string(renderEntityNote(ent, rels)) {
		t.Error("render not deterministic")
	}
}

func TestVaultExporterExportAndReconcile(t *testing.T) {
	root := t.TempDir()
	graph := &stubGraph{
		entities: []models.EntityResult{
			{Name: "A", Category: "x", Description: "entity A"},
			{Name: "B", Description: "entity B"},
		},
		relations: []models.RelationResult{{Source: "A", Target: "B", Type: "uses"}},
	}
	e := NewVaultExporter(graph, root, "entities", testLogger(t))
	ctx := context.Background()

	// 全量匯出
	if err := e.export(ctx, nil, true); err != nil {
		t.Fatalf("export: %v", err)
	}
	pathA := filepath.Join(root, "entities", "A.md")
	contentA, err := os.ReadFile(pathA)
	if err != nil {
		t.Fatalf("read A.md: %v", err)
	}
	if !strings.Contains(string(contentA), "[[B]]（uses）") {
		t.Errorf("A.md missing outgoing wikilink:\n%s", contentA)
	}
	if _, err := os.Stat(filepath.Join(root, "entities", "B.md")); err != nil {
		t.Fatalf("B.md not written: %v", err)
	}

	// 增量：內容不變不重寫（mtime 不動）
	before, _ := os.Stat(pathA)
	if err := e.export(ctx, map[string]bool{"A": true}, false); err != nil {
		t.Fatalf("incremental export: %v", err)
	}
	after, _ := os.Stat(pathA)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("unchanged file was rewritten")
	}

	// 圖譜變更 → 重新 render
	graph.entities[0].Description = "entity A v2"
	if err := e.export(ctx, map[string]bool{"A": true}, false); err != nil {
		t.Fatalf("re-export: %v", err)
	}
	contentA2, _ := os.ReadFile(pathA)
	if !strings.Contains(string(contentA2), "entity A v2") {
		t.Errorf("A.md not updated:\n%s", contentA2)
	}

	// reconcile 清掉圖譜已無對應實體的殘檔
	stale := filepath.Join(root, "entities", "Gone.md")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.export(ctx, nil, true); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale file not removed by reconcile")
	}
}

func TestVaultFSPathSafety(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "notes")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "ok.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	// dot 目錄不進 manifest
	if err := os.MkdirAll(filepath.Join(root, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".obsidian", "cfg.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	v := NewVaultFS(root)

	files, err := v.Manifest(context.Background())
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(files) != 1 || files[0].Path != "notes/ok.md" {
		t.Fatalf("manifest = %+v, want single notes/ok.md", files)
	}
	if files[0].SHA256 == "" || files[0].Size != 5 {
		t.Errorf("manifest entry incomplete: %+v", files[0])
	}

	if f, err := v.Open("notes/ok.md"); err != nil {
		t.Errorf("open valid path: %v", err)
	} else {
		f.Close()
	}
	for _, bad := range []string{
		"../outside.md",
		"notes/../../outside.md",
		"C:/Windows/system.md",
		"/etc/passwd.md",
		"secret.txt",
		"",
	} {
		if f, err := v.Open(bad); err == nil {
			f.Close()
			t.Errorf("Open(%q) should have been rejected", bad)
		}
	}

	// 停用時明確回錯
	var disabled *VaultFS
	if disabled.Enabled() {
		t.Error("nil VaultFS should be disabled")
	}
	if _, err := NewVaultFS("").Manifest(context.Background()); err == nil {
		t.Error("empty-root manifest should fail")
	}
}
