// tool_builtin.go — 三個內建 Tool：memory_read / memory_write / rag_query。
//
// 都用「介面」依賴而非具體型別，方便測試 / 之後換成 gRPC client：
//   - MemoryReader / MemoryWriter — 由 *MemoryService 滿足
//   - RAGSearcher                  — 由 vectordb.HTTPClient 或 retrieval.HybridSearcher 滿足
package services

import (
	"context"
	"fmt"

	"agent/app/models"
)

// ── 依賴介面 ────────────────────────────────────────────────────────────────

// MemoryReaderForTool 工具端只需的記憶讀取能力（避免循環匯入）。
type MemoryReaderForTool interface {
	GetPersonalByAgent(ctx context.Context, agentID string) (*models.PersonalMemory, error)
	ListProceduralByAgent(ctx context.Context, agentID string, limit int) ([]models.ProceduralMemory, error)
	ListEpisodicByAgent(ctx context.Context, agentID string, limit int) ([]models.EpisodicMemory, error)
}

// MemoryWriterForTool 工具端寫入能力（Procedural 是最常被 agent 寫的「學習筆記」）。
type MemoryWriterForTool interface {
	UpsertProcedural(ctx context.Context, req models.CreateProceduralMemoryRequest) (*models.ProceduralMemory, error)
}

// SemanticReaderForTool semantic memory（LongTermMemory ANN）讀取能力。
// 由 *SemanticMemory 滿足；nil 表示未接上 embedding 後端。
type SemanticReaderForTool interface {
	Search(ctx context.Context, agentID, query string, topK int) ([]models.LongTermMemoryHit, error)
}

// SemanticWriterForTool semantic memory 寫入能力。
type SemanticWriterForTool interface {
	Write(ctx context.Context, agentID, category, content, source string) (*models.LongTermMemory, error)
}

// SharedReaderForTool 跨 agent shared memory 讀取能力。
// 由 *MemoryGRPCClient 滿足；nil 表示走舊的直連 DB fallback（不支援 shared memory）。
type SharedReaderForTool interface {
	GetShared(ctx context.Context, poolID, key string) (*models.SharedMemory, error)
	ListShared(ctx context.Context, poolID string, limit int) ([]models.SharedMemory, error)
}

// SharedWriterForTool 跨 agent shared memory 寫入能力。
type SharedWriterForTool interface {
	UpsertShared(ctx context.Context, req models.UpsertSharedMemoryRequest) (*models.SharedMemory, error)
}

// RAGSearcherForTool 與 ctxbuilder.RAGSearcher 同形（避免循環匯入）。
type RAGSearcherForTool interface {
	SearchByQuery(ctx context.Context, agentID, query string, topK int) ([]RAGHit, error)
}

// GraphQuerierForTool agent_graph（A4 Knowledge Agent）查詢能力。
// 由 *GraphGRPCClient 滿足；nil 表示未接上 agent_graph。刻意不含 ExecuteCypher——
// 那是 agent_graph 自己 REST/gRPC 上的 admin/debug 逃生艙口，不該讓 LLM
// （可能被筆記內容 prompt injection）直接下任意 Cypher。
type GraphQuerierForTool interface {
	SearchEntities(ctx context.Context, query string, limit int) ([]GraphEntity, error)
	GetLocalContext(ctx context.Context, entityName string, hops, limit int) (GraphContext, error)
	HybridSearch(ctx context.Context, query string, topK, hops int) (GraphHybridResult, error)
	ProposeRelation(ctx context.Context, source, target, relType string, confidence float64, sourceFile, extractedBy string) (id, status string, err error)
}

// RAGHit RAG 工具回 LLM 的精簡 hit 結構。
type RAGHit struct {
	ChunkID    string  `json:"chunk_id"`
	DocID      string  `json:"doc_id"`
	Text       string  `json:"text"`
	Similarity float32 `json:"similarity"`
}

// ── memory_read ─────────────────────────────────────────────────────────────

// MemoryReadTool 讀取 agent 的 personal / procedural / episodic / semantic / shared 記憶。
type MemoryReadTool struct {
	AgentID  string // fallback；實際依 args.agent_id > ctx（WithToolAgentID）> 此欄位解析
	PoolID   string // 預設 shared memory pool；可被 args.pool_id 覆寫；空字串 → kind=shared 被擋
	Reader   MemoryReaderForTool
	Semantic SemanticReaderForTool // nil → kind=semantic 不可用
	Shared   SharedReaderForTool   // nil → kind=shared 不可用
}

