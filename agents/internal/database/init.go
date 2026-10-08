// Package database 包裝 hypgo/pkg/hidb 提供 PostgreSQL / MySQL 連線管理。
//
// 對外 API 保持不變（Init / GetDB / GetSQLDB / Close / Transaction），
// 底層由 hidb.Database 接管（含 bun ORM、replica pool、graceful close 等）。
package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/maoxiaoyue/hypgo/pkg/config"
	"github.com/maoxiaoyue/hypgo/pkg/hidb"
	hidbmysql "github.com/maoxiaoyue/hypgo/pkg/hidb/mysql"
	hidbpg "github.com/maoxiaoyue/hypgo/pkg/hidb/pg"
	"github.com/uptrace/bun"
)

// Config 與舊版欄位保持一致，供 YAML 反序列化使用。
// 注意：ConnMaxLifetime / LogLevel / AutoMigrate 由 hidb 內部管理或忽略。
type Config struct {
	Driver          string `yaml:"driver" json:"driver"`
	DSN             string `yaml:"dsn" json:"dsn"`
	MaxIdleConns    int    `yaml:"max_idle_conns" json:"max_idle_conns"`
	MaxOpenConns    int    `yaml:"max_open_conns" json:"max_open_conns"`
	ConnMaxLifetime string `yaml:"conn_max_lifetime" json:"conn_max_lifetime"`
	LogLevel        string `yaml:"log_level" json:"log_level"`
	AutoMigrate     bool   `yaml:"auto_migrate" json:"auto_migrate"`
}

// hidbAdapter 把 Config 包成 config.DatabaseConfigInterface
type hidbAdapter struct{ cfg Config }

func (a hidbAdapter) GetDriver() string                          { return a.cfg.Driver }
func (a hidbAdapter) GetDSN() string                             { return a.cfg.DSN }
func (a hidbAdapter) GetMaxIdleConns() int                       { return a.cfg.MaxIdleConns }
func (a hidbAdapter) GetMaxOpenConns() int                       { return a.cfg.MaxOpenConns }
func (a hidbAdapter) GetRedisConfig() config.RedisConfigInterface { return nil }

var instance *hidb.Database

// Init 初始化 SQL 資料庫連線（PostgreSQL / MySQL），由 hidb 接管底層。
func Init(cfg Config) (*bun.DB, error) {
	if cfg.Driver == "" {
		cfg.Driver = "postgres"
	}
	if cfg.MaxIdleConns == 0 {
		cfg.MaxIdleConns = 10
	}
	if cfg.MaxOpenConns == 0 {
		cfg.MaxOpenConns = 100
	}

	var opt hidb.Option
	switch cfg.Driver {
	case "postgres", "postgresql":
		opt = hidb.WithDialect(hidbpg.New())
	case "mysql":
		opt = hidb.WithDialect(hidbmysql.New())
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	db, err := hidb.NewWithInterface(hidbAdapter{cfg: cfg}, opt)
	if err != nil {
		return nil, fmt.Errorf("hidb init: %w", err)
	}
	instance = db
	return db.HypDB(), nil
}

// GetDB 取得主庫 Bun ORM 實例
func GetDB() *bun.DB {
	if instance == nil {
		return nil
	}
	return instance.HypDB()
}

// GetSQLDB 取得原始 SQL 連線
func GetSQLDB() *sql.DB {
	if instance == nil {
		return nil
	}
	return instance.SQL()
}

// Database 對外暴露 hidb.Database（呼叫端可進一步使用 ReadHypDB / Redis / HealthCheck 等）
func Database() *hidb.Database { return instance }

// Close 關閉所有連線
func Close() error {
	if instance == nil {
		return nil
	}
	return instance.Close()
}

// Transaction 事務輔助；以 HypDB ORM 執行
func Transaction(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	if instance == nil {
		return fmt.Errorf("database: not initialized")
	}
	return instance.HypDBTransaction(ctx, fn)
}
