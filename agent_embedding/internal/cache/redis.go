// Package cache provides a Redis client backed by hypgo/pkg/hidb.
//
// 對外 API（Init / Get / Close）保持不變；底層由 hidb.Database 管理。
package cache

import (
	"fmt"

	"github.com/maoxiaoyue/hypgo/pkg/config"
	"github.com/maoxiaoyue/hypgo/pkg/hidb"
	"github.com/redis/go-redis/v9"
)

// Config Redis connection options.
type Config struct {
	Addr     string
	Password string
	DB       int
	Enabled  bool
}

type hidbRedisAdapter struct{ cfg Config }

func (a hidbRedisAdapter) GetDriver() string    { return "redis" }
func (a hidbRedisAdapter) GetDSN() string       { return "" }
func (a hidbRedisAdapter) GetMaxIdleConns() int { return 0 }
func (a hidbRedisAdapter) GetMaxOpenConns() int { return 0 }
func (a hidbRedisAdapter) GetRedisConfig() config.RedisConfigInterface {
	return redisInner{cfg: a.cfg}
}

type redisInner struct{ cfg Config }

func (r redisInner) GetAddr() string     { return r.cfg.Addr }
func (r redisInner) GetPassword() string { return r.cfg.Password }
func (r redisInner) GetDB() int          { return r.cfg.DB }

var instance *hidb.Database

// Init creates the Redis client via hidb. Returns nil client (no error) when Enabled=false.
func Init(cfg Config) (*redis.Client, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	db, err := hidb.NewWithInterface(hidbRedisAdapter{cfg: cfg})
	if err != nil {
		return nil, fmt.Errorf("redis init: %w", err)
	}
	instance = db
	return db.Redis(), nil
}

// Get returns the active client (nil if disabled).
func Get() *redis.Client {
	if instance == nil {
		return nil
	}
	return instance.Redis()
}

// Database exposes the underlying hidb.Database for advanced operations.
func Database() *hidb.Database { return instance }

// Close terminates the client.
func Close() error {
	if instance == nil {
		return nil
	}
	return instance.Close()
}