func (t *MemoryReadTool) Name() string { return "memory_read" }
func (t *MemoryReadTool) Description() string {
	return "Read agent's stored memory. Supported kinds: personal (identity/style), procedural (mental models), " +
		"episodic (recent conversations), semantic (distilled facts you learned, searched by similarity to `query`), " +
		"shared (cross-agent coordination pool; omit `key` to list all entries in the pool)."
}
func (t *MemoryReadTool) Schema() ToolSchema {
	return NewObjectSchema().
		AddProperty("kind", ToolProperty{
			Type: "string", Description: "personal | procedural | episodic | semantic | shared",
			Enum: []string{"personal", "procedural", "episodic", "semantic", "shared"},
		}, true).
		AddProperty("query", ToolProperty{
			Type: "string", Description: "search query (required for kind=semantic)",
		}, false).
		AddProperty("key", ToolProperty{
			Type: "string", Description: "shared memory key (kind=shared; omit to list all keys in the pool)",
		}, false).
		AddProperty("limit", ToolProperty{
			Type: "integer", Description: "max items (procedural/episodic/semantic/shared); default 5",
			Default: 5,
		}, false).
		AddProperty("agent_id", ToolProperty{
			Type: "string", Description: "agent id override; defaults to current agent",
		}, false).
		AddProperty("pool_id", ToolProperty{
			Type: "string", Description: "shared memory pool id override (kind=shared)",
		}, false)
}
func (t *MemoryReadTool) Call(ctx context.Context, args map[string]any) (ToolResult, error) {
	kind, _ := args["kind"].(string)
	if kind == "" {
		return ToolResult{IsError: true, Content: `{"error":"kind required"}`}, nil
	}
	agentID := resolveToolAgentID(ctx, args, t.AgentID)
	if agentID == "" && kind != "shared" {
		return ToolResult{IsError: true, Content: `{"error":"agent_id required"}`}, nil
	}
	limit := 5
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	switch kind {
	case "personal", "procedural", "episodic":
		if t.Reader == nil {
			return ToolResult{IsError: true, Content: `{"error":"memory reader not configured"}`}, nil
		}
		switch kind {
		case "personal":
			pm, err := t.Reader.GetPersonalByAgent(ctx, agentID)
			if err != nil {
				return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
			}
			return ToolResult{Content: MarshalContent(pm)}, nil
		case "procedural":
			list, err := t.Reader.ListProceduralByAgent(ctx, agentID, limit)
			if err != nil {
				return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
			}
			return ToolResult{Content: MarshalContent(list)}, nil
		case "episodic":
			list, err := t.Reader.ListEpisodicByAgent(ctx, agentID, limit)
			if err != nil {
				return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
			}
			return ToolResult{Content: MarshalContent(list)}, nil
		}
	case "semantic":
		if t.Semantic == nil {
			return ToolResult{IsError: true, Content: `{"error":"semantic memory not configured"}`}, nil
		}
		query, _ := args["query"].(string)
		if query == "" {
			return ToolResult{IsError: true, Content: `{"error":"query required for kind=semantic"}`}, nil
		}
		hits, err := t.Semantic.Search(ctx, agentID, query, limit)
		if err != nil {
			return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
		}
		return ToolResult{Content: MarshalContent(hits)}, nil
	case "shared":
		if t.Shared == nil {
			return ToolResult{IsError: true, Content: `{"error":"shared memory not configured"}`}, nil
		}
		poolID := t.PoolID
		if v, ok := args["pool_id"].(string); ok && v != "" {
			poolID = v
		}
		if poolID == "" {
			return ToolResult{IsError: true, Content: `{"error":"pool_id required for kind=shared (no default configured)"}`}, nil
		}
		key, _ := args["key"].(string)
		if key == "" {
			list, err := t.Shared.ListShared(ctx, poolID, limit)
			if err != nil {
				return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
			}
			return ToolResult{Content: MarshalContent(list)}, nil
		}
		sm, err := t.Shared.GetShared(ctx, poolID, key)
		if err != nil {
			return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
		}
		return ToolResult{Content: MarshalContent(sm)}, nil
	}
	return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":"unknown kind %q"}`, kind)}, nil
}

