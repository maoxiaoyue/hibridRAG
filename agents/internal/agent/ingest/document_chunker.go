package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Chunker 預設值（符合架構圖：512 字元 / chunk，50 字元 overlap）
const (
	DefaultMaxChars = 512
	DefaultOverlap  = 50
)

// DocumentChunker 結構化切塊：md 依 heading、json 依 top-level key / array element
type DocumentChunker struct {
	// DefaultMaxChars 預設每塊字元上限（rune 計）；0 或負值視為不限
	DefaultMaxChars int
	// DefaultOverlap 相鄰塊之間的字元重疊數（rune 計）；<0 視為 0
	DefaultOverlap int
}

func NewDocumentChunker() *DocumentChunker {
	return &DocumentChunker{DefaultMaxChars: DefaultMaxChars, DefaultOverlap: DefaultOverlap}
}

func (c *DocumentChunker) Name() ChunkStrategy { return StrategyDocument }

// Chunk 依 input 類型派發：md / json / 其他 → 段落 fallback
func (c *DocumentChunker) Chunk(_ context.Context, in ChunkInput) ([]Chunk, error) {
	maxChars, overlap := c.resolveLimits(in)

	switch detectDocType(in) {
	case "json":
		return c.chunkJSON(in.Body, maxChars, overlap)
	case "markdown":
		return c.chunkMarkdown(in.Body, maxChars, overlap), nil
	default:
		return c.chunkPlainText(in), nil
	}
}

func (c *DocumentChunker) resolveLimits(in ChunkInput) (int, int) {
	maxChars := in.MaxChars
	if maxChars <= 0 {
		maxChars = c.DefaultMaxChars
	}
	overlap := in.Overlap
	switch {
	case overlap < 0:
		overlap = 0
	case overlap == 0:
		overlap = c.DefaultOverlap
	}
	if overlap >= maxChars {
		overlap = maxChars / 4
	}
	return maxChars, overlap
}

// ---- Markdown ----

