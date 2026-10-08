// cmd/probe — one-shot connectivity check: PostgreSQL, Cassandra, Redis
// 全部走 hypgo/pkg/hidb 統一介面。
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/maoxiaoyue/hypgo/pkg/config"
	"github.com/maoxiaoyue/hypgo/pkg/hidb"
	hidbcassandra "github.com/maoxiaoyue/hypgo/pkg/hidb/cassandra"
	hidbpg "github.com/maoxiaoyue/hypgo/pkg/hidb/pg"
)

// ── 適配器：把硬編碼 DSN / Redis addr 轉成 hidb 接受的介面 ────────────────────

type pgCfg struct{ dsn string }

func (p pgCfg) GetDriver() string                              { return "postgres" }
func (p pgCfg) GetDSN() string                                 { return p.dsn }
func (p pgCfg) GetMaxIdleConns() int                           { return 1 }
func (p pgCfg) GetMaxOpenConns() int                           { return 1 }
func (p pgCfg) GetRedisConfig() config.RedisConfigInterface    { return nil }

type rdCfg struct {
	addr, password string
	db             int
}

func (r rdCfg) GetDriver() string                              { return "redis" }
func (r rdCfg) GetDSN() string                                 { return "" }
func (r rdCfg) GetMaxIdleConns() int                           { return 0 }
func (r rdCfg) GetMaxOpenConns() int                           { return 0 }
func (r rdCfg) GetRedisConfig() config.RedisConfigInterface    { return rdInner{r} }

type rdInner struct{ rdCfg }

func (r rdInner) GetAddr() string     { return r.addr }
func (r rdInner) GetPassword() string { return r.password }
func (r rdInner) GetDB() int          { return r.db }

func main() {
	ok := true

	// ── PostgreSQL ────────────────────────────────────────────────────────────
	fmt.Print("[1/3] PostgreSQL 192.168.50.201:5433 ... ")
	dsn := "postgres://mcpadm:Mephisto80@192.168.50.201:5433/mcp?sslmode=disable&connect_timeout=5"
	pgDB, err := hidb.NewWithInterface(pgCfg{dsn: dsn}, hidb.WithDialect(hidbpg.New()))
	if err != nil {
		fmt.Printf("OPEN_ERR: %v\n", err)
		ok = false
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		var version string
		if err := pgDB.SQL().QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
			fmt.Printf("QUERY_FAIL: %v\n", err)
			ok = false
		} else {
			fmt.Printf("OK  (%s)\n", truncate(version, 60))
		}
		cancel()
		_ = pgDB.Close()
	}

	// ── Cassandra ─────────────────────────────────────────────────────────────
	fmt.Print("[2/3] Cassandra 192.168.50.201:9042     ... ")
	cDB, err := hidbcassandra.New(hidbcassandra.Config{
		Hosts:          []string{"192.168.50.201"},
		Port:           9042,
		Keyspace:       "mcp",
		Username:       "mcpadm",
		Password:       "qwert77142",
		Consistency:    "local_one",
		Timeout:        6 * time.Second,
		ConnectTimeout: 6 * time.Second,
		NumConns:       1,
	})
	if err != nil {
		fmt.Printf("CONNECT_FAIL: %v\n", err)
		ok = false
	} else {
		var release string
		if err := cDB.Session().Query("SELECT release_version FROM system.local").Scan(&release); err != nil {
			fmt.Printf("QUERY_FAIL: %v\n", err)
			ok = false
		} else {
			fmt.Printf("OK  (Cassandra %s, keyspace=mcp)\n", release)
		}
		_ = cDB.Close()
	}

	// ── Redis ─────────────────────────────────────────────────────────────────
	fmt.Print("[3/3] Redis     192.168.50.201:6379     ... ")
	rDB, err := hidb.NewWithInterface(rdCfg{addr: "192.168.50.201:6379", password: "Mephisto80", db: 0})
	if err != nil {
		fmt.Printf("PING_FAIL: %v\n", err)
		ok = false
	} else {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 7*time.Second)
		pong, perr := rDB.Redis().Ping(ctx2).Result()
		if perr != nil {
			fmt.Printf("PING_FAIL: %v\n", perr)
			ok = false
		} else {
			info, _ := rDB.Redis().Info(ctx2, "server").Result()
			ver := extractInfoField(info, "redis_version")
			fmt.Printf("OK  (PONG=%s, version=%s)\n", pong, ver)
		}
		cancel2()
		_ = rDB.Close()
	}

	// ── TCP reachability bonus check ──────────────────────────────────────────
	fmt.Println()
	fmt.Println("── TCP reachability ──")
	targets := []struct{ label, addr string }{
		{"PG     ", "192.168.50.201:5433"},
		{"Cassandra", "192.168.50.201:9042"},
		{"Redis  ", "192.168.50.201:6379"},
	}
	for _, t := range targets {
		conn, err := net.DialTimeout("tcp", t.addr, 4*time.Second)
		if err != nil {
			fmt.Printf("  %-10s  UNREACHABLE  %v\n", t.label, err)
		} else {
			conn.Close()
			fmt.Printf("  %-10s  REACHABLE\n", t.label)
		}
	}

	fmt.Println()
	if ok {
		fmt.Println("==> All connections OK")
		os.Exit(0)
	} else {
		fmt.Println("==> One or more connections FAILED")
		os.Exit(1)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func extractInfoField(info, field string) string {
	for _, line := range splitLines(info) {
		if len(line) > len(field)+1 && line[:len(field)] == field && line[len(field)] == ':' {
			v := line[len(field)+1:]
			for len(v) > 0 && (v[len(v)-1] == '\r' || v[len(v)-1] == '\n') {
				v = v[:len(v)-1]
			}
			return v
		}
	}
	return "?"
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
