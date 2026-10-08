// gencert 產生本機開發用 self-signed TLS 憑證
//
// 使用：go run ./scripts/gencert
// 輸出：certs/server.crt、certs/server.key
//
// 憑證覆蓋：localhost / 127.0.0.1 / ::1，有效期 10 年。
// 僅供開發；上線請用正式 CA 簽發的憑證或 ACME auto_cert。
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	outDir := "certs"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatal(err)
	}
	certPath := filepath.Join(outDir, "server.crt")
	keyPath := filepath.Join(outDir, "server.key")

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatal(err)
	}

	sn, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		fatal(err)
	}

	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber: sn,
		Subject: pkix.Name{
			Organization: []string{"single_agent dev"},
			CommonName:   "localhost",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		fatal(err)
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		fatal(err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		fatal(err)
	}
	certOut.Close()

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		fatal(err)
	}
	keyOut, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		fatal(err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		fatal(err)
	}
	keyOut.Close()

	fmt.Printf("✔ wrote %s\n", certPath)
	fmt.Printf("✔ wrote %s\n", keyPath)
	fmt.Printf("  covers: localhost, 127.0.0.1, ::1\n")
	fmt.Printf("  valid until: %s\n", tmpl.NotAfter.Format(time.RFC3339))
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "gencert: %v\n", err)
	os.Exit(1)
}
