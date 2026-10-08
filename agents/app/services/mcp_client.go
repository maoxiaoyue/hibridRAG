// mcp_client.go — Model Context Protocol（MCP）stdio client。
//
// 對 MCP server 子進程做 JSON-RPC 2.0 over stdio：
//   - Initialize 握手
//   - tools/list 列出工具
//   - tools/call 執行單一工具
//
// 把每個 MCP-side tool 適配成本地 Tool（mcpTool），就能直接放進 ToolRegistry。
//
// 本實作為「初版可用」：
//   - 純粹 line-delimited JSON（每筆訊息一行，符合多數 MCP server 慣例）
//   - 同步 request/response（共用 mu，一個 request 跑完才收下一個）
//   - 不處理 notification（無 id 的 push）；MCP 1.0 工具相關都是 request 形式
package services

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// ── JSON-RPC ────────────────────────────────────────────────────────────────

type jsonrpcReq struct {
	Jsonrpc string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonrpcResp struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonrpcError) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

// ── MCP types（精簡，只取會用到的欄位）─────────────────────────────────────

// MCPToolDef MCP server 回的 tool 描述（對應 tools/list result.tools[]）。
type MCPToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// mcpCallResult 對應 tools/call result（取常見欄位）。
type mcpCallResult struct {
	Content []mcpContentItem `json:"content"`
	IsError bool             `json:"isError"`
}

type mcpContentItem struct {
	Type string `json:"type"` // "text" | "image" | "resource"
	Text string `json:"text,omitempty"`
}

// ── MCPClient ───────────────────────────────────────────────────────────────

// MCPClient 對單一 MCP server 子進程的連線。
type MCPClient struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	mu   sync.Mutex
	next atomic.Int64
}

// MCPConfig 啟動 MCP server 子進程的設定。
type MCPConfig struct {
	Name    string            // 純標籤（log 用）
	Command string            // executable path
	Args    []string          // 啟動參數
	Env     map[string]string // 額外 env vars
}

// StartMCPClient spawn 子進程並做 initialize 握手；失敗會 kill 子進程。
func StartMCPClient(ctx context.Context, cfg MCPConfig) (*MCPClient, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("mcp: command required")
	}
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		env := cmd.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp start: %w", err)
	}

	c := &MCPClient{
		cmd: cmd,
		in:  stdin,
		out: bufio.NewReader(stdout),
	}

	// initialize 握手
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := c.call(initCtx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "single_agent", "version": "0.1"},
	}); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	return c, nil
}

// Close 關 stdin → 等子進程退出（最多 3s 後 kill）。
func (c *MCPClient) Close() error {
	if c.in != nil {
		_ = c.in.Close()
	}
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		_ = c.cmd.Process.Kill()
		return fmt.Errorf("mcp: forced kill after timeout")
	}
}

// ListTools 取出 server 提供的工具清單。
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPToolDef, error) {
	raw, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Tools []MCPToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("tools/list decode: %w", err)
	}
	return r.Tools, nil
}

// CallTool 呼叫 server 端 tool；回傳合併後文字內容 + isError。
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (string, bool, error) {
	raw, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", true, err
	}
	var r mcpCallResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", true, fmt.Errorf("tools/call decode: %w", err)
	}
	// 合併所有 text content
	out := ""
	for _, c := range r.Content {
		if c.Type == "text" {
			if out != "" {
				out += "\n"
			}
			out += c.Text
		}
	}
	if out == "" {
		out = string(raw) // fallback：給 LLM 原 JSON
	}
	return out, r.IsError, nil
}

// call 同步發 request 等 response（共用 mu，序列化 IO）。
func (c *MCPClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.next.Add(1)
	req := jsonrpcReq{Jsonrpc: "2.0", ID: id, Method: method, Params: params}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if _, err := c.in.Write(body); err != nil {
		return nil, fmt.Errorf("mcp write: %w", err)
	}

	// 讀一行 response；用 ctx-aware goroutine
	type readResult struct {
		line []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := c.out.ReadBytes('\n')
		ch <- readResult{line, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil && len(r.line) == 0 {
			return nil, fmt.Errorf("mcp read: %w", r.err)
		}
		var resp jsonrpcResp
		if err := json.Unmarshal(r.line, &resp); err != nil {
			return nil, fmt.Errorf("mcp decode: %w (raw=%q)", err, r.line)
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		if resp.ID != id {
			return nil, fmt.Errorf("mcp: response id mismatch want=%d got=%d", id, resp.ID)
		}
		return resp.Result, nil
	}
}

// ── mcpTool: 將 MCPClient + ToolDef 包成本地 Tool ──────────────────────────

// mcpTool 把單一 MCP-side tool 適配成本地 Tool。
type mcpTool struct {
	client *MCPClient
	def    MCPToolDef
	schema ToolSchema
	prefix string // 顯示用 prefix，例如 server label
}

// NewMCPTool 把一個 MCP server 上的工具包成本地 Tool。
// prefix 為 0 長度時不加前綴；建議用 server 標籤避免名稱衝突。
func NewMCPTool(client *MCPClient, def MCPToolDef, prefix string) Tool {
	s := decodeMCPSchema(def.InputSchema)
	return &mcpTool{client: client, def: def, schema: s, prefix: prefix}
}

func (t *mcpTool) Name() string {
	if t.prefix == "" {
		return t.def.Name
	}
	return t.prefix + "." + t.def.Name
}
func (t *mcpTool) Description() string { return t.def.Description }
func (t *mcpTool) Schema() ToolSchema  { return t.schema }
func (t *mcpTool) Call(ctx context.Context, args map[string]any) (ToolResult, error) {
	text, isErr, err := t.client.CallTool(ctx, t.def.Name, args)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
	}
	return ToolResult{Content: text, IsError: isErr}, nil
}

// decodeMCPSchema 從 MCP inputSchema JSON 解析出 ToolSchema（best-effort）。
// MCP server 端通常給的就是 JSON Schema 形式，跟我們的 ToolSchema 結構相容。
func decodeMCPSchema(raw json.RawMessage) ToolSchema {
	if len(raw) == 0 {
		return NewObjectSchema()
	}
	var s ToolSchema
	if err := json.Unmarshal(raw, &s); err != nil || s.Type == "" {
		return NewObjectSchema()
	}
	if s.Properties == nil {
		s.Properties = map[string]ToolProperty{}
	}
	return s
}

// RegisterMCPTools 將某個 MCP server 提供的工具一次性注入 registry。
// prefix 用於避免名稱衝突；建議用 server label，例如 "fs.read_file"。
func RegisterMCPTools(ctx context.Context, reg *ToolRegistry, client *MCPClient, prefix string) (int, error) {
	defs, err := client.ListTools(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range defs {
		if err := reg.Register(NewMCPTool(client, d, prefix)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
