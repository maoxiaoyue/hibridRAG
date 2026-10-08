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

func Get() *hidb.Database {
	return instance
}

func DB() *bun.DB {
	return instance.HypDB()
}

func Close() error {
	if instance != nil {
		return instance.Close()
	}
	return nil
}
