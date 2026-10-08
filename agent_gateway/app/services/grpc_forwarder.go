package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"

	"agent_gatway/app/models"
	"agent_gatway/internal/mtls"
)

// InternalTLS gateway 對內撥出的 mTLS 憑證設定（dev_pub_0.9）。
// 三個欄位全空 = 舊行為（InsecureSkipVerify、不帶呼叫端身份）。
type InternalTLS struct {
	ClientCertFile string
	ClientKeyFile  string
	CAFile         string
}

// GRPCForwarder 維護到下游 agent 的 gRPC 連線池，轉發 InboundReq
type GRPCForwarder struct {
	mu          sync.Mutex
	conns       map[string]*grpc.ClientConn // keyed by endpoint (host:port)
	dialTimeout time.Duration
	tlsOpt      InternalTLS
}

func NewGRPCForwarder(dialTimeoutSec int, tlsOpt InternalTLS) *GRPCForwarder {
	if dialTimeoutSec <= 0 {
		dialTimeoutSec = 5
	}
	return &GRPCForwarder{
		conns:       make(map[string]*grpc.ClientConn),
		dialTimeout: time.Duration(dialTimeoutSec) * time.Second,
		tlsOpt:      tlsOpt,
	}
}

// Forward sends an InboundReq to the downstream agent at endpoint and returns its InboundAck.
func (f *GRPCForwarder) Forward(ctx context.Context, endpoint string, req *models.InboundReq) (*models.InboundAck, error) {
	conn, err := f.getConn(endpoint)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", endpoint, err)
	}

	client := models.NewAgentServiceClient(conn)
	ack, err := client.Inbound(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("grpc inbound %s: %w", endpoint, err)
	}
	return ack, nil
}

// Close closes all pooled connections
func (f *GRPCForwarder) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, conn := range f.conns {
		conn.Close()
	}
	f.conns = make(map[string]*grpc.ClientConn)
}

func (f *GRPCForwarder) getConn(endpoint string) (*grpc.ClientConn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if conn, ok := f.conns[endpoint]; ok {
		return conn, nil
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), f.dialTimeout)
	defer cancel()

	// dev_pub_0.9：帶 gateway 的內部 client cert（CN=agent_gateway）撥往下游；
	// CAFile 空時退回 InsecureSkipVerify（dev 舊行為，見 mtls.ClientCredentials）。
	creds, err := mtls.ClientCredentials(f.tlsOpt.ClientCertFile, f.tlsOpt.ClientKeyFile, f.tlsOpt.CAFile)
	if err != nil {
		return nil, fmt.Errorf("internal tls: %w", err)
	}
	conn, err := grpc.DialContext(dialCtx, endpoint, //nolint:staticcheck
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, err
	}

	f.conns[endpoint] = conn
	return conn, nil
}
