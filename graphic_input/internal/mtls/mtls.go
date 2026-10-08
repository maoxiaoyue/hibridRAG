// Package mtls — dev_pub_0.9 內部 mTLS 共用邏輯。
//
// 各服務各持一份相同拷貝（比照 Event Bus / vector_marshal 的「複製成本低於
// 共享維護成本」慣例）。行為開關：
//   - server：ca_file 空 → 舊行為（單向 TLS）；非空 → mTLS + CN 允許清單
//     interceptor（空清單 = 拒絕全部，安全預設）
//   - trusted_ips 非空 → 握手降為 VerifyClientCertIfGiven（沒憑證也能連上），
//     interceptor 改判「CN 在允許清單 **或** 來源 IP 在信任清單」——保留內網
//     開發機（dev_090 通道，如 192.168.50.103）不持憑證直連所有服務的能力
//   - client：client cert 空 → 不帶身份（只能打未開 mTLS 的 server）；
//     ca_file 空 → InsecureSkipVerify（dev 舊行為）
package mtls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// ServerCredentials 建 gRPC server 端 TLS；caFile 非空即啟用 client cert 驗證。
// allowNoClientCert=true（= 有設 trusted_ips）時握手降為 VerifyClientCertIfGiven：
// 沒帶憑證的連線能完成握手，授權交給 interceptor 依「CN 或信任 IP」判定。
func ServerCredentials(certFile, keyFile, caFile string, allowNoClientCert bool) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load server keypair: %w", err)
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pool, err := loadCertPool(caFile)
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = pool
		if allowNoClientCert {
			cfg.ClientAuth = tls.VerifyClientCertIfGiven
		} else {
			cfg.ClientAuth = tls.RequireAndVerifyClientCert
		}
	}
	return credentials.NewTLS(cfg), nil
}

// ClientCredentials 建 gRPC client 端 TLS；certFile/keyFile 非空時帶內部呼叫端身份，
// caFile 非空時用內部 CA 做完整 server 驗證（取代 InsecureSkipVerify）。
func ClientCredentials(certFile, keyFile, caFile string) (credentials.TransportCredentials, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load client keypair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if caFile != "" {
		pool, err := loadCertPool(caFile)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool
	} else {
		cfg.InsecureSkipVerify = true // dev：自簽憑證、未配置內部 CA
	}
	return credentials.NewTLS(cfg), nil
}

func loadCertPool(caFile string) (*x509.CertPool, error) {
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA %s: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("no valid certs in %s", caFile)
	}
	return pool, nil
}

// PeerCN 取出已通過 TLS 驗證的 client cert CN；非 mTLS 連線回空字串。
func PeerCN(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(ti.State.VerifiedChains) == 0 || len(ti.State.VerifiedChains[0]) == 0 {
		return ""
	}
	return ti.State.VerifiedChains[0][0].Subject.CommonName
}

// PeerIPTrusted 判斷連線來源 IP 是否在信任清單（單一 IP 或 CIDR）。
// 這是內網開發通道（dev_090）的授權依據——只該搭配「防火牆已擋外網」使用；
// IP 偽造比憑證偽造容易，公網環境不可只靠這個。
func PeerIPTrusted(ctx context.Context, trusted []string) bool {
	if len(trusted) == 0 {
		return false
	}
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return false
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		host = p.Addr.String()
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, t := range trusted {
		if strings.Contains(t, "/") {
			if _, cidr, cerr := net.ParseCIDR(t); cerr == nil && cidr.Contains(ip) {
				return true
			}
		} else if tip := net.ParseIP(t); tip != nil && tip.Equal(ip) {
			return true
		}
	}
	return false
}

func cnAllowed(cn string, allowed []string) bool {
	if cn == "" {
		return false
	}
	for _, a := range allowed {
		if a == cn {
			return true
		}
	}
	return false
}

func authorize(ctx context.Context, allowed []string, trustedIPs []string) error {
	if cn := PeerCN(ctx); cnAllowed(cn, allowed) {
		return nil
	}
	if PeerIPTrusted(ctx, trustedIPs) {
		return nil
	}
	return status.Errorf(codes.PermissionDenied,
		"caller is neither an allowlisted client CN nor a trusted internal IP")
}

// UnaryAllowlist enabled=false 時直通（未配置 mTLS 的 dev 模式）；
// enabled=true 時放行「CN 在允許清單」或「來源 IP 在信任清單」，兩者皆否 = 拒絕。
func UnaryAllowlist(enabled bool, allowed []string, trustedIPs []string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !enabled {
			return handler(ctx, req)
		}
		if err := authorize(ctx, allowed, trustedIPs); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamAllowlist 同 UnaryAllowlist 的 stream 版。
func StreamAllowlist(enabled bool, allowed []string, trustedIPs []string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !enabled {
			return handler(srv, ss)
		}
		if err := authorize(ss.Context(), allowed, trustedIPs); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}
