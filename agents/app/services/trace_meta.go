// trace_meta.go — 輕量 trace_id 傳播（不依賴 OpenTelemetry SDK）。
//
// 設計選擇：
//   完整 OTel SDK 需要 exporter、resource、sampler 設置，本階段過重。
//   先用 gRPC metadata 傳一個 "x-trace-id" header，做到上下游 log correlation；
//   日後接 OTel 時把 interceptor 換成 otelgrpc.UnaryClient/ServerInterceptor 即可。
//
// 用法：
//   - 上游 client（GrpcDispatcher）：用 InjectTraceID(ctx, msg.TraceId) 帶入 metadata
//   - 下游 server（gRPC server）：用 UnaryTraceServerInterceptor 把 header → context；
//     handler 內以 TraceIDFromContext(ctx) 取出
package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// TraceHeader 是 metadata 中的 trace_id key。
// gRPC 規範要求 metadata key 全小寫；以 "x-" 起頭表自訂。
const TraceHeader = "x-trace-id"

// traceCtxKey 是把 trace_id 放回 ctx 的私有 key。
type traceCtxKey struct{}

// InjectTraceID 將 traceID 寫入 outgoing gRPC metadata；空字串時不動。
func InjectTraceID(ctx context.Context, traceID string) context.Context {
	if traceID == "" {
		return ctx
	}
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	} else {
		md = md.Copy()
	}
	md.Set(TraceHeader, traceID)
	return metadata.NewOutgoingContext(ctx, md)
}

// TraceIDFromContext 從 ctx（server-side 已注入 / client-side outgoing 都可）取出 trace_id。
func TraceIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(traceCtxKey{}).(string); ok && v != "" {
		return v
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(TraceHeader); len(v) > 0 && v[0] != "" {
			return v[0]
		}
	}
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		if v := md.Get(TraceHeader); len(v) > 0 && v[0] != "" {
			return v[0]
		}
	}
	return ""
}

// NewTraceID 生成 "tr_<16 hex>" 形式的 trace_id；caller 端 / 邊界處用。
func NewTraceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "tr_" + hex.EncodeToString(b)
}

// UnaryTraceServerInterceptor 是 gRPC server-side interceptor：
//   - 從 incoming metadata 取 x-trace-id；不存在則自動生成
//   - 寫回 ctx，handler 可用 TraceIDFromContext 直接取出
func UnaryTraceServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		traceID := ""
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if v := md.Get(TraceHeader); len(v) > 0 {
				traceID = v[0]
			}
		}
		if traceID == "" {
			traceID = NewTraceID()
		}
		ctx = context.WithValue(ctx, traceCtxKey{}, traceID)
		return handler(ctx, req)
	}
}

// UnaryTraceClientInterceptor 是 gRPC client-side interceptor：
//   - 從 ctx 取出 trace_id（若被 InjectTraceID / TraceIDFromContext 設過）→ 注入 outgoing metadata
//   - 否則 no-op（由 caller 自行 InjectTraceID）
func UnaryTraceClientInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if tid := TraceIDFromContext(ctx); tid != "" {
			ctx = InjectTraceID(ctx, tid)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
