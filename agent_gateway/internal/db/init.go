// Package db — gateway 的 PG 連線（dev_pub_0.9：api_keys 表）。同 agent_registry 慣例走 hidb。
package db

import (
	"fmt"

	"github.com/maoxiaoyue/hypgo/pkg/hidb"
	"github.com/maoxiaoyue/hypgo/pkg/hidb/pg"
	"github.com/uptrace/bun"
)

var instance *hidb.Database

func Init(cfg any) (*hidb.Database, error) {
	database, err := hidb.New(cfg, hidb.WithDialect(pg.New()))
	if err != nil {
		return nil, fmt.Errorf("hidb init: %w", err)
	}
	instance = database
	return database, nil
}

func Get() *hidb.Database { return instance }

func DB() *bun.DB {
	if instance == nil {
		return nil
	}
	return instance.HypDB()
}

func Close() error {
	if instance != nil {
		return instance.Close()
	}
	return nil
}
