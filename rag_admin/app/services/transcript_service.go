// transcript_service.go — 對話轉錄落地：每輪結束後把整份 Markdown 轉錄
// 覆寫到 %APPDATA%\agent_client\sessions\{session_id}.md，關視窗不再遺失對話。
package services

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SessionMeta 一筆轉錄的清單資訊（供 History 瀏覽器顯示標題）。
type SessionMeta struct {
	SessionID string
	Title     string    // 由轉錄內容推導（第一則使用者訊息）
	UpdatedAt time.Time // 檔案 mtime
}

// ListSessions 回傳最近的轉錄（新→舊，最多 limit 筆），含由內容推導的標題。
func ListSessions(limit int) ([]SessionMeta, error) {
	dir := SessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type f struct {
		id  string
		mod time.Time
	}
	var files []f
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		files = append(files, f{id: strings.TrimSuffix(e.Name(), ".md"), mod: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}
	out := make([]SessionMeta, 0, len(files))
	for _, fi := range files {
		content, _ := os.ReadFile(filepath.Join(dir, fi.id+".md"))
		out = append(out, SessionMeta{SessionID: fi.id, Title: TranscriptTitle(string(content)), UpdatedAt: fi.mod})
	}
	return out, nil
}

// ReadTranscript 讀回整份轉錄（Markdown）。
func ReadTranscript(sessionID string) (string, error) {
	id := sessionIDSanitizer.ReplaceAllString(sessionID, "_")
	b, err := os.ReadFile(filepath.Join(SessionsDir(), id+".md"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// TranscriptTitle 從轉錄推導標題：取第一則使用者訊息（"**You:** …"）首段，截斷。
func TranscriptTitle(md string) string {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "**You:**"); ok {
			rest = strings.TrimSpace(rest)
			if rest != "" && !strings.HasPrefix(rest, "*(") { // 略過只有附圖註記的情況
				return truncateTitle(rest)
			}
		}
	}
	for _, line := range strings.Split(md, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return truncateTitle(line)
		}
	}
	return "(empty)"
}

func truncateTitle(s string) string {
	r := []rune(s)
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// SessionsDir 回傳對話轉錄的存放目錄（%APPDATA%\rag_admin\sessions；無 APPDATA 時退到工作目錄）。
func SessionsDir() string { return filepath.Join(appDataRoot(), "rag_admin", "sessions") }

func legacySessionsDir() string { return filepath.Join(appDataRoot(), "agent_client", "sessions") }

func appDataRoot() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "."
	}
	return dir
}

// MigrateLegacySessions 一次性：若新的 rag_admin\sessions 不存在、但舊的 agent_client\sessions
// 有轉錄，就把 *.md 複製過來（best-effort，啟動時呼叫一次）。
func MigrateLegacySessions() {
	newDir := SessionsDir()
	if _, err := os.Stat(newDir); err == nil {
		return // 新目錄已存在
	}
	entries, err := os.ReadDir(legacySessionsDir())
	if err != nil {
		return // 舊目錄不存在
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(legacySessionsDir(), e.Name()))
		if rerr != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(newDir, e.Name()), data, 0o644)
	}
}

// sessionIDSanitizer 把 session id 中不適合當檔名的字元換成底線。
// server 端 id 格式是 "sess_<hex>"，正常情況不會被改寫；這層只是路徑安全防線。
var sessionIDSanitizer = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// SaveTranscript 把整份對話轉錄（Markdown）寫到 sessions/{session_id}.md（整檔覆寫）。
func SaveTranscript(sessionID, content string) error {
	return saveTranscriptIn(SessionsDir(), sessionID, content)
}

func saveTranscriptIn(dir, sessionID, content string) error {
	id := sessionIDSanitizer.ReplaceAllString(sessionID, "_")
	if id == "" || id == "_" {
		return fmt.Errorf("invalid session id %q", sessionID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir sessions dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	return nil
}

// OpenSessionsFolder 用檔案總管開啟轉錄目錄（本 client 只跑 Windows）。
func OpenSessionsFolder() error {
	dir := SessionsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir sessions dir: %w", err)
	}
	return exec.Command("explorer", dir).Start()
}
