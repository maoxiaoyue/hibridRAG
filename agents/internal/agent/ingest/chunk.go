// Package ingest 提供知識攝取管線的切塊（chunking）與後續 embed 流程
//
// 切塊策略依來源類型選擇：
//   - .json / .md / .markdown → DocumentChunker （依結構切分，零 LLM 呼叫）
//   - 其他文本              → LLMChunker     （語意切分，呼叫 LLM）
//
// 由 ChunkerRouter 統一派發，呼叫端不需關心選型：
//
//	router := ingest.NewChunkerRouter(llmClient)
//	chunks, err := router.Chunk(ctx, ingest.ChunkInput{Path: "foo.md", Body: ...})
package ingest

import (
	"context"
	"path/filepath"
	"strings"
)

// ChunkStrategy 切塊策略
type ChunkStrategy string

const (
	StrategyDocument ChunkStrategy = "document"  // 結構化（md heading / json key）
	StrategyLLM      ChunkStrategy = "llm_based" // LLM 語意切分
)

// Chunk 單一切塊
type Chunk struct {
	Index    int               // 在來源內的序號（0-based）
	Text     string            // 切塊內容
	Strategy ChunkStrategy     // 採用的策略
	Metadata map[string]string // 額外 metadata（例如 md heading 路徑、json key 路徑）
}

// ChunkInput 切塊輸入
type ChunkInput struct {
	Path     string // 相對或絕對路徑；用於判斷副檔名
	MIME     string // 選填；若已知 content-type 可直接帶入覆蓋副檔名判斷
	Body     string // 內容
	MaxChars int    // 選填；每塊最大字元數上限（0 = 採策略預設 512）
	Overlap  int    // 選填；相鄰塊之間的字元重疊數（0 = 採策略預設 50；負值 = 不重疊）
}

// Chunker 切塊器介面
type Chunker interface {
	Name() ChunkStrategy
	Chunk(ctx context.Context, in ChunkInput) ([]Chunk, error)
}

// ChunkerRouter 依檔案類型派發到對應的 Chunker
type ChunkerRouter struct {
	doc *DocumentChunker
	llm *LLMChunker
}

// NewChunkerRouter 建立派發器
// llmClient 可為 nil；若為 nil 則遇到需要 LLM 切分的內容時回退到 DocumentChunker 的純文字段落切法
func NewChunkerRouter(llmClient LLMClient) *ChunkerRouter {
	return &ChunkerRouter{
		doc: NewDocumentChunker(),
		llm: NewLLMChunker(llmClient),
	}
}

// Chunk 依 input 選擇策略並切塊
func (r *ChunkerRouter) Chunk(ctx context.Context, in ChunkInput) ([]Chunk, error) {
	switch DetectStrategy(in) {
	case StrategyDocument:
		return r.doc.Chunk(ctx, in)
	default:
		// 沒配 LLM client → 退回 DocumentChunker 的段落切法（由其內部處理非 md/json 的情況）
		if r.llm == nil || r.llm.client == nil {
			return r.doc.chunkPlainText(in), nil
		}
		return r.llm.Chunk(ctx, in)
	}
}

// DetectStrategy 判斷檔案應走哪條策略
//  1. 優先看 MIME（若呼叫端帶入）
//  2. 否則看副檔名：.json / .md / .markdown → document；其他 → llm_based
func DetectStrategy(in ChunkInput) ChunkStrategy {
	mime := strings.ToLower(in.MIME)
	switch {
	case strings.Contains(mime, "json"):
		return StrategyDocument
	case strings.Contains(mime, "markdown"):
		return StrategyDocument
	}

	ext := strings.ToLower(filepath.Ext(in.Path))
	switch ext {
	case ".json", ".md", ".markdown":
		return StrategyDocument
	}
	return StrategyLLM
}
