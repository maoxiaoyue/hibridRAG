// auth_interceptor.go — dev_pub_0.9 ClientGateway 的 API Key 認證 interceptor。
//
// 只攔 ClientGateway 的方法（/agent_gateway.v1.ClientGateway/...）——
// 舊有的 AgentService/Inbound（A2A 轉發）不走 API Key（公網部署時該入口關閉或限內網）。
package services

import (
	"context"
	"errors"
	"log"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"agent_gatway/internal/mtls"
)

const (
	clientGatewayPrefix = "/agent_gateway.v1.ClientGateway/"
	// MetaAPIKey / MetaDeviceID / MetaDeviceInfo：client 每次呼叫要帶的 metadata key
	MetaAPIKey     = "x-api-key"
	MetaDeviceID   = "x-device-id"
	MetaDeviceInfo = "x-device-info" // 選帶：綁定時記錄的裝置描述（hostname 等）
	// MetaKeyName：驗證通過後 interceptor 塞回 ctx 的 key 名稱（Ping 用）
	ctxKeyName ctxKey = "gateway-api-key-name"
)

type ctxKey string

// KeyNameFrom 取出驗證通過的 API Key 名稱（Ping handler 用）。
func KeyNameFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyName).(string); ok {
		return v
	}
	return ""
}

func authenticate(ctx context.Context, keys *APIKeyService) (context.Context, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	apiKey := firstMD(md, MetaAPIKey)
	deviceID := firstMD(md, MetaDeviceID)
	deviceInfo := firstMD(md, MetaDeviceInfo)

	ip := ""
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		ip = p.Addr.String()
	}

	if apiKey == "" || deviceID == "" {
		log.Printf("[WARN] Unauthenticated: missing x-api-key or x-device-id (peer=%s)", ip)
		return ctx, status.Error(codes.Unauthenticated, "x-api-key and x-device-id metadata are required")
	}

	name, err := keys.Authenticate(ctx, apiKey, deviceID, deviceInfo, ip)
	switch {
	case errors.Is(err, ErrDeviceMismatch):
		// Loud enough for log_watch to catch — device-mismatch usually means
		// key was leaked or an operator is trying to move it (should have used unbind).
		log.Printf("[WARN] PermissionDenied: api key bound to different device (peer=%s device_id=%s)", ip, deviceID)
		return ctx, status.Error(codes.PermissionDenied, "api key is already bound to a different device")
	case err != nil:
		log.Printf("[WARN] Unauthenticated: api key invalid or revoked (peer=%s)", ip)
		return ctx, status.Error(codes.Unauthenticated, "api key invalid or revoked")
	}
	return context.WithValue(ctx, ctxKeyName, name), nil
}

func firstMD(md metadata.MD, key string) string {
	if md == nil {
		return ""
	}
	if v := md.Get(key); len(v) > 0 {
		return strings.TrimSpace(v[0])
	}
	return ""
}

// APIKeyUnaryInterceptor enabled=false 時直通（本機開發）；keys 為 nil 時一律拒絕
//（auth.enabled=true 但 DB 沒起來——寧可拒絕也不放行）。
// trustedIPs（dev_090 內網通道，如 192.168.50.103）命中即免 API Key——
// 只該搭配「防火牆已擋外網」使用，公網不可只靠來源 IP。
func APIKeyUnaryInterceptor(enabled bool, keys *APIKeyService, trustedIPs []string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !enabled || !strings.HasPrefix(info.FullMethod, clientGatewayPrefix) {
			return handler(ctx, req)
		}
		if mtls.PeerIPTrusted(ctx, trustedIPs) {
			return handler(context.WithValue(ctx, ctxKeyName, "(trusted internal IP)"), req)
		}
		if keys == nil {
			return nil, status.Error(codes.Unavailable, "api key auth enabled but key store is unavailable")
		}
		authedCtx, err := authenticate(ctx, keys)
		if err != nil {
			return nil, err
		}
		return handler(authedCtx, req)
	}
}

// APIKeyStreamInterceptor 同 unary 版（UploadIngest 走這裡）。
func APIKeyStreamInterceptor(enabled bool, keys *APIKeyService, trustedIPs []string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !enabled || !strings.HasPrefix(info.FullMethod, clientGatewayPrefix) {
			return handler(srv, ss)
		}
		if mtls.PeerIPTrusted(ss.Context(), trustedIPs) {
			return handler(srv, &authedStream{
				ServerStream: ss,
				ctx:          context.WithValue(ss.Context(), ctxKeyName, "(trusted internal IP)"),
			})
		}
		if keys == nil {
			return status.Error(codes.Unavailable, "api key auth enabled but key store is unavailable")
		}
		authedCtx, err := authenticate(ss.Context(), keys)
		if err != nil {
			return err
		}
		return handler(srv, &authedStream{ServerStream: ss, ctx: authedCtx})
	}
}

type authedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authedStream) Context() context.Context { return s.ctx }