// ── memory_write ────────────────────────────────────────────────────────────

// MemoryWriteTool 寫 procedural memory（心智模型 / 學習筆記）、semantic memory
// （學到的事實）、或 shared memory（跨 agent 協調用 KV）。
type MemoryWriteTool struct {
	AgentID  string // fallback；實際依 args.agent_id > ctx（WithToolAgentID）> 此欄位解析
	PoolID   string // 預設 shared memory pool；可被 args.pool_id 覆寫
	Writer   MemoryWriterForTool
	Semantic SemanticWriterForTool // nil → kind=semantic 不可用
	Shared   SharedWriterForTool   // nil → kind=shared 不可用
}

func (t *MemoryWriteTool) Name() string { return "memory_write" }
func (t *MemoryWriteTool) Description() string {
	return "Persist a memory. kind=procedural (default): a mental model/heuristic worth reusing. " +
		"kind=semantic: a distilled fact you learned (category: knowledge/experience/concept/fact/preference). " +
		"kind=shared: a key-value entry other agents in the same pool can read."
}
func (t *MemoryWriteTool) Schema() ToolSchema {
	return NewObjectSchema().
		AddProperty("kind", ToolProperty{
			Type: "string", Description: "procedural (default) | semantic | shared",
			Enum: []string{"procedural", "semantic", "shared"}, Default: "procedural",
		}, false).
		AddProperty("model_name", ToolProperty{
			Type: "string", Description: "short name for this mental model (kind=procedural)",
		}, false).
		AddProperty("content", ToolProperty{
			Type: "string", Description: "the rule/mental model body (kind=procedural), or the fact text (kind=semantic)",
		}, false).
		AddProperty("category", ToolProperty{
			Type: "string", Description: "knowledge | experience | concept | fact | preference (kind=semantic); default knowledge",
		}, false).
		AddProperty("key", ToolProperty{
			Type: "string", Description: "shared memory key (kind=shared)",
		}, false).
		AddProperty("value", ToolProperty{
			Type: "object", Description: "shared memory value, any JSON object (kind=shared)",
		}, false).
		AddProperty("agent_id", ToolProperty{
			Type: "string", Description: "agent id override",
		}, false).
		AddProperty("pool_id", ToolProperty{
			Type: "string", Description: "shared memory pool id override (kind=shared)",
		}, false)
}
func (t *MemoryWriteTool) Call(ctx context.Context, args map[string]any) (ToolResult, error) {
	kind, _ := args["kind"].(string)
	if kind == "" {
		kind = "procedural"
	}
	agentID := resolveToolAgentID(ctx, args, t.AgentID)
	if agentID == "" {
		return ToolResult{IsError: true, Content: `{"error":"agent_id required"}`}, nil
	}

	switch kind {
	case "procedural":
		if t.Writer == nil {
			return ToolResult{IsError: true, Content: `{"error":"memory writer not configured"}`}, nil
		}
		name, _ := args["model_name"].(string)
		content, _ := args["content"].(string)
		if name == "" || content == "" {
			return ToolResult{IsError: true, Content: `{"error":"model_name and content required"}`}, nil
		}
		m, err := t.Writer.UpsertProcedural(ctx, models.CreateProceduralMemoryRequest{
			AgentID:   agentID,
			ModelName: name,
			Content:   content,
		})
		if err != nil {
			return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
		}
		return ToolResult{Content: MarshalContent(map[string]any{
			"status": "ok", "id": m.ID, "model_name": m.ModelName,
		})}, nil

	case "semantic":
		if t.Semantic == nil {
			return ToolResult{IsError: true, Content: `{"error":"semantic memory not configured"}`}, nil
		}
		content, _ := args["content"].(string)
		if content == "" {
			return ToolResult{IsError: true, Content: `{"error":"content required for kind=semantic"}`}, nil
		}
		category, _ := args["category"].(string)
		if category == "" {
			category = "knowledge"
		}
		m, err := t.Semantic.Write(ctx, agentID, category, content, "memory_write tool")
		if err != nil {
			return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
		}
		return ToolResult{Content: MarshalContent(map[string]any{
			"status": "ok", "memory_id": m.MemoryID, "category": m.Category,
		})}, nil

	case "shared":
		if t.Shared == nil {
			return ToolResult{IsError: true, Content: `{"error":"shared memory not configured"}`}, nil
		}
		poolID := t.PoolID
		if v, ok := args["pool_id"].(string); ok && v != "" {
			poolID = v
		}
		if poolID == "" {
			return ToolResult{IsError: true, Content: `{"error":"pool_id required for kind=shared (no default configured)"}`}, nil
		}
		key, _ := args["key"].(string)
		if key == "" {
			return ToolResult{IsError: true, Content: `{"error":"key required for kind=shared"}`}, nil
		}
		value, _ := args["value"].(map[string]any)
		if value == nil {
			return ToolResult{IsError: true, Content: `{"error":"value (object) required for kind=shared"}`}, nil
		}
		m, err := t.Shared.UpsertShared(ctx, models.UpsertSharedMemoryRequest{
			PoolID: poolID, Key: key, Value: value, WrittenBy: agentID,
		})
		if err != nil {
			return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
		}
		return ToolResult{Content: MarshalContent(map[string]any{
			"status": "ok", "pool_id": m.PoolID, "key": m.Key,
		})}, nil
	}
	return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":"unknown kind %q"}`, kind)}, nil
}