var reHeading = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+?)\s*$`)

// chunkMarkdown 依 heading 層級切分；每個 heading 與其下到下一個同級或更高級 heading 前的內容合併成一塊
// 過長的塊會再依段落二次切分，確保不超過 maxChars
func (c *DocumentChunker) chunkMarkdown(body string, maxChars, overlap int) []Chunk {
	lines := strings.Split(body, "\n")

	type section struct {
		level   int
		title   string
		path    []string
		content strings.Builder
	}

	var sections []*section
	var stack []*section

	var preamble strings.Builder
	started := false

	for _, line := range lines {
		m := reHeading.FindStringSubmatch(line)
		if m == nil {
			if !started {
				preamble.WriteString(line)
				preamble.WriteString("\n")
			} else if len(sections) > 0 {
				sections[len(sections)-1].content.WriteString(line)
				sections[len(sections)-1].content.WriteString("\n")
			}
			continue
		}
		started = true
		level := len(m[1])
		title := strings.TrimSpace(m[2])

		for len(stack) > 0 && stack[len(stack)-1].level >= level {
			stack = stack[:len(stack)-1]
		}
		path := make([]string, 0, len(stack)+1)
		for _, s := range stack {
			path = append(path, s.title)
		}
		path = append(path, title)

		sec := &section{level: level, title: title, path: path}
		sec.content.WriteString(line)
		sec.content.WriteString("\n")

		sections = append(sections, sec)
		stack = append(stack, sec)
	}

	var out []Chunk
	idx := 0

	if pre := strings.TrimSpace(preamble.String()); pre != "" {
		for _, piece := range splitByMaxChars(pre, maxChars, overlap) {
			out = append(out, Chunk{
				Index:    idx,
				Text:     piece,
				Strategy: StrategyDocument,
				Metadata: map[string]string{"doc_type": "markdown", "section": "(preamble)"},
			})
			idx++
		}
	}

	for _, sec := range sections {
		text := strings.TrimRight(sec.content.String(), "\n")
		sectionPath := strings.Join(sec.path, " / ")
		for _, piece := range splitByMaxChars(text, maxChars, overlap) {
			out = append(out, Chunk{
				Index:    idx,
				Text:     piece,
				Strategy: StrategyDocument,
				Metadata: map[string]string{
					"doc_type": "markdown",
					"section":  sectionPath,
					"level":    fmt.Sprintf("%d", sec.level),
				},
			})
			idx++
		}
	}

	if len(out) == 0 {
		return c.chunkPlainText(ChunkInput{Body: body})
	}
	return out
}

// ---- JSON ----

func (c *DocumentChunker) chunkJSON(body string, maxChars, overlap int) ([]Chunk, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil, nil
	}

	var top interface{}
	if err := json.Unmarshal([]byte(trimmed), &top); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	var out []Chunk
	idx := 0
	emit := func(path string, v interface{}) {
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			raw = []byte(fmt.Sprintf("%v", v))
		}
		for _, piece := range splitByMaxChars(string(raw), maxChars, overlap) {
			out = append(out, Chunk{
				Index:    idx,
				Text:     piece,
				Strategy: StrategyDocument,
				Metadata: map[string]string{"doc_type": "json", "path": path},
			})
			idx++
		}
	}

	switch t := top.(type) {
	case map[string]interface{}:
		for k, v := range t {
			emit(k, v)
		}
	case []interface{}:
		for i, v := range t {
			emit(fmt.Sprintf("[%d]", i), v)
		}
	default:
		emit("$", t)
	}

	return out, nil
}

// ---- plain text fallback ----

// chunkPlainText 把內容依空行切段，必要時再以 maxChars+overlap 切
func (c *DocumentChunker) chunkPlainText(in ChunkInput) []Chunk {
	maxChars, overlap := c.resolveLimits(in)
	paragraphs := splitParagraphs(in.Body)

	var out []Chunk
	idx := 0
	for _, p := range paragraphs {
		for _, piece := range splitByMaxChars(p, maxChars, overlap) {
			out = append(out, Chunk{
				Index:    idx,
				Text:     piece,
				Strategy: StrategyDocument,
				Metadata: map[string]string{"doc_type": "text"},
			})
			idx++
		}
	}
	return out
}

// ---- helpers ----

func detectDocType(in ChunkInput) string {
	mime := strings.ToLower(in.MIME)
	switch {
	case strings.Contains(mime, "json"):
		return "json"
	case strings.Contains(mime, "markdown"):
		return "markdown"
	}
	switch strings.ToLower(extOf(in.Path)) {
	case ".json":
		return "json"
	case ".md", ".markdown":
		return "markdown"
	}
	return "text"
}

func extOf(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i:]
	}
	return ""
}

var reBlankLines = regexp.MustCompile(`\n{2,}`)

func splitParagraphs(body string) []string {
	parts := reBlankLines.Split(strings.TrimSpace(body), -1)
	out := parts[:0]
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// splitByMaxChars 以 rune（字元）為單位切分；相鄰塊重疊 overlap 字元
//
//   - 每塊長度 ≤ maxChars（rune 計，對中日韓文正確）
//   - 優先在換行處斷（搜尋範圍 [maxChars/2, maxChars]）；找不到就硬切
//   - 下一塊起點 = 當前切點 - overlap（overlap <= 0 則無重疊）
//
// 設計理由：embedding 時 chunk 邊界上下文丟失，overlap 讓邊界語意可被兩側 chunk 捕捉
func splitByMaxChars(s string, maxChars, overlap int) []string {
	if maxChars <= 0 {
		return []string{s}
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return []string{s}
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxChars {
		overlap = maxChars / 4
	}

	var out []string
	start := 0
	for start < len(runes) {
		end := start + maxChars
		if end >= len(runes) {
			if piece := strings.TrimSpace(string(runes[start:])); piece != "" {
				out = append(out, piece)
			}
			break
		}
		// 在 [start+maxChars/2, end] 區間找最後一個換行當斷點
		cut := end
		for i := end; i > start+maxChars/2; i-- {
			if runes[i-1] == '\n' {
				cut = i
				break
			}
		}
		if piece := strings.TrimSpace(string(runes[start:cut])); piece != "" {
			out = append(out, piece)
		}
		next := cut - overlap
		if next <= start {
			next = cut
		}
		start = next
	}
	return out
}
