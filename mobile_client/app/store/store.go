// Package store — mobile_client 的加密儲存。
//
// 桌面版用 DuckDB 內建加密（ATTACH ... ENCRYPTION_KEY），但 go-duckdb 沒有
// Android binding；手機版改成兩個 AES-256-GCM 加密的 JSON 檔（config.enc /
// sessions.enc），存在 app 私有目錄（Android 沙箱本身即隔離其他 app）。
//
// 金鑰：私有目錄下的 store.key（首次啟動隨機 32 bytes）。威脅模型與桌面版
// fallback 路徑相同——「檔案 + 金鑰同放私有目錄」，靠 OS 沙箱擋其他 app；
// 檔案被單獨複製走（無 key）時無法解開。
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mobile_client/app/models"
)

var (
	mu      sync.Mutex
	baseDir string // Init 注入（Fyne app 私有儲存目錄）
)

// maxSessions sessions.enc 保留的 session 上限；超過時修剪最舊的（手機儲存有限）。
const maxSessions = 100

// Init 設定儲存目錄（views.Run 啟動時以 Fyne app.Storage().RootURI().Path() 呼叫）。
func Init(dir string) {
	mu.Lock()
	defer mu.Unlock()
	baseDir = dir
}

// BaseDir 目前儲存目錄（未 Init 時退回目前工作目錄下的 .mobile_client）。
func BaseDir() string {
	if baseDir != "" {
		return baseDir
	}
	return ".mobile_client"
}

// StorePath 設定檔路徑（設定頁顯示用）。
func StorePath() string { return filepath.Join(BaseDir(), "config.enc") }

// ── 加密原語 ─────────────────────────────────────────────────────────────────

// loadKey 讀出（或首次產生）32-byte 金鑰。
func loadKey() ([]byte, error) {
	dir := BaseDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir store dir: %w", err)
	}
	keyPath := filepath.Join(dir, "store.key")
	if raw, err := os.ReadFile(keyPath); err == nil {
		if k, derr := hex.DecodeString(strings.TrimSpace(string(raw))); derr == nil && len(k) == 32 {
			return k, nil
		}
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("generate store key: %w", err)
	}
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(k)), 0o600); err != nil {
		return nil, fmt.Errorf("persist store key: %w", err)
	}
	return k, nil
}

// seal AES-256-GCM 加密：輸出 nonce || ciphertext。
func seal(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// unseal AES-256-GCM 解密（輸入 nonce || ciphertext）。
func unseal(key, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("sealed data too short")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
}

// loadEncrypted 讀檔並解密到 v；檔案不存在回 os.ErrNotExist。
func loadEncrypted(name string, v any) error {
	raw, err := os.ReadFile(filepath.Join(BaseDir(), name))
	if err != nil {
		return err
	}
	key, err := loadKey()
	if err != nil {
		return err
	}
	plain, err := unseal(key, raw)
	if err != nil {
		return fmt.Errorf("decrypt %s: %w", name, err)
	}
	return json.Unmarshal(plain, v)
}

// saveEncrypted 序列化 v、加密、原子寫入（tmp + rename）。
func saveEncrypted(name string, v any) error {
	key, err := loadKey()
	if err != nil {
		return err
	}
	plain, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	sealed, err := seal(key, plain)
	if err != nil {
		return fmt.Errorf("encrypt %s: %w", name, err)
	}
	path := filepath.Join(BaseDir(), name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, sealed, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit %s: %w", name, err)
	}
	return nil
}

// ── 設定 ────────────────────────────────────────────────────────────────────

// LoadConfig 讀取設定；store 內沒有時回預設值並落地。
func LoadConfig() (*models.Config, error) {
	mu.Lock()
	defer mu.Unlock()
	cfg := models.DefaultConfig()
	err := loadEncrypted("config.enc", cfg)
	if err == nil {
		return cfg, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if serr := saveEncrypted("config.enc", cfg); serr != nil {
		return nil, serr
	}
	return cfg, nil
}

// SaveConfig 持久化設定。
func SaveConfig(cfg *models.Config) error {
	mu.Lock()
	defer mu.Unlock()
	return saveEncrypted("config.enc", cfg)
}

// ── 對話 session ─────────────────────────────────────────────────────────────

type sessionRecord struct {
	AgentID    string    `json:"agent_id"`
	Transcript string    `json:"transcript"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// SessionMeta 一筆 session 的清單資訊。
type SessionMeta struct {
	SessionID string
	AgentID   string
	UpdatedAt time.Time
	Chars     int
}

// loadSessions 讀出全部 session（檔案不存在時回空 map）。呼叫端須已握 mu。
func loadSessions() (map[string]sessionRecord, error) {
	sessions := map[string]sessionRecord{}
	err := loadEncrypted("sessions.enc", &sessions)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return sessions, nil
}

// SaveSession 每輪結束後整份轉錄 upsert；超過 maxSessions 時修剪最舊的。
func SaveSession(sessionID, agentID, transcript string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("empty session id")
	}
	mu.Lock()
	defer mu.Unlock()
	sessions, err := loadSessions()
	if err != nil {
		return err
	}
	sessions[sessionID] = sessionRecord{AgentID: agentID, Transcript: transcript, UpdatedAt: time.Now()}
	if len(sessions) > maxSessions {
		type kv struct {
			id string
			at time.Time
		}
		all := make([]kv, 0, len(sessions))
		for id, rec := range sessions {
			all = append(all, kv{id, rec.UpdatedAt})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
		for _, old := range all[:len(all)-maxSessions] {
			delete(sessions, old.id)
		}
	}
	return saveEncrypted("sessions.enc", sessions)
}

// ListSessions 最近的 session（新的在前）。
func ListSessions(limit int) ([]SessionMeta, error) {
	if limit <= 0 || limit > maxSessions {
		limit = maxSessions
	}
	mu.Lock()
	defer mu.Unlock()
	sessions, err := loadSessions()
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(sessions))
	for id, rec := range sessions {
		out = append(out, SessionMeta{
			SessionID: id, AgentID: rec.AgentID,
			UpdatedAt: rec.UpdatedAt, Chars: len(rec.Transcript),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetSession 取回整份轉錄（Markdown 原文）。
func GetSession(sessionID string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	sessions, err := loadSessions()
	if err != nil {
		return "", err
	}
	rec, ok := sessions[sessionID]
	if !ok {
		return "", fmt.Errorf("session %s not found", sessionID)
	}
	return rec.Transcript, nil
}
