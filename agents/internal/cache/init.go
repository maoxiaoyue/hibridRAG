// Package cache 包裝 hypgo/pkg/hidb 提供 Redis 連線（dirver="redis"）。
//
// 對外 API 保持不變（Init / GetClient / Close / Set / Get / Delete /
// Exists / SetNX / Incr / Expire），底層改用 hidb.Database。
//
// 注意：hidb 的 Redis 初始化僅支援 Addr / Password / DB 三個欄位；
// 原 Config 的進階 pool tuning（PoolSize / MinIdleConns / MaxRetries /
// timeouts）目前未被任何 YAML 設定使用，故保留欄位但實際被忽略，
// 之後若需更細的控制可改走 hidb plugin 介面。
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/maoxiaoyue/hypgo/pkg/config"
	"github.com/maoxiaoyue/hypgo/pkg/hidb"
	"github.com/redis/go-redis/v9"
)

type Config struct {
	Addr         string `yaml:"addr" json:"addr"`
	Password     string `yaml:"password" json:"password"`
	DB           int    `yaml:"db" json:"db"`
	PoolSize     int    `yaml:"pool_size" json:"pool_size"`
	MinIdleConns int    `yaml:"min_idle_conns" json:"min_idle_conns"`
	MaxRetries   int    `yaml:"max_retries" json:"max_retries"`
	DialTimeout  string `yaml:"dial_timeout" json:"dial_timeout"`
	ReadTimeout  string `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout string `yaml:"write_timeout" json:"write_timeout"`
}

// hidbRedisAdapter 把 Config 包成 hidb 接受的 DatabaseConfigInterface (driver=redis)
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

var (
	instance *hidb.Database
	ctx      = context.Background()
)

// Init 初始化 Redis 連線
func Init(cfg Config) error {
	if cfg.Addr == "" {
		cfg.Addr = "localhost:6379"
	}
	db, err := hidb.NewWithInterface(hidbRedisAdapter{cfg: cfg})
	if err != nil {
		return fmt.Errorf("redis init: %w", err)
	}
	instance = db
	return nil
}

// GetClient 取得底層 *redis.Client，相容舊呼叫端
func GetClient() *redis.Client {
	if instance == nil {
		return nil
	}
	return instance.Redis()
}

// Database 暴露 hidb.Database，呼叫端可使用其包裝過的 Redis* 方法（含 SetJSON / HSet 等）
func Database() *hidb.Database { return instance }

// Close 關閉連線
func Close() error {
	if instance == nil {
		return nil
	}
	return instance.Close()
}

// ── 以下為相容層：保留原 helper API，內部改呼叫 hidb 包裝方法 ──────────────

func client() *redis.Client {
	if instance == nil {
		return nil
	}
	return instance.Redis()
}

// Set 設置鍵值
func Set(key string, value interface{}, expiration time.Duration) error {
	c := client()
	if c == nil {
		return fmt.Errorf("redis not initialized")
	}
	return c.Set(ctx, key, value, expiration).Err()
}

// Get 取值
func Get(key string) (string, error) {
	if instance == nil {
		return "", fmt.Errorf("redis not initialized")
	}
	return instance.RedisGet(ctx, key)
}

// Delete 刪除一或多個 key
func Delete(keys ...string) error {
	if instance == nil {
		return fmt.Errorf("redis not initialized")
	}
	_, err := instance.RedisDel(ctx, keys...)
	return err
}

// Exists 檢查 key 是否存在
func Exists(keys ...string) (int64, error) {
	if instance == nil {
		return 0, fmt.Errorf("redis not initialized")
	}
	return instance.RedisExists(ctx, keys...)
}

// SetNX 僅 key 不存在時設定
func SetNX(key string, value interface{}, expiration time.Duration) (bool, error) {
	c := client()
	if c == nil {
		return false, fmt.Errorf("redis not initialized")
	}
	return c.SetNX(ctx, key, value, expiration).Result()
}

// Incr 原子遞增
func Incr(key string) (int64, error) {
	if instance == nil {
		return 0, fmt.Errorf("redis not initialized")
	}
	return instance.RedisIncr(ctx, key)
}

// Expire 設定過期時間
func Expire(key string, expiration time.Duration) error {
	if instance == nil {
		return fmt.Errorf("redis not initialized")
	}
	_, err := instance.RedisExpire(ctx, key, expiration)
	return err
}
