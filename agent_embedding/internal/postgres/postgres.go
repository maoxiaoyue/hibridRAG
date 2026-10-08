// Package postgres provides a PG connection wrapper backed by hypgo/pkg/hidb.
//
// 對外 API（Init / Get / Ping / Close）保持不變；底層由 hidb.Database
// 管理連線池與 lifecycle。需要 bun ORM 時可直接呼叫 instance.HypDB()。
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/maoxiaoyue/hypgo/pkg/config"
	"github.com/maoxiaoyue/hypgo/pkg/hidb"
	hidbpg "github.com/maoxiaoyue/hypgo/pkg/hidb/pg"
)

// Config PG 連線設定
type Config struct {
	DSN             string        `yaml:"dsn"`
	MaxOpenConns    int           `yaml:"max_open_conns"`
	MaxIdleConns    int           `yaml:"max_idle_conns"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
	ConnectTimeout  time.Duration `yaml:"connect_timeout"`
	Enabled         bool          `yaml:"enabled"`
}

type hidbAdapter struct{ cfg Config }

func (a hidbAdapter) GetDriver() string                           { return "postgres" }
func (a hidbAdapter) GetDSN() string                              { return a.cfg.DSN }
func (a hidbAdapter) GetMaxIdleConns() int                        { return a.cfg.MaxIdleConns }
func (a hidbAdapter) GetMaxOpenConns() int                        { return a.cfg.MaxOpenConns }
func (a hidbAdapter) GetRedisConfig() config.RedisConfigInterface { return nil }

var instance *hidb.Database

// Init 建立 PG 連線。Enabled=false 或 DSN 為空時回傳 nil（不算錯）。
func Init(cfg Config) (*sql.DB, error) {
	if !cfg.Enabled || cfg.DSN == "" {
		return nil, nil
	}
	if cfg.MaxOpenConns <= 0 {
		cfg.MaxOpenConns = 10
	}
	if cfg.MaxIdleConns <= 0 {
		cfg.MaxIdleConns = 2
	}
	db, err := hidb.NewWithInterface(hidbAdapter{cfg: cfg}, hidb.WithDialect(hidbpg.New()))
	if err != nil {
		return nil, fmt.Errorf("postgres init: %w", err)
	}
	instance = db
	return db.SQL(), nil
}

// Get 取得目前的 *sql.DB（未初始化時回 nil）
func Get() *sql.DB {
	if instance == nil {
		return nil
	}
	return instance.SQL()
}

// Database 暴露 hidb.Database，呼叫端可用 bun ORM / replica pool / 健檢等
func Database() *hidb.Database { return instance }

// Ping 連線檢查（給 health check 用）
func Ping(ctx context.Context) error {
	if instance == nil {
		return fmt.Errorf("postgres: not initialized")
	}
	return instance.HealthCheck(ctx)
}

// Close 關閉連線
func Close() error {
	if instance == nil {
		return nil
	}
	return instance.Close()
}