// ── rag_query ───────────────────────────────────────────────────────────────

// RAGQueryTool 對 agent 的知識庫做語義檢索（dense + sparse + RRF）。
type RAGQueryTool struct {
	AgentID  string // fallback；實際依 args.agent_id > ctx（WithToolAgentID）> 此欄位解析
	Searcher RAGSearcherForTool
	DefaultK int // 0 → 5
}

func (t *RAGQueryTool) Name() string { return "rag_query" }
func (t *RAGQueryTool) Description() string {
	return "Search the agent's knowledge base for relevant chunks via hybrid semantic search."
}
func (t *RAGQueryTool) Schema() ToolSchema {
	return NewObjectSchema().
		AddProperty("query", ToolProperty{
			Type: "string", Description: "natural-language search query",
		}, true).
		AddProperty("top_k", ToolProperty{
			Type: "integer", Description: "max chunks to return; default 5", Default: 5,
		}, false).
		AddProperty("agent_id", ToolProperty{
			Type: "string", Description: "agent id override",
		}, false)
}
func (t *RAGQueryTool) Call(ctx context.Context, args map[string]any) (ToolResult, error) {
	if t.Searcher == nil {
		return ToolResult{IsError: true, Content: `{"error":"rag searcher not configured"}`}, nil
	}
	query, _ := args["query"].(string)
	if query == "" {
		return ToolResult{IsError: true, Content: `{"error":"query required"}`}, nil
	}
	agentID := resolveToolAgentID(ctx, args, t.AgentID)
	if agentID == "" {
		return ToolResult{IsError: true, Content: `{"error":"agent_id required"}`}, nil
	}
	topK := t.DefaultK
	if topK <= 0 {
		topK = 5
	}
	if v, ok := args["top_k"].(float64); ok && v > 0 {
		topK = int(v)
	}
	hits, err := t.Searcher.SearchByQuery(ctx, agentID, query, topK)
	if err != nil {
		return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
	}
	return ToolResult{Content: MarshalContent(map[string]any{
		"query": query,
		"hits":  hits,
		"count": len(hits),
	})}, nil
}

// ── graph_query ──────────────────────────────────────────────────────────────

// GraphQueryTool 查詢 agent_graph 的 Apache AGE 知識圖譜（Obsidian vault 同步而來）。
// 不像 memory_read/write，圖譜資料沒有 agent_id 分區（知識圖譜是共享的），
// 所以這個工具不經 resolveToolAgentID。
type GraphQueryTool struct {
	Querier GraphQuerierForTool // nil → not configured
}

