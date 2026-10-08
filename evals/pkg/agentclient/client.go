// Package agentclient 是 evals runner 對 agents 服務的最小 gRPC client：
// 只用 AgentService.Chat（單輪、無 client tools），不涉及工具回合制/續推理。
package agentclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"evals/pb/agentpb"
)

// Client 對 agents 的 gRPC 連線。
type Client struct {
	conn *grpc.ClientConn
	rpc  agentpb.AgentServiceClient
}

// New 建立連線（非阻塞 dial；agents 沒起來時呼叫自然回錯）。
// addr 為 host:port（不帶 scheme）。agents 預設自簽 TLS，insecureTLS=true
// 跳過憑證驗證（比照 rag_admin/pub_client 的 InsecureTLS 慣例）。
func New(addr string, insecureTLS bool) (*Client, error) {
	creds := credentials.NewTLS(&tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecureTLS, // #nosec G402 — 開發/測試機自簽憑證
	})
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("dial agents %s: %w", addr, err)
	}
	return &Client{conn: conn, rpc: agentpb.NewAgentServiceClient(conn)}, nil
}

// Close 關閉底層連線。
func (c *Client) Close() error { return c.conn.Close() }

// ErrToolCallsUnsupported 代表 agent 這輪回了 status=tool_calls——evals runner
// 目前只支援單輪純文字問答（無 client tools），無法接續工具回合制。
// 挑到會觸發工具呼叫的案例時，改用不需要工具的 persona，或跳過該案例。
var ErrToolCallsUnsupported = fmt.Errorf("agent requested tool_calls, which the eval runner does not support")

// Ask 送一則使用者訊息（單輪、無 client tools），回傳 assistant 的最終文字。
func (c *Client) Ask(ctx context.Context, agentID, question string, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := c.rpc.Chat(cctx, &agentpb.ChatTurnReq{
		AgentId:  agentID,
		Messages: []*agentpb.Message{{Role: "user", Content: question}},
	})
	if err != nil {
		return "", fmt.Errorf("chat: %w", err)
	}
	if resp.GetStatus() == "tool_calls" {
		return "", ErrToolCallsUnsupported
	}
	return resp.GetContent(), nil
}
