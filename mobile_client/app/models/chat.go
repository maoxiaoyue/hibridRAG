package models

// 對應 agent_gateway 的 ClientGateway gRPC 契約（Chat / UploadIngest / GetIngestJob）。
// 手機版不送 client_tools（本機工具不移植），但保留 tool_calls / tool_results
// 結構——server 若因其他原因回 tool_calls，engine 會逐一回錯並續推理。

// Msg 一則對話訊息。
type Msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ToolResult 工具執行結果，回送給 agent（手機版只會回「不支援」錯誤結果）。
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

// ChatRequest 對話請求。
type ChatRequest struct {
	AgentID        string       `json:"agent_id"`
	SessionID      string       `json:"session_id,omitempty"`
	Messages       []Msg        `json:"messages,omitempty"`
	ContinuationID string       `json:"continuation_id,omitempty"`
	ToolResults    []ToolResult `json:"tool_results,omitempty"`
	Temperature    float32      `json:"temperature,omitempty"`
}

// ToolCall agent 要求 client 執行的工具呼叫。
type ToolCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// Usage token 用量統計。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse 對話回應。
type ChatResponse struct {
	Status         string     `json:"status"` // "final" | "tool_calls"
	Content        string     `json:"content,omitempty"`
	SessionID      string     `json:"session_id"`
	ContinuationID string     `json:"continuation_id,omitempty"`
	ToolCalls      []ToolCall `json:"tool_calls,omitempty"`
	Usage          *Usage     `json:"usage,omitempty"`
}

// Job 攝取任務狀態。
type Job struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	TotalChunks int    `json:"total_chunks"`
	DoneChunks  int    `json:"done_chunks"`
	Error       string `json:"error"`
}

// UploadFile 一個待上傳的檔案（Android SAF 選檔拿到的是 URI 不是路徑，
// 內容在選檔當下讀進記憶體）。
type UploadFile struct {
	Name string
	Data []byte
}
