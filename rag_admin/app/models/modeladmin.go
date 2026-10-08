package models

// Modalities 一顆模型可接受的輸入內容型態（對應 agent_registry 的同名結構）。
type Modalities struct {
	Text  bool `json:"text"`
	Image bool `json:"image"`
	Audio bool `json:"audio"`
}

// LLMModel agent_registry model catalog 的一筆（GET /registry/models）。
type LLMModel struct {
	Name          string     `json:"name"`
	Host          string     `json:"host"`
	Modalities    Modalities `json:"modalities"`
	Capabilities  []string   `json:"capabilities"`
	Family        string     `json:"family,omitempty"`
	ParameterSize string     `json:"parameter_size,omitempty"`
	ContextLen    int        `json:"context_len,omitempty"`
}

// PersonaInfo agents /api/personas 的一筆：persona 的模型設定現況。
type PersonaInfo struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Role    string `json:"role,omitempty"`
	Model   string `json:"model,omitempty"`
	RoleMD  string `json:"role_md,omitempty"`
}

// ── 回應信封 payload ─────────────────────────────────────────────────────────

type ListModelsData struct {
	Models []LLMModel `json:"models"`
	Total  int        `json:"total"`
}

type SyncModelsData struct {
	Host   string     `json:"host"`
	Synced int        `json:"synced"`
	Models []LLMModel `json:"models"`
}

type ListPersonasData struct {
	Personas []PersonaInfo `json:"personas"`
	Total    int           `json:"total"`
}
