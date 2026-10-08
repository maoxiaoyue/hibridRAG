package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// MDConfig Obsidian vault 同步設定。
type MDConfig struct {
	Vault VaultConfig `yaml:"vault"`
}

type VaultConfig struct {
	// Path vault 根目錄絕對路徑；空字串 → 同步 worker 停用（服務仍可跑查詢 API）
	Path string `yaml:"path"`
	// Watch true：持續用 fsnotify 監聽；false：啟動時只做一次性掃描
	Watch bool `yaml:"watch"`
	// IncludeExt 只處理這些副檔名；空 → 預設 [".md"]
	IncludeExt []string `yaml:"include_ext"`
	// DebounceMS 同一檔案短時間內多次寫入事件的合併等待時間；<=0 → 500ms
	DebounceMS int `yaml:"debounce_ms"`
	// ExportNotes 圖譜寫入時同步匯出實體 .md 鏡像到 vault；未設 → 啟用
	//（Path 有設定時）。明確 false 才關閉。
	ExportNotes *bool `yaml:"export_notes"`
	// ExportDir 鏡像匯出子目錄（相對 Path）；空 → "entities"。此目錄被
	// vault 同步掃描/監聽排除（避免鏡像檔被再攝取的迴圈）。
	ExportDir string `yaml:"export_dir"`
	// TempDir 未分類暫存子目錄（相對 Path）；空 → "temp"。對話/上傳先落這裡，
	// 由分類 worker 挑走搬進 vault 分類目錄。此目錄同樣被 sync 掃描/監聽排除
	//（temp 內容還沒分類，不進圖譜；搬出 temp 後才會被攝取）。
	TempDir string `yaml:"temp_dir"`
	// Digest 知識沉澱管線設定（分類 + 每日整理）；nil → 用預設值。
	Digest DigestConfig `yaml:"digest"`
}

// DigestConfig 控制「temp → 分類 → vault、每日整理切割」的背景管線。
type DigestConfig struct {
	// Enabled 是否啟用管線；未設 → 啟用（Path 有設定時）。
	Enabled *bool `yaml:"enabled"`
	// ClassifyIntervalSec 分類 worker 掃 temp 的間隔；<=0 → 60。
	ClassifyIntervalSec int `yaml:"classify_interval_sec"`
	// MaintainIntervalSec 每日整理間隔；<=0 → 86400（一天）。
	MaintainIntervalSec int `yaml:"maintain_interval_sec"`
	// MaxCategoryFiles 一個分類目錄的檔案數上限，超過就切割；<=0 → 50。
	MaxCategoryFiles int `yaml:"max_category_files"`
}

func (v VaultConfig) Debounce() time.Duration {
	if v.DebounceMS <= 0 {
		return 500 * time.Millisecond
	}
	return time.Duration(v.DebounceMS) * time.Millisecond
}

func (v VaultConfig) Extensions() []string {
	if len(v.IncludeExt) == 0 {
		return []string{".md"}
	}
	return v.IncludeExt
}

// ExportDirName 鏡像匯出子目錄名；空 → "entities"。
func (v VaultConfig) ExportDirName() string {
	if v.ExportDir == "" {
		return "entities"
	}
	return v.ExportDir
}

// TempDirName 未分類暫存子目錄名；空 → "temp"。
func (v VaultConfig) TempDirName() string {
	if v.TempDir == "" {
		return "temp"
	}
	return v.TempDir
}

// ExcludedDirNames sync 掃描/監聽要排除的子目錄名（相對 Path）：鏡像匯出目錄
// （避免迴圈）+ 暫存目錄（未分類、還不進圖譜）。
func (v VaultConfig) ExcludedDirNames() []string {
	return []string{v.ExportDirName(), v.TempDirName()}
}

// ExportEnabled vault path 有設且未明確關閉時啟用鏡像匯出。
func (v VaultConfig) ExportEnabled() bool {
	return v.Path != "" && (v.ExportNotes == nil || *v.ExportNotes)
}

// DigestEnabled vault path 有設且未明確關閉時啟用知識沉澱管線。
func (v VaultConfig) DigestEnabled() bool {
	return v.Path != "" && (v.Digest.Enabled == nil || *v.Digest.Enabled)
}

// LoadMDConfig 讀取 app/config/md.yaml
func LoadMDConfig(path string) (*MDConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read md config %s: %w", path, err)
	}
	cfg := &MDConfig{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse md config: %w", err)
	}
	return cfg, nil
}
