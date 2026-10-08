// semantic_memory.go — text-in/text-out wrapper over embed.Client + the
// agent_memory gRPC LongTermMemory RPCs. Callers never touch raw vectors:
// Write embeds the fact content, Search embeds the query, both talk to
// agent_memory's Cassandra-backed LongTermMemory (semantic memory: distilled
// facts + entity/concept knowledge, distinct from raw episodic conversation
// logs and from document RAG).
package services

import (
	"context"
	"fmt"

	"agent/app/models"
	"agent/internal/agent/embed"
)

// SemanticMemory wraps an embed.Client + MemoryGRPCClient.
type SemanticMemory struct {
	embed *embed.Client
	mem   *MemoryGRPCClient
}

// NewSemanticMemory returns nil if either dependency is nil — semantic memory
// is optional (falls back to being silently unavailable, like RAGSearcher).
func NewSemanticMemory(embedClient *embed.Client, mem *MemoryGRPCClient) *SemanticMemory {
	if embedClient == nil || mem == nil {
		return nil
	}
	return &SemanticMemory{embed: embedClient, mem: mem}
}

// Write embeds content and persists it as a distilled fact (category:
// knowledge/experience/concept/fact/preference; defaults to "knowledge").
func (s *SemanticMemory) Write(ctx context.Context, agentID, category, content, source string) (*models.LongTermMemory, error) {
	if s == nil {
		return nil, fmt.Errorf("semantic memory not configured (embedding.provider unset or agent_memory gRPC unavailable)")
	}
	r, err := s.embed.EmbedOne(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("semantic write: embed: %w", err)
	}
	return s.mem.CreateSemantic(ctx, models.CreateLongTermMemoryRequest{
		AgentID:   agentID,
		Category:  category,
		Content:   content,
		Source:    source,
		Embedding: r.Dense,
	})
}

// Search embeds query and returns the top-K most similar facts.
func (s *SemanticMemory) Search(ctx context.Context, agentID, query string, topK int) ([]models.LongTermMemoryHit, error) {
	if s == nil {
		return nil, fmt.Errorf("semantic memory not configured (embedding.provider unset or agent_memory gRPC unavailable)")
	}
	r, err := s.embed.EmbedOne(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("semantic search: embed: %w", err)
	}
	return s.mem.SearchSemantic(ctx, models.SearchLongTermMemoryRequest{
		AgentID:     agentID,
		QueryVector: r.Dense,
		TopK:        topK,
	})
}
