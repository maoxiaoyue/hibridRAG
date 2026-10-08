package db

import (
	"fmt"

	"github.com/maoxiaoyue/hypgo/pkg/hidb"
	"github.com/maoxiaoyue/hypgo/pkg/hidb/pg"
	"github.com/uptrace/bun"
)

var instance *hidb.Database

// Init 以 hidb 初始化 PostgreSQL 連線，儲存為 singleton
func Init(cfg any) (*hidb.Database, error) {
	db, err := hidb.New(cfg, hidb.WithDialect(pg.New()))
	if err != nil {
		return nil, fmt.Errorf("hidb init: %w", err)
	}
	instance = db
	return db, nil
}

// Get 回傳已初始化的 Database 實例；呼叫前必須先執行 Init
func Get() *hidb.Database {
	return instance
}

// DB 直接回傳主庫 Bun ORM 實例，供 service 層使用
func DB() *bun.DB {
	return instance.HypDB()
}

// Close 關閉所有連線
func Close() error {
	if instance != nil {
		return instance.Close()
	}
	return nil
}
