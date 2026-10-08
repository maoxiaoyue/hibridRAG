package services

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestInjectAndReadTraceID(t *testing.T) {
	ctx := InjectTraceID(context.Background(), "tr_abc")
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("expected outgoing md")
	}
	if got := md.Get(TraceHeader); len(got) != 1 || got[0] != "tr_abc" {
		t.Fatalf("md=%v", md)
	}
	if TraceIDFromContext(ctx) != "tr_abc" {
		t.Fatal("TraceIDFromContext should read outgoing md")
	}
}

func TestInjectTraceID_EmptyIsNoOp(t *testing.T) {
	ctx := InjectTraceID(context.Background(), "")
	if _, ok := metadata.FromOutgoingContext(ctx); ok {
		t.Fatal("empty trace id should not create md")
	}
}

func TestUnaryTraceServerInterceptor_PropagatesAndGenerates(t *testing.T) {
	interc := UnaryTraceServerInterceptor()

	// Case 1: incoming has header → handler 看得到同樣 id
	in := metadata.New(map[string]string{TraceHeader: "tr_xyz"})
	ctx := metadata.NewIncomingContext(context.Background(), in)
	got, err := interc(ctx, nil, &grpc.UnaryServerInfo{},
		func(c context.Context, _ interface{}) (interface{}, error) {
			return TraceIDFromContext(c), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if got.(string) != "tr_xyz" {
		t.Fatalf("want propagated tr_xyz; got %v", got)
	}

	// Case 2: no incoming header → 自動生成
	got2, _ := interc(context.Background(), nil, &grpc.UnaryServerInfo{},
		func(c context.Context, _ interface{}) (interface{}, error) {
			return TraceIDFromContext(c), nil
		})
	if s := got2.(string); s == "" || len(s) < 4 {
		t.Fatalf("expected auto-generated trace id; got %q", s)
	}
}

func TestNewTraceID_FormatAndUnique(t *testing.T) {
	a := NewTraceID()
	b := NewTraceID()
	if a == b {
		t.Fatal("two calls should yield different ids")
	}
	if len(a) != len("tr_")+16 || a[:3] != "tr_" {
		t.Fatalf("bad format: %q", a)
	}
}
