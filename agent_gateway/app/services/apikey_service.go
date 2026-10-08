// apikey_service.go — dev_pub_0.9 API Key 簽發 / 驗證 / 裝置綁定（TOFU）。
//
// 安全邊界：
//   - 明文 key 只在 Create 時產生並回傳一次，DB 只存 SHA-256 hash
//   - 「一 Key 一裝置」：首次使用以條件式 UPDATE 原子綁定 device_id，
//     防多裝置同時搶綁的競態；已綁定但不符 → ErrDeviceMismatch
//   - 日誌/錯誤訊息永不包含明文 key
package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"agent_gatway/app/models"
)

var (
	// ErrKeyInvalid key 不存在或已撤銷。
	ErrKeyInvalid = errors.New("api key invalid or revoked")
	// ErrDeviceMismatch key 已綁定其他裝置。
	ErrDeviceMismatch = errors.New("api key is bound to a different device")
)

// APIKeyService api_keys 表的業務邏輯。
type APIKeyService struct {
	db *bun.DB
}

func NewAPIKeyService(db *bun.DB) *APIKeyService { return &APIKeyService{db: db} }

func hashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// Create 簽發一把新 key：回傳（明文, 記錄）。明文只在這裡出現一次。
func (s *APIKeyService) Create(ctx context.Context, name string) (string, *models.APIKey, error) {
	if name == "" {
		return "", nil, fmt.Errorf("name is required")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate key: %w", err)
	}
	plaintext := "sak_" + hex.EncodeToString(raw) // single_agent key

	rec := &models.APIKey{
		ID:        "ak_" + newID(),
		KeyHash:   hashKey(plaintext),
		Name:      name,
		Status:    "active",
		CreatedAt: time.Now(),
	}
	if _, err := s.db.NewInsert().Model(rec).Exec(ctx); err != nil {
		return "", nil, fmt.Errorf("insert api key: %w", err)
	}
	return plaintext, rec, nil
}

// List 全部 key（不含 hash）。
func (s *APIKeyService) List(ctx context.Context) ([]models.APIKey, error) {
	var out []models.APIKey
	if err := s.db.NewSelect().Model(&out).Order("created_at ASC").Scan(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// Revoke 撤銷（不可逆；要恢復請重發新 key）。
func (s *APIKeyService) Revoke(ctx context.Context, id string) error {
	res, err := s.db.NewUpdate().Model((*models.APIKey)(nil)).
		Set("status = 'revoked'").Where("id = ?", id).Exec(ctx)
	return oneRowOr(res, err, id)
}

// Unbind 重置裝置綁定（換機用）：下一次使用重新 TOFU 綁定。
func (s *APIKeyService) Unbind(ctx context.Context, id string) error {
	res, err := s.db.NewUpdate().Model((*models.APIKey)(nil)).
		Set("device_id = ''").Set("device_info = ''").Where("id = ?", id).Exec(ctx)
	return oneRowOr(res, err, id)
}

func oneRowOr(res sql.Result, err error, id string) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("api key %s not found", id)
	}
	return nil
}

// Authenticate 驗證一次呼叫：key 有效 + 裝置綁定成立。
// 回傳 key 名稱（供 Ping 顯示）。deviceInfo 只在首次綁定時寫入。
func (s *APIKeyService) Authenticate(ctx context.Context, plaintext, deviceID, deviceInfo, ip string) (string, error) {
	if plaintext == "" || deviceID == "" {
		return "", ErrKeyInvalid
	}
	h := hashKey(plaintext)

	var rec models.APIKey
	err := s.db.NewSelect().Model(&rec).Where("key_hash = ?", h).Scan(ctx)
	if err != nil {
		return "", ErrKeyInvalid // 不區分「不存在」與 DB 錯誤細節，避免探測
	}
	if rec.Status != "active" {
		return "", ErrKeyInvalid
	}

	switch rec.DeviceID {
	case deviceID:
		// 已綁定本裝置 → 通過
	case "":
		// TOFU 首用即綁：條件式 UPDATE 防競態（兩台裝置同時搶綁只有一台會成功）
		res, uerr := s.db.NewUpdate().Model((*models.APIKey)(nil)).
			Set("device_id = ?", deviceID).
			Set("device_info = ?", deviceInfo).
			Set("first_used_at = ?", time.Now()).
			Where("key_hash = ? AND (device_id IS NULL OR device_id = '')", h).
			Exec(ctx)
		if uerr != nil {
			return "", fmt.Errorf("bind device: %w", uerr)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// 綁定競態輸了 → 重查確認是不是同裝置
			if s.db.NewSelect().Model(&rec).Where("key_hash = ?", h).Scan(ctx) != nil || rec.DeviceID != deviceID {
				return "", ErrDeviceMismatch
			}
		}
	default:
		return "", ErrDeviceMismatch
	}

	// last_used_at / last_seen_ip：best-effort，不阻塞請求
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = s.db.NewUpdate().Model((*models.APIKey)(nil)).
			Set("last_used_at = ?", time.Now()).
			Set("last_seen_ip = ?", ip).
			Where("key_hash = ?", h).Exec(bg)
	}()

	return rec.Name, nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
