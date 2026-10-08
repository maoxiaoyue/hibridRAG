package eventbus

import (
	"context"
	"encoding/json"
	"testing"
)

// ── nopPublisher ────────────────────────────────────────────────────────────

func TestNopPublisher_AlwaysOK(t *testing.T) {
	p := NewNopPublisher()
	if err := p.Publish(context.Background(), "any", Event{Type: EventIngestStarted}); err != nil {
		t.Fatalf("nop publish should never error; got %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("nop close should never error; got %v", err)
	}
}

// ── Factory ────────────────────────────────────────────────────────────────

func TestNewRedisStream_NilClientFallbacksToNop(t *testing.T) {
	p := NewRedisStream(nil, Config{Source: "test"})
	// nopPublisher 不會 panic 且回 nil；用具體型別檢查兜底
	if _, ok := p.(nopPublisher); !ok {
		t.Fatalf("nil client should fall back to nopPublisher; got %T", p)
	}
	if err := p.Publish(context.Background(), "s", Event{}); err != nil {
		t.Fatal(err)
	}
}

func TestRedisStream_PublishOnDisabledStreamNameRequired(t *testing.T) {
	// 直接構造一個 enabled 但 client 為 nil 的 RedisStream，
	// 測 stream 名稱檢查邏輯（不會打到 client）。
	r := &RedisStream{enabled: true}
	err := r.Publish(context.Background(), "", Event{Type: EventIngestStarted})
	if err == nil {
		t.Fatal("empty stream should error")
	}
}

func TestRedisStream_Disabled_NoOp(t *testing.T) {
	r := &RedisStream{enabled: false}
	if err := r.Publish(context.Background(), "any", Event{}); err != nil {
		t.Fatalf("disabled should silently OK; got %v", err)
	}
}

func TestRedisStream_NilReceiver_NoOp(t *testing.T) {
	var r *RedisStream
	if err := r.Publish(context.Background(), "any", Event{}); err != nil {
		t.Fatalf("nil receiver should silently OK; got %v", err)
	}
}

func TestRedisStream_Close_DisablesFurtherPublish(t *testing.T) {
	r := &RedisStream{enabled: true}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// Close 後 enabled=false → publish 應 no-op
	if err := r.Publish(context.Background(), "s", Event{Type: EventIngestCompleted}); err != nil {
		t.Fatalf("publish after close should be no-op; got %v", err)
	}
}

// ── Event marshal sanity ────────────────────────────────────────────────────

func TestEvent_JSONShape(t *testing.T) {
	ev := Event{
		Type:    EventIngestCompleted,
		AgentID: "a1",
		TraceID: "tr1",
		Source:  "agents",
		Payload: map[string]interface{}{"chunks": 5, "doc": "x"},
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]interface{}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back["type"] != "ingest.completed" {
		t.Fatalf("type should be string-coerced; got %v", back["type"])
	}
	if back["agent_id"] != "a1" || back["trace_id"] != "tr1" {
		t.Fatalf("ids missing: %v", back)
	}
	if _, ok := back["payload"].(map[string]interface{}); !ok {
		t.Fatalf("payload shape lost: %v", back["payload"])
	}
}

func TestEventType_Constants(t *testing.T) {
	// 防止 const 名稱被改名而沒同步：靜態保證類別字串
	cases := []struct {
		got  EventType
		want string
	}{
		{EventIngestStarted, "ingest.started"},
		{EventIngestCompleted, "ingest.completed"},
		{EventIngestFailed, "ingest.failed"},
	}
	for _, tc := range cases {
		if string(tc.got) != tc.want {
			t.Fatalf("%s want %q got %q", tc.want, tc.want, tc.got)
		}
	}
}
