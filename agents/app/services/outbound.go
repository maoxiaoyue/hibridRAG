package services

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync"

	"agent/app/models"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Dispatcher 將 OutboundMsg 送給下游 agent。
type Dispatcher interface {
	Send(ctx context.Context, msg *models.OutboundMsg) (*models.InboundAck, error)
	Close() error
}

// ErrNoRoute 表示 OutboundMsg.To 未在 DownstreamConfig 中註冊。
var ErrNoRoute = errors.New("outbound: no route for target")

// ToInboundReq 將 OutboundMsg 映射為對端的 InboundReq。
func ToInboundReq(msg *models.OutboundMsg) *models.InboundReq {
	return &models.InboundReq{
		TraceId: msg.TraceId,
		From:    msg.From,
		Payload: msg.Payload,
	}
}

// ValidateOutbound 對 OutboundMsg 做基本檢查。
func ValidateOutbound(msg *models.OutboundMsg) error {
	if msg == nil {
		return fmt.Errorf("outbound: nil message")
	}
	if msg.To == "" {
		return fmt.Errorf("outbound: missing target (To)")
	}
	if msg.Payload == nil || len(msg.Payload.Messages) == 0 {
		return fmt.Errorf("outbound: empty payload messages")
	}
	if msg.Direction == "" {
		return fmt.Errorf("outbound: missing direction")
	}
	return nil
}

// GrpcDispatcher 按 OutboundMsg.To 查 DownstreamConfig，
// 建立 (或重用) gRPC 連線並呼叫對端 AgentService/Inbound。
type GrpcDispatcher struct {
	cfg *DownstreamConfig

	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

// NewGrpcDispatcher 建立 dispatcher；不預先 dial，連線延後到第一次 Send。
func NewGrpcDispatcher(cfg *DownstreamConfig) *GrpcDispatcher {
	return &GrpcDispatcher{
		cfg:   cfg,
		conns: make(map[string]*grpc.ClientConn),
	}
}

// Send 將 msg 推送到 cfg.Find(msg.To) 找到的下游 agent。
func (d *GrpcDispatcher) Send(ctx context.Context, msg *models.OutboundMsg) (*models.InboundAck, error) {
	if err := ValidateOutbound(msg); err != nil {
		return &models.InboundAck{
			TraceId:  traceIDOf(msg),
			Accepted: false,
			Code:     400,
			Error:    err.Error(),
		}, err
	}

	agent, ok := d.cfg.Find(msg.To)
	if !ok {
		return &models.InboundAck{
			TraceId:  msg.TraceId,
			Accepted: false,
			Code:     502,
			Error:    fmt.Sprintf("%s: %s", ErrNoRoute.Error(), msg.To),
		}, ErrNoRoute
	}

	conn, err := d.getOrDial(ctx, agent)
	if err != nil {
		return &models.InboundAck{
			TraceId:  msg.TraceId,
			Accepted: false,
			Code:     502,
			Error:    fmt.Sprintf("dial %s: %v", agent.Endpoint, err),
		}, err
	}

	// Trace propagation：把 msg.TraceId 寫入 outgoing metadata，
	// 下游 server interceptor 會取出寫回 ctx 供 handler 用。
	callCtxBase := InjectTraceID(ctx, msg.TraceId)

	client := models.NewAgentServiceClient(conn)

	// Retry：依 defaults.MaxRetries / RetryBackoff，僅對 transient err。
	ack, err := retryDo(callCtxBase, d.cfg.Defaults.MaxRetries, d.cfg.Defaults.RetryBackoff,
		func(rctx context.Context, _ int) (*models.InboundAck, error) {
			callCtx, cancel := context.WithTimeout(rctx, agent.CallTimeout)
			defer cancel()
			return client.Inbound(callCtx, ToInboundReq(msg))
		})
	if err != nil {
		code := int32(502)
		if errors.Is(err, context.DeadlineExceeded) {
			code = 504
		}
		return &models.InboundAck{
			TraceId:  msg.TraceId,
			Accepted: false,
			Code:     code,
			Error:    err.Error(),
		}, err
	}
	return ack, nil
}

func traceIDOf(msg *models.OutboundMsg) string {
	if msg == nil {
		return ""
	}
	return msg.TraceId
}

// getOrDial 取得已建立的 ClientConn，或依設定新建一條。
func (d *GrpcDispatcher) getOrDial(ctx context.Context, agent *DownstreamAgentConfig) (*grpc.ClientConn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if conn, ok := d.conns[agent.ID]; ok {
		return conn, nil
	}

	opts, err := buildDialOptions(agent, d.cfg.Defaults.InsecureSkipVerify)
	if err != nil {
		return nil, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, agent.DialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, agent.Endpoint, opts...)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", agent.Endpoint, err)
	}
	d.conns[agent.ID] = conn
	return conn, nil
}

// buildDialOptions 依 agent 設定組出 gRPC dial options (TLS / insecure)。
func buildDialOptions(agent *DownstreamAgentConfig, skipVerify bool) ([]grpc.DialOption, error) {
	var opts []grpc.DialOption
	opts = append(opts,
		grpc.WithBlock(),
		grpc.WithUnaryInterceptor(UnaryTraceClientInterceptor()),
	)

	if !agent.TLSEnabled {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		return opts, nil
	}

	tlsCfg := &tls.Config{
		ServerName:         agent.ServerName,
		InsecureSkipVerify: skipVerify,
	}
	if agent.CAFile != "" {
		pem, err := os.ReadFile(agent.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read ca %s: %w", agent.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("invalid ca pem: %s", agent.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	return opts, nil
}

// Close 關閉所有已建立的下游連線。
func (d *GrpcDispatcher) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var firstErr error
	for id, conn := range d.conns {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close %s: %w", id, err)
		}
		delete(d.conns, id)
	}
	return firstErr
}
