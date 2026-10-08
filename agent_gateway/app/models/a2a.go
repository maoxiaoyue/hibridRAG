package models

// A2A Protocol types (Google ADK compatible)

// AgentCard is returned at GET /.well-known/agent-card.json
type AgentCard struct {
	Name         string             `json:"name"`
	Description  string             `json:"description,omitempty"`
	Version      string             `json:"version"`
	URL          string             `json:"url"`
	Capabilities A2ACapabilities    `json:"capabilities"`
	Skills       []A2ASkill         `json:"skills"`
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

// ── JSON-RPC 2.0 ──────────────────────────────────────────────────────────────

type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      interface{}   `json:"id"`
	Result  interface{}   `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard JSON-RPC error codes
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
	ErrCodeNoAgent        = -32001 // custom: no agent found for skill
	ErrCodeAgentError     = -32002 // custom: downstream agent returned error
)

// ── A2A message/send params ───────────────────────────────────────────────────

type MessageSendParams struct {
	Message   A2AMessage `json:"message"`
	SkillHint string     `json:"x-skill,omitempty"`
	TaskID    string     `json:"task_id,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
}

type A2AMessage struct {
	Role    string    `json:"role"`
	Parts   []A2APart `json:"parts,omitempty"`
	Content string    `json:"content,omitempty"` // shorthand plain-text alternative to parts
}

type A2APart struct {
	Text string `json:"text,omitempty"`
	Type string `json:"type,omitempty"`
}

// ── Registry types (mirrors agent_registry models) ───────────────────────────

type RegistryAgent struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	URL          string          `json:"url"`
	GRPCEndpoint string          `json:"grpc_endpoint,omitempty"` // host:port，空表示不支援 gRPC
	Version      string          `json:"version,omitempty"`
	Skills       []RegistrySkill `json:"skills"`
	Capabilities A2ACapabilities `json:"capabilities"`
}

type RegistrySkill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type RegistrySearchResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Agents []RegistryAgent `json:"agents"`
		Total  int             `json:"total"`
	} `json:"data"`
}

type RegistryAgentResponse struct {
	Success bool          `json:"success"`
	Data    RegistryAgent `json:"data"`
}
