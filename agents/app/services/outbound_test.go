package services

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	"agent/app/models"
)

// ── ValidateOutbound ───────────────────────────────────────────────────────

func TestValidateOutbound(t *testing.T) {
	good := &models.OutboundMsg{
		To: "b", Direction: "reply", From: "a",
		Payload: &models.AgentReq{Messages: []*models.Message{{Role: "user", Content: "x"}}},
	}
	if err := ValidateOutbound(good); err != nil {
		t.Fatalf("good msg should pass: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*models.OutboundMsg)
	}{
		{"nil", func(m *models.OutboundMsg) { *m = models.OutboundMsg{} }},
		{"no-to", func(m *models.OutboundMsg) { m.To = "" }},
		{"no-direction", func(m *models.OutboundMsg) { m.Direction = "" }},
		{"empty-payload", func(m *models.OutboundMsg) { m.Payload = nil }},
		{"empty-messages", func(m *models.OutboundMsg) { m.Payload.Messages = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := *good
			pl := *good.Payload
			msg.Payload = &pl
			tc.mut(&msg)
			if tc.name == "nil" {
				if err := ValidateOutbound(nil); err == nil {
					t.Fatal("nil should error")
				}
				return
			}
			if err := ValidateOutbound(&msg); err == nil {
				t.Fatalf("%s should error", tc.name)
			}
		})
	}
}

func TestToInboundReq(t *testing.T) {
	out := &models.OutboundMsg{
		TraceId: "tr1", From: "a", To: "b",
		Payload: &models.AgentReq{AgentId: "b", Messages: []*models.Message{{Role: "user", Content: "x"}}},
	}
	in := ToInboundReq(out)
	if in.TraceId != "tr1" || in.From != "a" || in.Payload != out.Payload {
		t.Fatalf("mapping wrong: %+v", in)
	}
}

// ── ErrNoRoute via GrpcDispatcher.Send ─────────────────────────────────────

func TestGrpcDispatcher_NoRoute(t *testing.T) {
	cfg := &DownstreamConfig{}
	cfg.ApplyDefaults()
	d := NewGrpcDispatcher(cfg)
	defer d.Close()

	ack, err := d.Send(context.Background(), &models.OutboundMsg{
		TraceId: "tr1", From: "a", To: "unknown", Direction: "reply",
		Payload: &models.AgentReq{Messages: []*models.Message{{Role: "user", Content: "x"}}},
	})
	if !errors.Is(err, ErrNoRoute) {
		t.Fatalf("want ErrNoRoute, got %v", err)
	}
	if ack == nil || ack.Code != 502 || ack.Accepted {
		t.Fatalf("ack wrong: %+v", ack)
	}
}

func TestGrpcDispatcher_ValidationFails(t *testing.T) {
	d := NewGrpcDispatcher(&DownstreamConfig{})
	defer d.Close()

	ack, err := d.Send(context.Background(), &models.OutboundMsg{
		To: "", From: "a", Direction: "reply",
		Payload: &models.AgentReq{Messages: []*models.Message{{Role: "user", Content: "x"}}},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if ack == nil || ack.Code != 400 {
		t.Fatalf("ack wrong: %+v", ack)
	}
}

// ── GrpcDispatcher.Send with live in-proc server (success + retry path) ───

// stubAgentServer is a minimal models.AgentServiceServer for in-proc testing.
type stubAgentServer struct {
	models.UnimplementedAgentServiceServer
	calls int
}

func (s *stubAgentServer) Inbound(_ context.Context, req *models.InboundReq) (*models.InboundAck, error) {
	s.calls++
	return &models.InboundAck{TraceId: req.TraceId, MessageId: "msg_x", Accepted: true, Code: 0}, nil
}

// startInProcServer：bind 一個 random TCP port + 啟動 gRPC server，回 endpoint + 停止函式。
func startInProcServer(t *testing.T) (endpoint string, stop func(), srvImpl *stubAgentServer) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	impl := &stubAgentServer{}
	models.RegisterAgentServiceServer(srv, impl)
	go func() { _ = srv.Serve(lis) }()
	return lis.Addr().String(), func() {
		srv.Stop()
		_ = lis.Close()
	}, impl
}

func TestGrpcDispatcher_Send_Success(t *testing.T) {
	endpoint, stop, impl := startInProcServer(t)
	defer stop()

	cfg := &DownstreamConfig{
		Defaults: DownstreamDefaults{
			DialTimeout: 2 * time.Second, CallTimeout: 2 * time.Second,
		},
		Agents: []DownstreamAgentConfig{
			{ID: "down1", Endpoint: endpoint},
		},
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	d := NewGrpcDispatcher(cfg)
	defer d.Close()

	ack, err := d.Send(context.Background(), &models.OutboundMsg{
		TraceId: "tr1", From: "a", To: "down1", Direction: "reply",
		Payload: &models.AgentReq{AgentId: "down1", Messages: []*models.Message{{Role: "user", Content: "hi"}}},
	})
	if err != nil {
		t.Fatalf("send error: %v", err)
	}
	if !ack.Accepted || ack.Code != 0 {
		t.Fatalf("ack wrong: %+v", ack)
	}
	if impl.calls != 1 {
		t.Fatalf("downstream not invoked exactly once; calls=%d", impl.calls)
	}

	// 連線應被快取（再次 Send 不應重 dial）
	_, _ = d.Send(context.Background(), &models.OutboundMsg{
		TraceId: "tr2", From: "a", To: "down1", Direction: "reply",
		Payload: &models.AgentReq{AgentId: "down1", Messages: []*models.Message{{Role: "user", Content: "hi"}}},
	})
	if impl.calls != 2 {
		t.Fatalf("second call lost; calls=%d", impl.calls)
	}
}

func TestGrpcDispatcher_DialFailureReturns502(t *testing.T) {
	cfg := &DownstreamConfig{
		Defaults: DownstreamDefaults{DialTimeout: 200 * time.Millisecond, CallTimeout: 200 * time.Millisecond},
		Agents:   []DownstreamAgentConfig{{ID: "down1", Endpoint: "127.0.0.1:1"}}, // 1 號 port 連不上
	}
	cfg.ApplyDefaults()
	d := NewGrpcDispatcher(cfg)
	defer d.Close()

	ack, err := d.Send(context.Background(), &models.OutboundMsg{
		TraceId: "tr1", From: "a", To: "down1", Direction: "reply",
		Payload: &models.AgentReq{Messages: []*models.Message{{Role: "user", Content: "x"}}},
	})
	if err == nil {
		t.Fatal("expected dial error")
	}
	if ack == nil || ack.Code != 502 {
		t.Fatalf("ack wrong: %+v", ack)
	}
}
