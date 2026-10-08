// Package eventbus provides a thin Redis Stream publisher/consumer
// implementing the architecture v2 Event Bus contract.
//
// 設計重點：
//   - 純 XADD / XREADGROUP，不引入新依賴（複用 go-redis client）
//   - Publish 失敗不阻斷主流程（best-effort，僅記 log）
//   - 訊息格式統一為 JSON payload + 平面 metadata fields
//   - Stream 名稱由呼叫端決定（例：embedding.upserted, embedding.searched）
//
// 對應架構圖元件：⚡ EVENT BUS — Redis Stream（輕量任務佇列 + 即時事件）
package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// EventType 預定義事件類型（避免拼字錯誤）
type EventType string

const (
	// agent_embedding 發出
	EventEmbeddingUpserted EventType = "embedding.upserted" // upsert 完成
	EventEmbeddingSearched EventType = "embedding.searched" // search 完成（可選追蹤）
	EventEmbeddingDeleted  EventType = "embedding.deleted"  // chunks 被刪除

	// agents 發出
	EventIngestStarted   EventType = "ingest.started"
	EventIngestCompleted EventType = "ingest.completed"
	EventIngestFailed    EventType = "ingest.failed"
)

// Event 通用事件載體；Payload 為任意 JSON-序列化結構。
type Event struct {
	Type      EventType              `json:"type"`
	AgentID   string                 `json:"agent_id,omitempty"`
	TraceID   string                 `json:"trace_id,omitempty"`
	Source    string                 `json:"source"` // 發送服務名（例如 "agent_embedding"）
	Timestamp time.Time              `json:"timestamp"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
}

// Publisher 對應「produce」箭頭的發送端。
type Publisher interface {
	// Publish 將事件寫入指定 stream。實作可選擇略過（disabled 時回 nil）。
	Publish(ctx context.Context, stream string, ev Event) error
	// Close 關閉底層連線（若有）。多次呼叫應為 no-op。
	Close() error
}

// nopPublisher 在 Redis 不可用 / disabled 時使用，所有操作皆 no-op。
type nopPublisher struct{}

func (nopPublisher) Publish(context.Context, string, Event) error { return nil }
func (nopPublisher) Close() error                                 { return nil }

// NewNopPublisher 回傳一個永不發送的 publisher（測試 / disabled 用）。
func NewNopPublisher() Publisher { return nopPublisher{} }

// RedisStream 以 Redis Stream（XADD）為後端的 Publisher 實作。
type RedisStream struct {
	client    *redis.Client
	source    string // 服務識別碼，會寫入每筆事件
	maxLen    int64  // XADD MAXLEN ~N（環形緩衝），<=0 表示不限
	enabled   bool
	publishTO time.Duration
}

// Config 建立 RedisStream 所需的設定。
type Config struct {
	// Source 寫入每筆事件的 source 欄位（例：agent_embedding / agents）
	Source string
	// MaxLen XADD MAXLEN ~ 上限；0 = 不限。建議 1萬～10萬。
	MaxLen int64
	// PublishTimeout 單筆 Publish 超時，預設 1s
	PublishTimeout time.Duration
}

// NewRedisStream 用既有的 *redis.Client 建立 publisher。
// client 為 nil 時回傳 nopPublisher（不會發事件，但呼叫端不需特判）。
func NewRedisStream(client *redis.Client, cfg Config) Publisher {
	if client == nil {
		return nopPublisher{}
	}
	if cfg.PublishTimeout <= 0 {
		cfg.PublishTimeout = 1 * time.Second
	}
	if cfg.Source == "" {
		cfg.Source = "unknown"
	}
	return &RedisStream{
		client:    client,
		source:    cfg.Source,
		maxLen:    cfg.MaxLen,
		enabled:   true,
		publishTO: cfg.PublishTimeout,
	}
}

// Publish 將事件以 XADD 寫入 stream。失敗時回傳錯誤，呼叫端決定是否 swallow。
//
// Stream entry 結構：
//
//	type:        embedding.upserted
//	agent_id:    demo
//	trace_id:    ...
//	source:      agent_embedding
//	timestamp:   2026-04-30T...Z
//	payload:     <JSON string>
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
		args.Approx = true // ~MAXLEN，允許近似上限以提升效能
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, r.publishTO)
	defer cancel()
	if err := r.client.XAdd(timeoutCtx, args).Err(); err != nil {
		return fmt.Errorf("eventbus: XADD %s: %w", stream, err)
	}
	return nil
}

// Close 不主動關閉 client（client 由呼叫端管理生命週期）。
func (r *RedisStream) Close() error {
	r.enabled = false
	return nil
}

// ---------- Consumer（preview，未在 Phase 1 主動使用） ----------

// Consumer 提供 XREADGROUP 介面，供未來 Vector Search / LLM Gateway 訂閱使用。
type Consumer struct {
	client  *redis.Client
	stream  string
	group   string
	name    string
	timeout time.Duration
}

// NewConsumer 建立一個 Consumer Group 訂閱者。
// 若 group 不存在會自動建立（MKSTREAM + XGROUP CREATE）。
func NewConsumer(client *redis.Client, stream, group, consumerName string) (*Consumer, error) {
	if client == nil {
		return nil, errors.New("eventbus: client required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// MKSTREAM：stream 不存在時自動建立，避免 NOGROUP 錯誤
	if err := client.XGroupCreateMkStream(ctx, stream, group, "$").Err(); err != nil {
		// BUSYGROUP 表示已存在，視為成功
		if err.Error() != "BUSYGROUP Consumer Group name already exists" {
			return nil, fmt.Errorf("eventbus: XGROUP CREATE: %w", err)
		}
	}
	return &Consumer{
		client:  client,
		stream:  stream,
		group:   group,
		name:    consumerName,
		timeout: 5 * time.Second,
	}, nil
}

// Read 阻塞讀取最多 count 筆未 ack 的訊息（block 表示阻塞時長，0 = 立即返回）。
func (c *Consumer) Read(ctx context.Context, count int, block time.Duration) ([]Event, []string, error) {
	streams, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    c.group,
		Consumer: c.name,
		Streams:  []string{c.stream, ">"},
		Count:    int64(count),
		Block:    block,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("eventbus: XREADGROUP: %w", err)
	}
	if len(streams) == 0 {
		return nil, nil, nil
	}

	var events []Event
	var ids []string
	for _, msg := range streams[0].Messages {
		ev, err := decodeEvent(msg.Values)
		if err != nil {
			// 跳過格式錯誤的訊息但仍 ACK 以避免 stuck
			ids = append(ids, msg.ID)
			continue
		}
		events = append(events, ev)
		ids = append(ids, msg.ID)
	}
	return events, ids, nil
}

// Ack 確認訊息已處理。
func (c *Consumer) Ack(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return c.client.XAck(ctx, c.stream, c.group, ids...).Err()
}

func decodeEvent(values map[string]interface{}) (Event, error) {
	var ev Event
	getStr := func(k string) string {
		if v, ok := values[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	ev.Type = EventType(getStr("type"))
	ev.AgentID = getStr("agent_id")
	ev.TraceID = getStr("trace_id")
	ev.Source = getStr("source")
	if ts := getStr("timestamp"); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			ev.Timestamp = t
		}
	}
	if p := getStr("payload"); p != "" {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(p), &m); err == nil {
			ev.Payload = m
		}
	}
	return ev, nil
}
