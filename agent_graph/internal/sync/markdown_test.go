package sync

import (
	"reflect"
	"testing"
)

func TestParseNote_FrontmatterWins(t *testing.T) {
	raw := []byte(`---
title: Explicit Title
type: concept
description: A one-line summary.
---
# Ignored Heading

Body paragraph text that would otherwise become the description.

Links to [[Other Page]] and [[Aliased|Alias Text]].
`)
	n := ParseNote("/vault/note.md", raw)
	if n.Title != "Explicit Title" {
		t.Errorf("Title = %q, want frontmatter title", n.Title)
	}
	if n.Category != "concept" {
		t.Errorf("Category = %q, want concept", n.Category)
	}
	if n.Description != "A one-line summary." {
		t.Errorf("Description = %q, want frontmatter description", n.Description)
	}
	want := []string{"Other Page", "Aliased"}
	if !reflect.DeepEqual(n.WikiLinks, want) {
		t.Errorf("WikiLinks = %v, want %v", n.WikiLinks, want)
	}
}

func TestParseNote_FallsBackToH1AndFirstParagraph(t *testing.T) {
	raw := []byte(`# My Heading Title

This is the first real paragraph, used as description.

Second paragraph, ignored.
`)
	n := ParseNote("/vault/whatever.md", raw)
	if n.Title != "My Heading Title" {
		t.Errorf("Title = %q, want H1 fallback", n.Title)
	}
	if n.Description != "This is the first real paragraph, used as description." {
		t.Errorf("Description = %q, want first paragraph", n.Description)
	}
}

func TestParseNote_FallsBackToFilename(t *testing.T) {
	raw := []byte("Just some prose with no heading.\n")
	n := ParseNote("/vault/My Note.md", raw)
	if n.Title != "My Note" {
		t.Errorf("Title = %q, want filename fallback", n.Title)
	}
}

func TestParseNote_MalformedFrontmatterDoesNotFail(t *testing.T) {
	raw := []byte("---\n: not valid yaml: [\n---\n# Fallback Title\nBody.\n")
	n := ParseNote("/vault/x.md", raw)
	if n.Title != "Fallback Title" {
		t.Errorf("Title = %q, want H1 fallback after frontmatter parse failure", n.Title)
	}
}

func TestExtractWikiLinks_DedupesAndSkipsImageEmbeds(t *testing.T) {
	body := "See [[Foo]] and [[Foo]] again, plus ![[diagram.png]] and [[Bar#Section|label]]."
	got := extractWikiLinks(body)
	want := []string{"Foo", "Bar"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractWikiLinks = %v, want %v", got, want)
	}
}

func TestExtractWikiLinks_NoLinks(t *testing.T) {
	if got := extractWikiLinks("no links here"); got != nil {
		t.Errorf("extractWikiLinks = %v, want nil", got)
	}
}

func TestSplitFrontmatter_NoFrontmatter(t *testing.T) {
	fm, body := splitFrontmatter("# Just a note\nNo frontmatter here.")
	if len(fm) != 0 {
		t.Errorf("fm = %v, want empty", fm)
	}
	if body != "# Just a note\nNo frontmatter here." {
		t.Errorf("body = %q, want unchanged content", body)
	}
}

func TestSplitFrontmatter_UnterminatedBlockTreatedAsNoFrontmatter(t *testing.T) {
	content := "---\ntitle: X\nbody without closing marker"
	fm, body := splitFrontmatter(content)
	if len(fm) != 0 || body != content {
		t.Errorf("unterminated frontmatter should be treated as plain content; fm=%v body=%q", fm, body)
	}
}
