// Package services — embedding_service.go: high-level orchestrator.
//
// EmbeddingService combines the EmbeddingClient (BGE-M3) and VectorDB
// (Cassandra) into a single API surface used by HTTP controllers.
package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"agent_embedding/internal/eventbus"
	"agent_embedding/internal/logger"
)

var ErrInvalidInput = errors.New("invalid input")

// EmbeddingService orchestrates embedding + storage + search.
type EmbeddingService struct {
	embedder *EmbeddingClient
	vectors  *VectorDB
	maxBatch int

	// EventBus（v2 架構）— nil 時不發送事件，主流程不受影響
	bus     eventbus.Publisher
	streams EventStreamNames
}

// EventStreamNames 由 main 注入，避免硬編碼 stream 名稱
type EventStreamNames struct {
	Upserted string
	Searched string
	Deleted  string
}

func NewEmbeddingService(embedder *EmbeddingClient, vectors *VectorDB, maxBatch int) *EmbeddingService {
	if maxBatch <= 0 {
		maxBatch = 32
	}
	return &EmbeddingService{
		embedder: embedder,
		vectors:  vectors,
		maxBatch: maxBatch,
		bus:      eventbus.NewNopPublisher(),
	}
}

// WithEventBus 注入 Event Bus publisher 與 stream 名稱（在 main 啟動時呼叫）。
// 傳入 nil 時改用 nop publisher（不發送事件）。
func (s *EmbeddingService) WithEventBus(bus eventbus.Publisher, streams EventStreamNames) *EmbeddingService {
	if bus == nil {
		bus = eventbus.NewNopPublisher()
	}
	s.bus = bus
	s.streams = streams
	return s
}

// emitEvent 發送事件，失敗時僅記 log（不阻斷主流程）
func (s *EmbeddingService) emitEvent(ctx context.Context, stream string, ev eventbus.Event) {
	if s.bus == nil || stream == "" {
		return
	}
	if err := s.bus.Publish(ctx, stream, ev); err != nil {
		logger.Warning("eventbus publish %s: %v", stream, err)
	}
}

// EmbedOne single text → dense vector (with cache hit info).
func (s *EmbeddingService) EmbedOne(ctx context.Context, text string) (vec []float32, cached bool, err error) {
	if text == "" {
		return nil, false, ErrInvalidInput
	}
	r, hit, err := s.embedder.EmbedCached(ctx, text)
	if err != nil {
		return nil, false, err
	}
	return r.Dense, hit, nil
}

// EmbedBatch batches texts → vectors in input order.
// Splits requests into chunks of maxBatch; result order is preserved.
func (s *EmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += s.maxBatch {
		end := i + s.maxBatch
		if end > len(texts) {
			end = len(texts)
		}
		results, err := s.embedder.Embed(ctx, texts[i:end])
		if err != nil {
			return nil, err
		}
		for _, r := range results {
			out = append(out, r.Dense)
		}
	}
	return out, nil
}

// UpsertItem one chunk to embed and store.
type UpsertItem struct {
	ChunkID string `json:"chunk_id,omitempty"` // optional; auto-generated if empty
	Text    string `json:"text"`
}

// UpsertChunks embed + store chunks for one (agent_id, doc_id).
// chunk_id auto-generated when missing. Returns the actually-stored chunk IDs.
func (s *EmbeddingService) UpsertChunks(ctx context.Context, agentID, docID string, items []UpsertItem) ([]string, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w: agent_id required", ErrInvalidInput)
	}
	if len(items) == 0 {
		return nil, nil
	}

	texts := make([]string, len(items))
	for i, it := range items {
		if it.Text == "" {
			return nil, fmt.Errorf("%w: items[%d].text is empty", ErrInvalidInput, i)
		}
		texts[i] = it.Text
	}

	vectors, err := s.EmbedBatch(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed batch: %w", err)
	}

	now := time.Now().UTC()
	chunks := make([]DenseChunk, len(items))
	ids := make([]string, len(items))
	for i, it := range items {
		id := it.ChunkID
		if id == "" {
			id = newChunkID()
		}
		ids[i] = id
		chunks[i] = DenseChunk{
			ChunkID:   id,
			AgentID:   agentID,
			DocID:     docID,
			Text:      it.Text,
			Model:     s.embedder.Model,
			Embedding: vectors[i],
			CreatedAt: now,
		}
	}
	if err := s.vectors.Upsert(ctx, chunks); err != nil {
		return nil, err
	}

	// 發送 embedding.upserted 事件（best-effort）
	s.emitEvent(ctx, s.streams.Upserted, eventbus.Event{
		Type:    eventbus.EventEmbeddingUpserted,
		AgentID: agentID,
		Payload: map[string]interface{}{
			"doc_id":    docID,
			"chunk_ids": ids,
			"chunk_cnt": len(ids),
			"model":     s.embedder.Model,
			"dimension": len(vectors[0]),
		},
	})
	return ids, nil
}

// SearchByText embeds the query text and runs ANN search.
func (s *EmbeddingService) SearchByText(ctx context.Context, agentID, query string, topK int) ([]SearchHit, error) {
	if agentID == "" || query == "" {
		return nil, fmt.Errorf("%w: agent_id and query required", ErrInvalidInput)
	}
	r, _, err := s.embedder.EmbedCached(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	hits, err := s.vectors.SearchANN(ctx, agentID, r.Dense, topK)
	if err != nil {
		return nil, err
	}
	// 發送 embedding.searched 事件（best-effort，不含 query 內容）
	s.emitEvent(ctx, s.streams.Searched, eventbus.Event{
		Type:    eventbus.EventEmbeddingSearched,
		AgentID: agentID,
		Payload: map[string]interface{}{
			"top_k":     topK,
			"hits":      len(hits),
			"query_len": len(query),
		},
	})
	return hits, nil
}

// SearchByVector ANN search using a pre-computed vector.
func (s *EmbeddingService) SearchByVector(ctx context.Context, agentID string, vec []float32, topK int) ([]SearchHit, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w: agent_id required", ErrInvalidInput)
	}
	if len(vec) == 0 {
		return nil, fmt.Errorf("%w: vector required", ErrInvalidInput)
	}
	return s.vectors.SearchANN(ctx, agentID, vec, topK)
}

// DeleteByDoc removes all chunks for a doc.
func (s *EmbeddingService) DeleteByDoc(ctx context.Context, agentID, docID string) (int, error) {
	n, err := s.vectors.DeleteByDoc(ctx, agentID, docID)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.emitEvent(ctx, s.streams.Deleted, eventbus.Event{
			Type:    eventbus.EventEmbeddingDeleted,
			AgentID: agentID,
			Payload: map[string]interface{}{
				"doc_id":  docID,
				"deleted": n,
			},
		})
	}
	return n, nil
}

// Embedder returns the underlying client (for health checks).
func (s *EmbeddingService) Embedder() *EmbeddingClient { return s.embedder }

// Vectors returns the underlying vector DB.
func (s *EmbeddingService) Vectors() *VectorDB { return s.vectors }

func newChunkID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "ck_" + hex.EncodeToString(b)
}
