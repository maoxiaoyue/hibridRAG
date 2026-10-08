package controllers

import "agent_embedding/app/services"

type H map[string]interface{}

var (
	svc          *services.EmbeddingService
	hybridSearch *services.HybridSearcher // nil → /api/search/hybrid 回 503
)

// Init wires the controller singleton.
func Init(s *services.EmbeddingService) { svc = s }

// InitHybrid wires the HybridSearcher (optional; only used if PG is configured).
func InitHybrid(h *services.HybridSearcher) { hybridSearch = h }
