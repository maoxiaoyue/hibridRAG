// Package eventbus provides a thin Redis Stream publisher for the
// architecture v2 Event Bus contract.
//
// 此套件與 agent_embedding/internal/eventbus 為相同設計，獨立複製是為了
// 避免兩個服務間的編譯期耦合（未來可抽出為 shared module）。
//
// 對應架構圖元件：⚡ EVENT BUS — Redis Stream
package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// EventType 預定義事件類型
type EventType string

const (
	// agents 發出的 ingest 生命週期事件
	EventIngestStarted   EventType = "ingest.started"
	EventIngestCompleted EventType = "ingest.completed"
	EventIngestFailed    EventType = "ingest.failed"

	// agents 發出的 agent 推理事件（未來擴充）
	EventAgentInbound  EventType = "agent.inbound"
	EventAgentOutbound EventType = "agent.outbound"
)

// Event 通用事件載體
type Event struct {
	Type      EventType              `json:"type"`
	AgentID   string                 `json:"agent_id,omitempty"`
	TraceID   string                 `json:"trace_id,omitempty"`
	Source    string                 `json:"source"`
	Timestamp time.Time              `json:"timestamp"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
}

// Publisher 發送端介面
type Publisher interface {
	Publish(ctx context.Context, stream string, ev Event) error
	Close() error
}

type nopPublisher struct{}

func (nopPublisher) Publish(context.Context, string, Event) error { return nil }
func (nopPublisher) Close() error                                 { return nil }

// NewNopPublisher 回傳永不發送的 publisher（測試 / disabled 用）
func NewNopPublisher() Publisher { return nopPublisher{} }

// RedisStream 以 Redis Stream（XADD）為後端的 Publisher
type RedisStream struct {
	client    *redis.Client
	source    string
	maxLen    int64
	enabled   bool
	publishTO time.Duration
}

// Config 建立 RedisStream 所需設定
type Config struct {
	Source         string
	MaxLen         int64
	PublishTimeout time.Duration
}

// NewRedisStream 用既有的 *redis.Client 建立 publisher。
// client 為 nil 時回傳 nopPublisher。
func NewRedisStream(client *redis.Client, cfg Config) Publisher {
	if client == nil {
		return nopPublisher{}
	}
	if cfg.PublishTimeout <= 0 {
		cfg.PublishTimeout = 1 * time.Second
	}
	if cfg.Source == "" {
		cfg.Source = "agents"
	}
	return &RedisStream{
		client:    client,
		source:    cfg.Source,
		maxLen:    cfg.MaxLen,
		enabled:   true,
		publishTO: cfg.PublishTimeout,
	}
}

// Publish 將事件以 XADD 寫入 stream
func (r *RedisStream) Publish(ctx context.Context, stream string, ev Event) error {
	if r == nil || !r.enabled {
		return nil
	}
	if stream == "" {
		return errors.New("eventbus: stream name required")
	}

	if ev.Source == "" {
		ev.Source = r.source
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}

	payloadJSON, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("eventbus: marshal payload: %w", err)
	}

	values := map[string]interface{}{
		"type":      string(ev.Type),
		"source":    ev.Source,
		"timestamp": ev.Timestamp.Format(time.RFC3339Nano),
		"payload":   string(payloadJSON),
	}
	if ev.AgentID != "" {
		values["agent_id"] = ev.AgentID
	}
	if ev.TraceID != "" {
		values["trace_id"] = ev.TraceID
	}

	args := &redis.XAddArgs{
		Stream: stream,
		Values: values,
	}
	if r.maxLen > 0 {
		args.MaxLen = r.maxLen
		args.Approx = true
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, r.publishTO)
	defer cancel()
	if err := r.client.XAdd(timeoutCtx, args).Err(); err != nil {
		return fmt.Errorf("eventbus: XADD %s: %w", stream, err)
	}
	return nil
}

func (r *RedisStream) Close() error {
	r.enabled = false
	return nil
}
