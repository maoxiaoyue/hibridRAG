// markdown.go — pure Obsidian markdown parsing (frontmatter, WikiLinks,
// title/description fallbacks). No I/O so it's fully unit-testable; worker.go
// is the only caller and only place that touches the filesystem/DB/network.
package sync

import (
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Note is one parsed markdown file's structural graph-relevant content.
type Note struct {
	Title       string
	Category    string
	Description string
	Body        string // content after the frontmatter block
	WikiLinks   []string
}

var (
	wikiLinkPattern = regexp.MustCompile(`\[\[([^\]|#]+)`)
	nonEntityExt    = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|svg|webp|pdf|mp3|mp4|wav|mov)$`)
)

// ParseNote extracts title/category/description/WikiLinks from one file's
// raw content. path is only used as a last-resort title fallback (filename).
func ParseNote(path string, raw []byte) Note {
	fm, body := splitFrontmatter(string(raw))

	title := fm["title"]
	if title == "" {
		title = firstH1(body)
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	category := fm["type"]
	if category == "" {
		category = fm["category"]
	}

	description := fm["description"]
	if description == "" {
		description = firstParagraph(body)
	}

	return Note{
		Title:       title,
		Category:    category,
		Description: description,
		Body:        body,
		WikiLinks:   extractWikiLinks(body),
	}
}

// splitFrontmatter splits a leading `---\n...\n---` YAML block from the rest
// of the body. Malformed or absent frontmatter degrades to an empty map and
// the original content as body — never an error, since a note without valid
// frontmatter is still a perfectly normal note to sync.
func splitFrontmatter(content string) (map[string]string, string) {
	if !strings.HasPrefix(content, "---") {
		return map[string]string{}, content
	}
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return map[string]string{}, content
	}
	fmText := rest[:idx]
	body := strings.TrimPrefix(rest[idx+len("\n---"):], "\n")

	var raw map[string]any
	_ = yaml.Unmarshal([]byte(fmText), &raw) // parse failure → empty map, not fatal
	fm := map[string]string{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			fm[strings.ToLower(k)] = s
		}
	}
	return fm, body
}

func firstH1(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return ""
}

// firstParagraph returns the first contiguous run of non-heading, non-blank
// lines, joined and truncated — used as a description fallback when
// frontmatter doesn't provide one (feeds both the graph node's description
// property and the text that gets embedded for vector search).
func firstParagraph(body string) string {
	var buf []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(buf) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		buf = append(buf, trimmed)
	}
	return truncateRunes(strings.Join(buf, " "), 300)
}

// extractWikiLinks pulls unique [[Page]] / [[Page|Alias]] / [[Page#Heading]]
// targets, skipping image/media embeds (![[photo.png]]) which aren't
// meaningful graph entities.
func extractWikiLinks(body string) []string {
	matches := wikiLinkPattern.FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] || nonEntityExt.MatchString(name) {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
