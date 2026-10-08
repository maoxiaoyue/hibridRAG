package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// LLMClient 由呼叫端注入的 LLM 介面；保持最小面以便替換實作
//
// 合約：輸入 system + user prompt，回傳純文字；
// LLMChunker 會要求模型輸出特定 JSON，再自行解析
type LLMClient interface {
	Complete(ctx context.Context, req LLMRequest) (LLMResponse, error)
}

type LLMRequest struct {
	System      string
	User        string
	Temperature float32
	MaxTokens   int
}

type LLMResponse struct {
	Text string
}

// LLMChunker 用 LLM 做語意切分
//
// 流程：把原文交給模型，要求回傳切點陣列（依字元位置或原文片段），
// 解析後組成 Chunk。失敗時退回 DocumentChunker.chunkPlainText 以保證至少產出。
type LLMChunker struct {
	client   LLMClient
	fallback *DocumentChunker

	// DefaultMaxChars 提示模型每塊字元上限；也作為過大塊的硬上限
	DefaultMaxChars int
	// DefaultOverlap 過大塊二次切分時的重疊字元
	DefaultOverlap int
	// Temperature 切塊任務期望穩定 → 低溫度
	Temperature float32
}

func NewLLMChunker(client LLMClient) *LLMChunker {
	return &LLMChunker{
		client:          client,
		fallback:        NewDocumentChunker(),
		DefaultMaxChars: DefaultMaxChars,
		DefaultOverlap:  DefaultOverlap,
		Temperature:     0.1,
	}
}

func (c *LLMChunker) Name() ChunkStrategy { return StrategyLLM }

// Chunk 呼叫 LLM 取得語意切點；LLM 失效時退回段落切法
func (c *LLMChunker) Chunk(ctx context.Context, in ChunkInput) ([]Chunk, error) {
	if c.client == nil {
		return c.fallback.chunkPlainText(in), nil
	}

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

	resp, err := c.client.Complete(ctx, LLMRequest{
		System:      llmChunkSystemPrompt,
		User:        buildLLMUserPrompt(in.Body, maxChars),
		Temperature: c.Temperature,
		MaxTokens:   2048,
	})
	if err != nil {
		// 不讓 LLM 故障阻擋攝取；退回段落切法
		return c.fallback.chunkPlainText(in), nil
	}

	pieces, perr := parseLLMChunkResponse(resp.Text)
	if perr != nil || len(pieces) == 0 {
		return c.fallback.chunkPlainText(in), nil
	}

	out := make([]Chunk, 0, len(pieces))
	for i, p := range pieces {
		text := strings.TrimSpace(p)
		if text == "" {
			continue
		}
		for _, piece := range splitByMaxChars(text, maxChars, overlap) {
			out = append(out, Chunk{
				Index:    len(out),
				Text:     piece,
				Strategy: StrategyLLM,
				Metadata: map[string]string{"semantic_index": fmt.Sprintf("%d", i)},
			})
		}
	}
	if len(out) == 0 {
		return c.fallback.chunkPlainText(in), nil
	}
	return out, nil
}

// ---- prompt / parse ----

const llmChunkSystemPrompt = `You are a document chunking assistant. ` +
	`Split the user-provided text into semantically coherent chunks for retrieval. ` +
	`Rules:
- Keep each chunk focused on a single topic or step.
- Do not paraphrase; preserve the original wording.
- Respect natural boundaries (paragraphs, lists, code blocks).
- Respond ONLY with a JSON object of the form:
  {"chunks": ["chunk1 text", "chunk2 text", ...]}
- No commentary, no markdown code fences around the JSON.`

func buildLLMUserPrompt(body string, maxChars int) string {
	return fmt.Sprintf(
		"Target max chars per chunk: %d.\n\n---BEGIN TEXT---\n%s\n---END TEXT---",
		maxChars, body,
	)
}

type llmChunkPayload struct {
	Chunks []string `json:"chunks"`
}

// parseLLMChunkResponse 容錯解析：先試嚴格 JSON，失敗時嘗試從文字中抽 JSON 物件
func parseLLMChunkResponse(text string) ([]string, error) {
	trimmed := strings.TrimSpace(text)
	// 有些模型會加 ```json ... ```
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	var p llmChunkPayload
	if err := json.Unmarshal([]byte(trimmed), &p); err == nil {
		return p.Chunks, nil
	}

	// 再嘗試抽第一個 { ... }
	if start := strings.Index(trimmed, "{"); start >= 0 {
		if end := strings.LastIndex(trimmed, "}"); end > start {
			if err := json.Unmarshal([]byte(trimmed[start:end+1]), &p); err == nil {
				return p.Chunks, nil
			}
		}
	}
	return nil, fmt.Errorf("llm chunker: cannot parse response as JSON")
}
