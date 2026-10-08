// tool_web.go — web_browse 工具：用遠端瀏覽器抓網頁，工具端先用 LLM 摘要/抽取重點
// 再回主對話（依使用者決策：分析並優化 = 工具端先濃縮再回，避免整頁原文爆 context）。
package services

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// WebBrowseTool 讓 LLM 上網查資料：抓頁 → 摘要 → 回。
type WebBrowseTool struct {
	Fetcher      *BrowserFetcher // nil → 未設定（不會被註冊）
	Summarizer   LLMClient       // 工具端摘要用；nil → 直接回截斷後的原文
	MaxBodyChars int             // 送摘要前的 body 上限；<=0 → 12000
}

func (t *WebBrowseTool) Name() string { return "web_browse" }

func (t *WebBrowseTool) Description() string {
	return "Fetch a web page through a headless browser (renders JavaScript) and return a focused, " +
		"faithful summary of its main text. Use this to look up current information on the internet. " +
		"Provide 'url'; optionally 'focus' describing what to extract."
}

func (t *WebBrowseTool) Schema() ToolSchema {
	return NewObjectSchema().
		AddProperty("url", ToolProperty{
			Type: "string", Description: "the page URL to fetch (http/https)",
		}, true).
		AddProperty("focus", ToolProperty{
			Type: "string", Description: "what to look for / extract from the page (optional)",
		}, false)
}

func (t *WebBrowseTool) Call(ctx context.Context, args map[string]any) (ToolResult, error) {
	if t.Fetcher == nil {
		return ToolResult{IsError: true, Content: `{"error":"web browser not configured"}`}, nil
	}
	url, _ := args["url"].(string)
	url = strings.TrimSpace(url)
	if url == "" {
		return ToolResult{IsError: true, Content: `{"error":"url required"}`}, nil
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}
	focus, _ := args["focus"].(string)

	log.Printf("[web_browse] call url=%s focus=%q", url, focus)
	started := time.Now()

	page, err := t.Fetcher.Fetch(ctx, url)
	if err != nil {
		log.Printf("[web_browse] call fail url=%s elapsed=%s err=%v", url, time.Since(started), err)
		return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
	}

	body := page.BodyText
	max := t.MaxBodyChars
	if max <= 0 {
		max = 12000
	}
	truncated := false
	if r := []rune(body); len(r) > max {
		body = string(r[:max])
		truncated = true
	}

	// 工具端摘要/抽取（有 Summarizer 才做；失敗或無 LLM → 退回截斷原文）。
	content := body
	if t.Summarizer != nil && strings.TrimSpace(body) != "" {
		if s, serr := t.summarize(ctx, page.Title, url, focus, body); serr == nil && strings.TrimSpace(s) != "" {
			content = s
		}
	}

	summarized := t.Summarizer != nil && content != body
	log.Printf("[web_browse] call ok url=%s elapsed=%s title=%q body_bytes=%d truncated=%v summarized=%v",
		url, time.Since(started), page.Title, len(page.BodyText), truncated, summarized)

	return ToolResult{Content: MarshalContent(map[string]any{
		"url":       url,
		"title":     page.Title,
		"summary":   content,
		"truncated": truncated,
	})}, nil
}

func (t *WebBrowseTool) summarize(ctx context.Context, title, url, focus, body string) (string, error) {
	instruction := "你是網頁內容分析器。把提供的網頁純文字整理成精簡、忠實的重點摘要（以條列為主），" +
		"只根據內文、不杜撰、不臆測。"
	if strings.TrimSpace(focus) != "" {
		instruction += "特別聚焦於：" + focus + "。"
	}
	user := fmt.Sprintf("網址：%s\n標題：%s\n\n內文：\n%s", url, title, body)
	resp, err := t.Summarizer.Chat(ctx, ChatRequest{Messages: []ChatMessage{
		{Role: RoleSystem, Content: instruction},
		{Role: RoleUser, Content: user},
	}})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}
