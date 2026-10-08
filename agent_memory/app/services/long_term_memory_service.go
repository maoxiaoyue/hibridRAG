package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/gocql/gocql"

	"agent_memory/app/models"
)

// LongTermMemoryService 處理 Cassandra long_term_memory 的存取
type LongTermMemoryService struct {
	session *gocql.Session
}

// NewLongTermMemoryService 建構子；session 來自 internal/cassandra.Get().Session()
func NewLongTermMemoryService(session *gocql.Session) *LongTermMemoryService {
	return &LongTermMemoryService{session: session}
}

func newMemID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "ltm_" + hex.EncodeToString(b)
}

// Insert 寫入新記憶
func (s *LongTermMemoryService) Insert(ctx context.Context, req models.CreateLongTermMemoryRequest) (*models.LongTermMemory, error) {
	if req.AgentID == "" {
		return nil, fmt.Errorf("agent_id required")
	}
	if req.Content == "" {
		return nil, fmt.Errorf("content required")
	}
	if len(req.Embedding) != 1024 {
		return nil, fmt.Errorf("embedding must be 1024 dims, got %d", len(req.Embedding))
	}
	if req.Category == "" {
		req.Category = "knowledge"
	}

	now := time.Now()
	m := &models.LongTermMemory{
		MemoryID:     newMemID(),
		AgentID:      req.AgentID,
		Category:     req.Category,
		Source:       req.Source,
		Content:      req.Content,
		Summary:      req.Summary,
		Metadata:     req.Metadata,
		Embedding:    req.Embedding,
		Importance:   req.Importance,
		CreatedAt:    now,
		LastAccessed: now,
		AccessCount:  0,
	}

	err := s.session.Query(`INSERT INTO long_term_memory
		(memory_id, agent_id, category, source, content, summary, metadata,
		 embedding, importance, created_at, last_accessed, access_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.MemoryID, m.AgentID, m.Category, m.Source, m.Content, m.Summary, m.Metadata,
		CQLVector(m.Embedding), m.Importance, m.CreatedAt, m.LastAccessed, m.AccessCount,
	).WithContext(ctx).Exec()
	if err != nil {
		return nil, fmt.Errorf("insert long_term_memory: %w", err)
	}
	return m, nil
}

// Get 依 memory_id 取一筆（不回傳 embedding）
func (s *LongTermMemoryService) Get(ctx context.Context, memoryID string) (*models.LongTermMemory, error) {
	m := &models.LongTermMemory{}
	err := s.session.Query(`SELECT memory_id, agent_id, category, source, content, summary,
		metadata, importance, created_at, last_accessed, access_count
		FROM long_term_memory WHERE memory_id = ?`, memoryID,
	).WithContext(ctx).Scan(
		&m.MemoryID, &m.AgentID, &m.Category, &m.Source, &m.Content, &m.Summary,
		&m.Metadata, &m.Importance, &m.CreatedAt, &m.LastAccessed, &m.AccessCount,
	)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// SearchANN 以 BGE-M3 dense vector 做 SAI ANN 相似度搜尋；
// 命中後 fire-and-forget 更新 access_count + last_accessed。
func (s *LongTermMemoryService) SearchANN(ctx context.Context, req models.SearchLongTermMemoryRequest) ([]models.LongTermMemoryHit, error) {
	if req.AgentID == "" {
		return nil, fmt.Errorf("agent_id required")
	}
	if len(req.QueryVector) != 1024 {
		return nil, fmt.Errorf("query_vector must be 1024 dims, got %d", len(req.QueryVector))
	}
	topK := req.TopK
	if topK <= 0 {
		topK = 5
	}

	var query string
	var args []interface{}
	if req.Category != "" {
		query = `SELECT memory_id, agent_id, category, source, content, summary, metadata, importance
		         FROM long_term_memory
		         WHERE agent_id = ? AND category = ?
		         ORDER BY embedding ANN OF ? LIMIT ?`
		args = []interface{}{req.AgentID, req.Category, CQLVector(req.QueryVector), topK}
	} else {
		query = `SELECT memory_id, agent_id, category, source, content, summary, metadata, importance
		         FROM long_term_memory
		         WHERE agent_id = ?
		         ORDER BY embedding ANN OF ? LIMIT ?`
		args = []interface{}{req.AgentID, CQLVector(req.QueryVector), topK}
	}

	iter := s.session.Query(query, args...).WithContext(ctx).Iter()

	hits := make([]models.LongTermMemoryHit, 0, topK)
	for {
		var hit models.LongTermMemoryHit
		if !iter.Scan(&hit.MemoryID, &hit.AgentID, &hit.Category, &hit.Source,
			&hit.Content, &hit.Summary, &hit.Metadata, &hit.Importance) {
			break
		}
		hits = append(hits, hit)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("ann search: %w", err)
	}

	for _, h := range hits {
		go s.bumpAccessAsync(h.MemoryID)
	}
	return hits, nil
}

// UpdateAccess 同步遞增 access_count 並更新 last_accessed
func (s *LongTermMemoryService) UpdateAccess(ctx context.Context, memoryID string) error {
	var current int
	err := s.session.Query(`SELECT access_count FROM long_term_memory WHERE memory_id = ?`, memoryID).
		WithContext(ctx).Scan(&current)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return s.session.Query(`UPDATE long_term_memory
		SET access_count = ?, last_accessed = ?
		WHERE memory_id = ?`, current+1, time.Now(), memoryID).
		WithContext(ctx).Exec()
}

// bumpAccessAsync 非同步盡力更新存取統計（race condition 可接受，僅供分析）
func (s *LongTermMemoryService) bumpAccessAsync(memoryID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.UpdateAccess(ctx, memoryID)
}

// Delete 刪除一筆長期記憶
func (s *LongTermMemoryService) Delete(ctx context.Context, memoryID string) error {
	return s.session.Query(`DELETE FROM long_term_memory WHERE memory_id = ?`, memoryID).
		WithContext(ctx).Exec()
}
