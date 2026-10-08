package models

// ── A2A JSON-RPC 2.0 types (subset used by the agents A2A endpoint) ──────────

type A2ARequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type A2AResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *A2AError   `json:"error,omitempty"`
}

type A2AError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MessageSendParams A2A message/send request params
type MessageSendParams struct {
	Message   A2AMessage `json:"message"`
	SkillHint string     `json:"x-skill,omitempty"`
	TaskID    string     `json:"task_id,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
}

type A2AMessage struct {
	Role  string    `json:"role"`
	Parts []A2APart `json:"parts,omitempty"`
	// plain text shorthand (some clients send this instead of parts)
	Content string `json:"content,omitempty"`
}

type A2APart struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

// MessageSendResult A2A message/send response result
type MessageSendResult struct {
	TaskID    string `json:"task_id"`
	MessageID string `json:"message_id"`
	Status    string `json:"status"` // "submitted"
}

// AgentCard A2A agent capability declaration
type AgentCard struct {
	Name         string             `json:"name"`
	Description  string             `json:"description,omitempty"`
	Version      string             `json:"version,omitempty"`
	URL          string             `json:"url,omitempty"`
	Capabilities A2ACapabilities    `json:"capabilities"`
	Skills       []A2ASkill         `json:"skills,omitempty"`
}

type A2ACapabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"push_notifications"`
}

type A2ASkill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// A2A JSON-RPC error codes
const (
	A2AErrParse          = -32700
	A2AErrInvalidRequest = -32600
	A2AErrMethodNotFound = -32601
	A2AErrInvalidParams  = -32602
	A2AErrInternal       = -32603
)
