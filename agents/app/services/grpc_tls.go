// grpc_tls.go — 內部 gRPC 呼叫端的 TLS 選項（dev_pub_0.9 mTLS）。
package services

import (
	"crypto/tls"

	"google.golang.org/grpc/credentials"

	"agent/internal/mtls"
)

// GRPCClientTLS 內部 gRPC client 的 TLS 設定。
//
//   - CAFile 非空 → 用內部 CA 完整驗證 server（InsecureSkipVerify 無效）
//   - CAFile 空 + InsecureSkipVerify → 跳過驗證（dev 自簽憑證舊行為）
//   - ClientCertFile/KeyFile 非空 → 帶內部呼叫端身份（server 端 mTLS 允許清單比對用）
type GRPCClientTLS struct {
	InsecureSkipVerify bool
	ClientCertFile     string
	ClientKeyFile      string
	CAFile             string
}

// Credentials 產生 gRPC transport credentials。
func (t GRPCClientTLS) Credentials() (credentials.TransportCredentials, error) {
	if t.CAFile == "" && !t.InsecureSkipVerify {
		// 沒配內部 CA、也不跳過驗證 → 用系統根憑證（正式 CA 簽發的憑證用這條）
		cfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if t.ClientCertFile != "" && t.ClientKeyFile != "" {
			cert, err := tls.LoadX509KeyPair(t.ClientCertFile, t.ClientKeyFile)
			if err != nil {
				return nil, err
			}
			cfg.Certificates = []tls.Certificate{cert}
		}
		return credentials.NewTLS(cfg), nil
	}
	return mtls.ClientCredentials(t.ClientCertFile, t.ClientKeyFile, t.CAFile)
}
