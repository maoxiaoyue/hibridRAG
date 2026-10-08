// vault_files.go — vault .md 檔的唯讀存取層，供 gRPC VaultManifest/VaultFetch
// 使用（pub_client「Sync Vault」下載）。與 VaultExporter 相反方向：exporter 寫
// vault、這裡只讀。路徑安全比照 pub_client 的 resolveInWorkspace：逃逸明確回錯。
package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agent_graph/app/models"
)

// VaultFS 掛在一個 vault 根目錄上的唯讀檔案層。root 空 = 停用。
type VaultFS struct {
	root string
}

func NewVaultFS(root string) *VaultFS { return &VaultFS{root: root} }

func (v *VaultFS) Enabled() bool { return v != nil && v.root != "" }

// Manifest 列出 vault 內全部 .md（跳過 dot 目錄），含 sha256——client 端用
// hash 差異決定要下載哪些檔。
func (v *VaultFS) Manifest(ctx context.Context) ([]models.VaultFileInfo, error) {
	if !v.Enabled() {
		return nil, fmt.Errorf("vault not configured")
	}
	rels, err := walkMD(v.root)
	if err != nil {
		return nil, fmt.Errorf("walk vault: %w", err)
	}
	out := make([]models.VaultFileInfo, 0, len(rels))
	for _, rel := range rels {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		full := filepath.Join(v.root, filepath.FromSlash(rel))
		info, err := os.Stat(full)
		if err != nil {
			continue // 清單期間被刪：跳過
		}
		sum, err := fileSHA256(full)
		if err != nil {
			continue
		}
		out = append(out, models.VaultFileInfo{
			Path:         rel,
			Size:         info.Size(),
			SHA256:       sum,
			ModifiedUnix: info.ModTime().Unix(),
		})
	}
	return out, nil
}

// Open 開啟 vault 內一個 .md。相對路徑限定：絕對路徑、.. 逃逸、非 .md 一律
// 明確回錯（不靜默鉗制）。
func (v *VaultFS) Open(rel string) (*os.File, error) {
	if !v.Enabled() {
		return nil, fmt.Errorf("vault not configured")
	}
	if rel == "" {
		return nil, fmt.Errorf("empty path")
	}
	native := filepath.FromSlash(rel)
	if filepath.IsAbs(native) {
		return nil, fmt.Errorf("absolute path not allowed: %s", rel)
	}
	if !strings.EqualFold(filepath.Ext(native), ".md") {
		return nil, fmt.Errorf("only .md files are served: %s", rel)
	}
	full := filepath.Join(v.root, native)
	r, err := filepath.Rel(v.root, full)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("path escapes vault: %s", rel)
	}
	return os.Open(full)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
