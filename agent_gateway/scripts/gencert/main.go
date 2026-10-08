// gencert（dev_pub_0.9 兩段式版）— 產生內部 CA + 全部服務的 server/client 憑證
//
// 使用（在 agent_gateway/ 目錄下執行，-root 指到 repo 根）：
//
//	go run ./scripts/gencert -root ..
//	go run ./scripts/gencert -root .. -dns myhost.example.com -ip 192.168.1.10   # 部署機加對外 SAN
//
// 輸出：
//
//	<root>/certs-internal/internal-ca.{crt,key}   內部 CA（只存在於部署機，不進 git）
//	<root>/<svc>/certs/server.{crt,key}           各服務 gRPC/HTTP server 憑證（內部 CA 簽發）
//	<root>/<svc>/certs/client.{crt,key}           各服務對內撥出用 client 憑證（CN=<svc>）
//	<root>/<svc>/certs/internal-ca.crt            信任錨副本（驗對方憑證用）
//
// 內部 mTLS 的授權邏輯：server 端驗 client cert 是否由內部 CA 簽發，
// 再依 CN 比對 config.yaml 的 grpc.allowed_client_cns 允許清單。
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// services 需要內部憑證的服務（agent_registry 第一版不進 mTLS 圈，靠內網綁定）。
var services = []string{
	"agent_gateway",
	"agents",
	"agent_memory",
	"agent_critic",
	"agent_graph",
	"agent_embedding",
	"graphic_input", // dev_090 內網服務（外網版不部署，但憑證一起發）
}

type stringList []string

func (s *stringList) String() string     { return fmt.Sprint(*s) }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var (
		root     = flag.String("root", "..", "repo 根目錄（各服務目錄的上層）")
		extraDNS stringList
		extraIPs stringList
	)
	flag.Var(&extraDNS, "dns", "額外的 DNS SAN（可重複；部署機對外主機名）")
	flag.Var(&extraIPs, "ip", "額外的 IP SAN（可重複；部署機對外/內網 IP）")
	flag.Parse()

	dnsNames := append([]string{"localhost"}, extraDNS...)
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	for _, s := range extraIPs {
		ip := net.ParseIP(s)
		if ip == nil {
			fatal(fmt.Errorf("invalid -ip %q", s))
		}
		ips = append(ips, ip)
	}

	// ── 第一段：內部 CA ──
	caDir := filepath.Join(*root, "certs-internal")
	must(os.MkdirAll(caDir, 0o755))

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	caTmpl := x509.Certificate{
		SerialNumber: serial(),
		Subject: pkix.Name{
			Organization: []string{"single_agent internal"},
			CommonName:   "single_agent internal CA",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTmpl, &caTmpl, &caKey.PublicKey, caKey)
	must(err)
	caCert, err := x509.ParseCertificate(caDER)
	must(err)

	writePEM(filepath.Join(caDir, "internal-ca.crt"), "CERTIFICATE", caDER, 0o644)
	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	must(err)
	writePEM(filepath.Join(caDir, "internal-ca.key"), "EC PRIVATE KEY", caKeyDER, 0o600)
	fmt.Printf("✔ internal CA → %s\n", caDir)

	// ── 第二段：per-service server + client 憑證 ──
	for _, svc := range services {
		certDir := filepath.Join(*root, svc, "certs")
		must(os.MkdirAll(certDir, 0o755))

		// server cert：SAN = localhost/127.0.0.1/::1 + 部署機額外 SAN
		issue(certDir, "server", caCert, caKey, x509.Certificate{
			SerialNumber: serial(),
			Subject: pkix.Name{
				Organization: []string{"single_agent internal"},
				CommonName:   svc,
			},
			NotBefore:   time.Now().Add(-time.Hour),
			NotAfter:    time.Now().AddDate(10, 0, 0),
			KeyUsage:    x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			DNSNames:    dnsNames,
			IPAddresses: ips,
		})

		// client cert：CN=<svc>，內部 mTLS 呼叫端身份（允許清單比對用）
		issue(certDir, "client", caCert, caKey, x509.Certificate{
			SerialNumber: serial(),
			Subject: pkix.Name{
				Organization: []string{"single_agent internal"},
				CommonName:   svc,
			},
			NotBefore:   time.Now().Add(-time.Hour),
			NotAfter:    time.Now().AddDate(10, 0, 0),
			KeyUsage:    x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		})

		// 信任錨副本
		writePEM(filepath.Join(certDir, "internal-ca.crt"), "CERTIFICATE", caDER, 0o644)
		fmt.Printf("✔ %s → server.crt / client.crt (CN=%s) / internal-ca.crt\n", certDir, svc)
	}

	fmt.Printf("\nSANs: %v %v\n", dnsNames, extraIPs)
	fmt.Println("注意：internal-ca.key 只應存在於部署機；各服務 certs/ 目錄不需要它。")
}

func issue(dir, name string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, tmpl x509.Certificate) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, caCert, &priv.PublicKey, caKey)
	must(err)
	writePEM(filepath.Join(dir, name+".crt"), "CERTIFICATE", der, 0o644)
	keyDER, err := x509.MarshalECPrivateKey(priv)
	must(err)
	writePEM(filepath.Join(dir, name+".key"), "EC PRIVATE KEY", keyDER, 0o600)
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	must(err)
	defer f.Close()
	must(pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}))
}

func serial() *big.Int {
	sn, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	must(err)
	return sn
}

func must(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "gencert: %v\n", err)
	os.Exit(1)
}
