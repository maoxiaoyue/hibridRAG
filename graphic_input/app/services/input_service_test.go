package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"graphic_input/app/models"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Hello World": "hello-world",
		"知識 圖譜/測試":    "知識-圖譜-測試",
		"  a  b  ":    "a-b",
		"!!!":         "untitled",
		"":            "untitled",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyFile(t *testing.T) {
	cases := map[string]FileKind{
		"a.md":  KindMarkdown,
		"b.TXT": KindText,
		"c.png": KindImage,
		"d.pdf": KindPDF,
		"e.exe": KindUnsupported,
		"noext": KindUnsupported,
	}
	for name, want := range cases {
		if got := ClassifyFile(name); got != want {
			t.Errorf("ClassifyFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestExtractText_PlainAndInvalid(t *testing.T) {
	txt, err := ExtractText(context.Background(), nil, "note.txt", []byte("hello"))
	if err != nil || txt != "hello" {
		t.Errorf("txt extract = %q, %v", txt, err)
	}
	if _, err := ExtractText(context.Background(), nil, "bad.md", []byte{0xff, 0xfe, 0x01}); err == nil {
		t.Error("expected error for non-UTF-8 markdown")
	}
	if _, err := ExtractText(context.Background(), nil, "x.exe", []byte("x")); err == nil {
		t.Error("expected error for unsupported type")
	}
	// vision 未配置時圖片要明確回錯（不是 panic）
	if _, err := ExtractText(context.Background(), NewVisionClient("", "", "", 0), "p.png", []byte{1}); err == nil {
		t.Error("expected error when vision is disabled")
	}
}

func TestVaultWriter_WriteNote(t *testing.T) {
	root := t.TempDir()
	v := NewVaultWriter(root, "inbox", "attachments", "temp")

	rel, err := v.WriteNote(NoteOptions{
		Title: "My Note", Body: "content here", Actor: "tester",
		OriginalFile: "My Note.txt", Tags: []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	if !strings.HasPrefix(rel, "inbox/") || !strings.HasSuffix(rel, ".md") {
		t.Errorf("unexpected rel path %q", rel)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	s := string(data)
	for _, want := range []string{"source: graphic_input", "actor: tester", "original_file: My Note.txt", "tags: [a, b]", "# My Note", "content here"} {
		if !strings.Contains(s, want) {
			t.Errorf("note missing %q:\n%s", want, s)
		}
	}

	// 同標題再寫 → 不覆蓋（撞名加序號）
	rel2, err := v.WriteNote(NoteOptions{Title: "My Note", Body: "second"})
	if err != nil {
		t.Fatalf("WriteNote 2: %v", err)
	}
	if rel2 == rel {
		t.Errorf("second note should not overwrite first: %q", rel2)
	}
}

func TestVaultWriter_Attachment(t *testing.T) {
	root := t.TempDir()
	v := NewVaultWriter(root, "inbox", "attachments", "temp")
	rel, err := v.WriteNote(NoteOptions{
		Title: "Pic", Body: "ocr text", Attachment: []byte{1, 2, 3}, AttachName: "photo.png",
	})
	if err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if !strings.Contains(string(data), "![[") {
		t.Errorf("note should embed attachment:\n%s", data)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "attachments"))
	if len(entries) != 1 {
		t.Errorf("expected 1 attachment file, got %d", len(entries))
	}
}

func TestVaultWriter_Disabled(t *testing.T) {
	v := NewVaultWriter("", "", "", "")
	if v.Enabled() {
		t.Error("empty root should be disabled")
	}
	if _, err := v.WriteNote(NoteOptions{Title: "x", Body: "y"}); err == nil {
		t.Error("expected error when vault disabled")
	}
}

func TestParseBatch_CSV(t *testing.T) {
	rows, err := ParseBatch(models.BatchImportRequest{
		CSVKind: "entity",
		CSV:     "Apple,company,fruit company\nBanana",
	})
	if err != nil {
		t.Fatalf("ParseBatch: %v", err)
	}
	if len(rows) != 2 || rows[0].Entity.Name != "Apple" || rows[0].Entity.Category != "company" || rows[1].Entity.Name != "Banana" {
		t.Errorf("rows = %+v", rows)
	}

	rows, err = ParseBatch(models.BatchImportRequest{
		CSVKind: "relation",
		CSV:     "Apple,makes,iPhone",
	})
	if err != nil {
		t.Fatalf("ParseBatch rel: %v", err)
	}
	if len(rows) != 1 || rows[0].Relation.Source != "Apple" || rows[0].Relation.Type != "makes" || rows[0].Relation.Target != "iPhone" {
		t.Errorf("rows = %+v", rows)
	}

	if _, err := ParseBatch(models.BatchImportRequest{CSVKind: "nope", CSV: "x"}); err == nil {
		t.Error("expected error for bad csv_kind")
	}
	if _, err := ParseBatch(models.BatchImportRequest{}); err == nil {
		t.Error("expected error for empty request")
	}
}