func (t *GraphQueryTool) Name() string { return "graph_query" }
func (t *GraphQueryTool) Description() string {
	return "Query the knowledge graph synced from Obsidian notes (Apache AGE). Supported kinds: " +
		"search (substring match on entity name/description), " +
		"context (an entity's local neighborhood: direct relations + N-hop neighbors), " +
		"hybrid_search (semantic vector search for seed entities + graph expansion), " +
		"propose_relation (submit a low-confidence relation candidate for human review; does NOT modify the graph directly)."
}
func (t *GraphQueryTool) Schema() ToolSchema {
	return NewObjectSchema().
		AddProperty("kind", ToolProperty{
			Type: "string", Description: "search | context | hybrid_search | propose_relation",
			Enum: []string{"search", "context", "hybrid_search", "propose_relation"},
		}, true).
		AddProperty("query", ToolProperty{
			Type: "string", Description: "search text (kind=search/hybrid_search)",
		}, false).
		AddProperty("entity_name", ToolProperty{
			Type: "string", Description: "entity to expand (kind=context)",
		}, false).
		AddProperty("hops", ToolProperty{
			Type: "integer", Description: "traversal depth, 1-4; default 1 (kind=context/hybrid_search)", Default: 1,
		}, false).
		AddProperty("limit", ToolProperty{
			Type: "integer", Description: "max results; default 10 (kind=search/context)", Default: 10,
		}, false).
		AddProperty("top_k", ToolProperty{
			Type: "integer", Description: "seed entity count; default 5 (kind=hybrid_search)", Default: 5,
		}, false).
		AddProperty("source_entity", ToolProperty{
			Type: "string", Description: "relation source entity name (kind=propose_relation)",
		}, false).
		AddProperty("target_entity", ToolProperty{
			Type: "string", Description: "relation target entity name (kind=propose_relation)",
		}, false).
		AddProperty("relation_type", ToolProperty{
			Type: "string", Description: `relation semantics, e.g. "causes", "part_of" (kind=propose_relation)`,
		}, false).
		AddProperty("confidence", ToolProperty{
			Type: "number", Description: "0-1 certainty this relation is real (kind=propose_relation)",
		}, false)
}
func (t *GraphQueryTool) Call(ctx context.Context, args map[string]any) (ToolResult, error) {
	if t.Querier == nil {
		return ToolResult{IsError: true, Content: `{"error":"graph querier not configured"}`}, nil
	}
	kind, _ := args["kind"].(string)

	switch kind {
	case "search":
		query, _ := args["query"].(string)
		if query == "" {
			return ToolResult{IsError: true, Content: `{"error":"query required"}`}, nil
		}
		limit := 10
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		entities, err := t.Querier.SearchEntities(ctx, query, limit)
		if err != nil {
			return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
		}
		return ToolResult{Content: MarshalContent(map[string]any{"entities": entities, "count": len(entities)})}, nil

	case "context":
		name, _ := args["entity_name"].(string)
		if name == "" {
			return ToolResult{IsError: true, Content: `{"error":"entity_name required"}`}, nil
		}
		hops := 1
		if v, ok := args["hops"].(float64); ok && v > 0 {
			hops = int(v)
		}
		limit := 10
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		result, err := t.Querier.GetLocalContext(ctx, name, hops, limit)
		if err != nil {
			return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
		}
		return ToolResult{Content: MarshalContent(result)}, nil

	case "hybrid_search":
		query, _ := args["query"].(string)
		if query == "" {
			return ToolResult{IsError: true, Content: `{"error":"query required"}`}, nil
		}
		topK := 5
		if v, ok := args["top_k"].(float64); ok && v > 0 {
			topK = int(v)
		}
		hops := 1
		if v, ok := args["hops"].(float64); ok && v > 0 {
			hops = int(v)
		}
		result, err := t.Querier.HybridSearch(ctx, query, topK, hops)
		if err != nil {
			return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
		}
		return ToolResult{Content: MarshalContent(result)}, nil

	case "propose_relation":
		source, _ := args["source_entity"].(string)
		target, _ := args["target_entity"].(string)
		if source == "" || target == "" {
			return ToolResult{IsError: true, Content: `{"error":"source_entity and target_entity required"}`}, nil
		}
		relType, _ := args["relation_type"].(string)
		var confidence float64
		if v, ok := args["confidence"].(float64); ok {
			confidence = v
		}
		id, status, err := t.Querier.ProposeRelation(ctx, source, target, relType, confidence, "", "llm-tool-call")
		if err != nil {
			return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":%q}`, err.Error())}, nil
		}
		return ToolResult{Content: MarshalContent(map[string]any{"id": id, "status": status})}, nil

	default:
		return ToolResult{IsError: true, Content: fmt.Sprintf(`{"error":"unknown kind %q"}`, kind)}, nil
	}
}
