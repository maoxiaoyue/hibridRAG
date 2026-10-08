package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaChatWithTools_RequestShape(t *testing.T) {
	var gotReq ollamaChatToolsReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotReq)
		_, _ = w.Write([]byte(`{
			"model":"llama3.1",
			"message":{"role":"assistant","content":"hi"},
			"done":true,"done_reason":"stop",
			"prompt_eval_count":10,"eval_count":2
		}`))
	}))
	defer srv.Close()

	c := NewOllamaClient(OllamaConfig{URL: srv.URL, Model: "llama3.1", Timeout: 5})
	resp, err := c.ChatWithTools(context.Background(),
		ChatRequest{Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}}},
		[]Tool{&fakeTool{name: "echo"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hi" || len(resp.ToolCalls) != 0 {
		t.Fatalf("bad resp: %+v", resp)
	}
	if len(gotReq.Tools) != 1 || gotReq.Tools[0].Function.Name != "echo" {
		t.Fatalf("tools not encoded: %+v", gotReq.Tools)
	}
	if gotReq.Tools[0].Type != "function" {
		t.Fatalf("type want 'function' got %q", gotReq.Tools[0].Type)
	}
}

func TestOllamaChatWithTools_ParsesToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"model":"llama3.1",
			"message":{
				"role":"assistant","content":"",
				"tool_calls":[
					{"function":{"name":"rag_query","arguments":{"query":"foo","top_k":3}}},
					{"function":{"name":"memory_read","arguments":{"kind":"personal"}}}
				]
			},
			"done":true
		}`))
	}))
	defer srv.Close()

	c := NewOllamaClient(OllamaConfig{URL: srv.URL, Model: "llama3.1", Timeout: 5})
	resp, err := c.ChatWithTools(context.Background(),
		ChatRequest{Messages: []ChatMessage{{Role: RoleUser, Content: "q"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("want 2 tool_calls; got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "rag_query" || resp.ToolCalls[0].Args["query"] != "foo" {
		t.Fatalf("first call wrong: %+v", resp.ToolCalls[0])
	}
	if resp.ToolCalls[0].ID == "" || resp.ToolCalls[1].ID == resp.ToolCalls[0].ID {
		t.Fatalf("ids should be synthesized and unique: %v / %v", resp.ToolCalls[0].ID, resp.ToolCalls[1].ID)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish should default to tool_calls when calls present; got %q", resp.FinishReason)
	}
}

func TestParseToolResultPrefix(t *testing.T) {
	n, b, ok := parseToolResultPrefix("[tool_result name=rag_query status=ok]\n{\"hits\":[]}")
	if !ok || n != "rag_query" || b != `{"hits":[]}` {
		t.Fatalf("parse failed: ok=%v n=%q b=%q", ok, n, b)
	}
	n, b, ok = parseToolResultPrefix("[tool_result name=x error=\"bad\"]\nooops")
	if !ok || n != "x" || b != "ooops" {
		t.Fatalf("parse error variant: ok=%v n=%q b=%q", ok, n, b)
	}
	if _, _, ok := parseToolResultPrefix("plain user message"); ok {
		t.Fatal("non-tool-result string must not match")
	}
}

func TestConvertChatMessages_ToolRoleRouting(t *testing.T) {
	in := []ChatMessage{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "thinking..."},
		{Role: RoleUser, Content: "[tool_result name=rag_query status=ok]\n{\"a\":1}"},
	}
	out := convertChatMessagesToOllama(in)
	if len(out) != 4 {
		t.Fatalf("len mismatch: %d", len(out))
	}
	if out[3].Role != "tool" || out[3].Name != "rag_query" || !strings.Contains(out[3].Content, `"a":1`) {
		t.Fatalf("tool message not routed: %+v", out[3])
	}
	// 純 user 不被誤判
	if out[1].Role != "user" || out[1].Name != "" {
		t.Fatalf("user message altered: %+v", out[1])
	}
}

// End-to-end with RunToolLoop using OllamaClient backed by httptest.
func TestRunToolLoop_WithOllama(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`{
				"model":"m","done":true,
				"message":{"role":"assistant","content":"",
					"tool_calls":[{"function":{"name":"echo","arguments":{}}}]}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"model":"m","done":true,"done_reason":"stop",
			"message":{"role":"assistant","content":"final-answer"}
		}`))
	}))
	defer srv.Close()

	c := NewOllamaClient(OllamaConfig{URL: srv.URL, Model: "m", Timeout: 5})
	reg := NewToolRegistry()
	_ = reg.Register(&fakeTool{name: "echo"})

	out, _, traces, err := RunToolLoop(context.Background(), c, reg,
		[]ChatMessage{{Role: RoleUser, Content: "hi"}}, reg.List(), ToolLoopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "final-answer" {
		t.Fatalf("want final-answer got %q", out)
	}
	if len(traces) != 1 || traces[0].Name != "echo" || !traces[0].OK {
		t.Fatalf("traces wrong: %+v", traces)
	}
	if calls != 2 {
		t.Fatalf("want 2 llm calls; got %d", calls)
	}
}
