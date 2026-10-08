// api_key.go — dev_pub_0.9 API Key 認證的持久化模型（「一 Key 一裝置」）。
package models

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// APIKey 一把發給 client 的 API Key。明文不落地——只存 SHA-256 hash。
// DeviceID 空字串 = 尚未綁定；首次使用時 TOFU 原子綁定（見 apikey_service.go）。
type APIKey struct {
	bun.BaseModel `bun:"table:api_keys,alias:ak"`

	ID          string    `bun:"id,pk" json:"id"`
	KeyHash     string    `bun:"key_hash,notnull,unique" json:"-"`
	Name        string    `bun:"name,notnull" json:"name"`
	DeviceID    string    `bun:"device_id" json:"device_id,omitempty"`
	DeviceInfo  string    `bun:"device_info" json:"device_info,omitempty"`
	Status      string    `bun:"status,notnull,default:'active'" json:"status"` // active | revoked
	CreatedAt   time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	FirstUsedAt time.Time `bun:"first_used_at,nullzero" json:"first_used_at,omitempty"`
	LastUsedAt  time.Time `bun:"last_used_at,nullzero" json:"last_used_at,omitempty"`
	LastSeenIP  string    `bun:"last_seen_ip" json:"last_seen_ip,omitempty"`
}

// AutoMigrateAPIKeys 建 api_keys 表（bun CreateTable，IfNotExists）。
func AutoMigrateAPIKeys(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().Model((*APIKey)(nil)).IfNotExists().Exec(ctx)
	return err
}
