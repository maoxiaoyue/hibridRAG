package services

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/net/http2"

	"agent_gatway/app/models"
)

// A2AClient forwards JSON-RPC requests to a downstream A2A agent
type A2AClient struct {
	http *http.Client
}

// A2AClientOptions TLS and transport options for the A2A HTTP client.
type A2AClientOptions struct {
	InsecureSkipVerify bool
	CAFile             string
}

func NewA2AClient(timeoutSec int, opts A2AClientOptions) *A2AClient {
	if timeoutSec <= 0 {
		timeoutSec = 30
	}

	tlsCfg := &tls.Config{
		InsecureSkipVerify: opts.InsecureSkipVerify, //nolint:gosec
	}

	if opts.CAFile != "" {
		pem, err := os.ReadFile(opts.CAFile)
		if err == nil {
			pool := x509.NewCertPool()
			if pool.AppendCertsFromPEM(pem) {
				tlsCfg.RootCAs = pool
				tlsCfg.InsecureSkipVerify = false
			}
		}
	}

	// Force HTTP/2 over TLS via ALPN (h2).
	// http2.ConfigureTransport upgrades a plain http.Transport to speak HTTP/2.
	t1 := &http.Transport{TLSClientConfig: tlsCfg}
	_ = http2.ConfigureTransport(t1)

	return &A2AClient{
		http: &http.Client{
			Timeout:   time.Duration(timeoutSec) * time.Second,
			Transport: t1,
		},
	}
}

// Forward sends a JSON-RPC request to the given agent URL and returns its response.
// The caller is responsible for setting the task_id in params before calling.
func (c *A2AClient) Forward(ctx context.Context, agentURL string, req *models.JSONRPCRequest) (*models.JSONRPCResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("forward to agent: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var rpcResp models.JSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("decode agent response (status=%d): %w", resp.StatusCode, err)
	}
	return &rpcResp, nil
}
