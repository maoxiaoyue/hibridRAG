package models

import "agent_embedding/app/services"

// ── Single embed ────────────────────────────────────────────────────────────

type EmbedRequest struct {
	Text string `json:"text"`
}

type EmbedResponse struct {
	Vector    []float32 `json:"vector"`
	Dimension int       `json:"dimension"`
	Model     string    `json:"model"`
	Cached    bool      `json:"cached"`
}

// ── Batch embed ─────────────────────────────────────────────────────────────

type EmbedBatchRequest struct {
	Texts []string `json:"texts"`
}

type EmbedBatchResponse struct {
	Vectors   [][]float32 `json:"vectors"`
	Dimension int         `json:"dimension"`
	Model     string      `json:"model"`
	Count     int         `json:"count"`
}

// ── Upsert ──────────────────────────────────────────────────────────────────

type UpsertRequest struct {
	AgentID string                  `json:"agent_id"`
	DocID   string                  `json:"doc_id"`
	Chunks  []services.UpsertItem   `json:"chunks"`
}

type UpsertResponse struct {
	Upserted int      `json:"upserted"`
	ChunkIDs []string `json:"chunk_ids"`
}
