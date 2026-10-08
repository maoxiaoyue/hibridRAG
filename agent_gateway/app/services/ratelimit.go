// ratelimit.go — per-source-IP token-bucket rate limiter for the
// ClientGateway gRPC surface. Sits in front of the API-key auth interceptor
// so unauthenticated floods (portscans, credential-stuffers) are dropped
// before ever touching the DB.
//
// Scope: only /agent_gateway.v1.ClientGateway/* methods (same subset the
// auth interceptor guards). AgentService/Inbound (A2A) is left alone —
// its exposure is governed separately by network binding, not by this
// limiter.
//
// Design choices worth calling out:
//
//   - Per-IP only. Per-API-key limiting would add a second bucket after
//     auth; not doing it here keeps the interceptor cheap and lets a
//     single misbehaving legitimate client burn its own quota rather
//     than starving others. Add later if abuse patterns show up.
//
//   - Trusted IPs bypass entirely (same convention as the auth path).
//     `trusted_ips` is meant for the LAN dev channel where the firewall
//     is the real boundary.
//
//   - Buckets are stored in an unbounded sync.Map keyed by IP. On a
//     public gateway the source-IP cardinality is small compared to
//     request volume (attackers reuse IPs; legitimate clients are
//     even fewer), so a background reaper isn't worth the complexity.
//     If IP churn ever explodes, add a size cap + LRU eviction.
package services

import (
	"context"
	"log"
	"net"
	"strings"
	"sync"

	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"agent_gatway/internal/mtls"
)

// IPRateLimiter tracks a token bucket per source IP. Callers use Allow to
// consume a token, returning false when the caller has exceeded its quota.
type IPRateLimiter struct {
	limiters sync.Map // string(IP) → *rate.Limiter
	rps      rate.Limit
	burst    int
}

// NewIPRateLimiter — rps is sustained requests/sec, burst is the initial
// bucket size (also the largest allowed burst). Typical: 60/120 for a
// public gateway; a legit interactive client stays well under this.
func NewIPRateLimiter(rps float64, burst int) *IPRateLimiter {
	if rps <= 0 {
		rps = 60
	}
	if burst <= 0 {
		burst = 120
	}
	return &IPRateLimiter{rps: rate.Limit(rps), burst: burst}
}

func (l *IPRateLimiter) get(ip string) *rate.Limiter {
	if lim, ok := l.limiters.Load(ip); ok {
		return lim.(*rate.Limiter)
	}
	newLim := rate.NewLimiter(l.rps, l.burst)
	actual, _ := l.limiters.LoadOrStore(ip, newLim)
	return actual.(*rate.Limiter)
}

// Allow consumes one token from the caller's bucket.
func (l *IPRateLimiter) Allow(ip string) bool {
	return l.get(ip).Allow()
}

// peerIP extracts the client's IP from the gRPC context. Empty string when
// no peer info is available (never happens for real RPCs, but guard anyway).
func peerIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return p.Addr.String()
	}
	return host
}

// RateLimitUnaryInterceptor rejects with codes.ResourceExhausted when the
// per-IP bucket is empty. Skipped for non-ClientGateway methods, for
// trusted IPs, and when the limiter is nil.
func RateLimitUnaryInterceptor(lim *IPRateLimiter, trustedIPs []string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if lim == nil || !strings.HasPrefix(info.FullMethod, clientGatewayPrefix) {
			return handler(ctx, req)
		}
		if mtls.PeerIPTrusted(ctx, trustedIPs) {
			return handler(ctx, req)
		}
		if ip := peerIP(ctx); ip != "" && !lim.Allow(ip) {
			// Log so operators/log_watch can spot floods; message format is
			// what log_watch.sh greps for ("ResourceExhausted / rate limit exceeded").
			log.Printf("[WARN] ResourceExhausted: rate limit exceeded for %s method=%s", ip, info.FullMethod)
			return nil, status.Errorf(codes.ResourceExhausted, "rate limit exceeded for %s", ip)
		}
		return handler(ctx, req)
	}
}

// RateLimitStreamInterceptor — same policy as unary. Rate-limiting a stream
// on Recv() is possible but overkill; charging one token at handshake
// mirrors what the API-key interceptor does and is simpler to reason about.
func RateLimitStreamInterceptor(lim *IPRateLimiter, trustedIPs []string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if lim == nil || !strings.HasPrefix(info.FullMethod, clientGatewayPrefix) {
			return handler(srv, ss)
		}
		if mtls.PeerIPTrusted(ss.Context(), trustedIPs) {
			return handler(srv, ss)
		}
		if ip := peerIP(ss.Context()); ip != "" && !lim.Allow(ip) {
			log.Printf("[WARN] ResourceExhausted: rate limit exceeded for %s method=%s", ip, info.FullMethod)
			return status.Errorf(codes.ResourceExhausted, "rate limit exceeded for %s", ip)
		}
		return handler(srv, ss)
	}
}
