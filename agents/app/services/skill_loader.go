package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"agent/app/models"
)

// SkillLoader 負責載入 skills/{agent_id}/SKILL.md 與 references/*.md，
// 以 mtime + sha256 作為 cache 失效依據，供 Inbound 推理前零成本取用。
//
// 快取策略：
//  1. SKILL.md：先比 mtime（fast path）；若變動再重算 checksum 決定是否真的重讀
//  2. references/：逐檔比 mtime；任一變動則重讀該檔並更新聚合 hash
//  3. force=true 時跳過 mtime/checksum 檢查，整包重讀
type SkillLoader struct {
	rootDir string // skills/ 的根目錄

	mu    sync.RWMutex
	cache map[string]*models.SkillPack // key = agentID
}

// NewSkillLoader rootDir 指向 skills/ 根目錄（例如 "./skills"）
func NewSkillLoader(rootDir string) *SkillLoader {
	return &SkillLoader{
		rootDir: rootDir,
		cache:   make(map[string]*models.SkillPack),
	}
}

// Get 從 cache 取得目前載入的 pack，不觸發重讀；未載入回傳 ErrNotFound
func (l *SkillLoader) Get(agentID string) (*models.SkillPack, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p, ok := l.cache[agentID]
	if !ok {
		return nil, ErrNotFound
	}
	return p, nil
}

// Load 載入或重載指定 agent 的 SkillPack
//
// 回傳值：
//   - pack: 最新的 SkillPack（失敗時 nil）
//   - cached: true 表示完全命中 cache，無任何檔案重讀
//   - reloaded: 因 mtime / checksum 變動而重讀的檔案相對路徑列表
func (l *SkillLoader) Load(agentID string, force bool) (pack *models.SkillPack, cached bool, reloaded []string, err error) {
	if agentID == "" {
		return nil, false, nil, fmt.Errorf("agent_id is required")
	}

	dir := filepath.Join(l.rootDir, agentID)
	skillPath := filepath.Join(dir, "SKILL.md")

	skillStat, err := os.Stat(skillPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil, ErrNotFound
		}
		return nil, false, nil, err
	}

	l.mu.RLock()
	prev := l.cache[agentID]
	l.mu.RUnlock()

	// 掃 references/ 目錄並取得 (path → mtime) 對照
	refsDir := filepath.Join(dir, "references")
	refFiles, err := scanReferenceFiles(refsDir)
	if err != nil {
		return nil, false, nil, err
	}

	// Fast path：force=false 且有前值 → 用 mtime 判斷
	if !force && prev != nil && skillStat.ModTime().Equal(prev.SkillMTime) && sameRefMTimes(prev, refFiles) {
		return prev, true, nil, nil
	}

	// 重讀 SKILL.md（但若 checksum 未變就複用 Body）
	skillBody, skillSum, err := readAndHash(skillPath)
	if err != nil {
		return nil, false, nil, err
	}

	pack = &models.SkillPack{
		AgentID:       agentID,
		Body:          skillBody,
		References:    make(map[string]*models.Reference, len(refFiles)),
		LoadedAt:      time.Now(),
		SourceDir:     dir,
		SkillMTime:    skillStat.ModTime(),
		SkillChecksum: skillSum,
	}

	if prev == nil || prev.SkillChecksum != skillSum {
		reloaded = append(reloaded, "SKILL.md")
	}

	// 處理 references：能重用就重用，僅變動才重讀
	for _, rf := range refFiles {
		relPath := rf.relPath
		var prevRef *models.Reference
		if prev != nil {
			prevRef = prev.References[relPath]
		}

		if !force && prevRef != nil && rf.mtime.Equal(prevRef.MTime) {
			// mtime 未變，直接複用
			pack.References[relPath] = prevRef
			continue
		}

		body, sum, err := readAndHash(rf.absPath)
		if err != nil {
			return nil, false, nil, err
		}

		// 即便 mtime 變了，checksum 相同仍視為未變動（不列入 reloaded）
		if prevRef != nil && prevRef.Checksum == sum {
			// 僅更新 mtime，其餘複用
			copy := *prevRef
			copy.MTime = rf.mtime
			pack.References[relPath] = &copy
			continue
		}

		ref := &models.Reference{
			Path:     relPath,
			Title:    extractTitle(body, rf.absPath),
			Body:     body,
			MTime:    rf.mtime,
			Checksum: sum,
			Tokens:   estimateTokens(body),
			Keywords: extractKeywords(body),
		}
		pack.References[relPath] = ref
		reloaded = append(reloaded, relPath)
	}

	pack.RefsChecksum = aggregateRefsChecksum(pack.References)

	l.mu.Lock()
	l.cache[agentID] = pack
	l.mu.Unlock()

	// cached 定義：完全未重讀任何檔案
	cached = len(reloaded) == 0 && prev != nil
	return pack, cached, reloaded, nil
}

// ---- 內部 helper ----

type refFileInfo struct {
	relPath string // references/foo.md
	absPath string
	mtime   time.Time
}

func scanReferenceFiles(refsDir string) ([]refFileInfo, error) {
	var out []refFileInfo
	info, err := os.Stat(refsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil // references/ 不存在視為空
		}
		return nil, err
	}
	if !info.IsDir() {
		return out, nil
	}

	err = filepath.WalkDir(refsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Dir(refsDir), path)
		if err != nil {
			return err
		}
		out = append(out, refFileInfo{
			relPath: filepath.ToSlash(rel),
			absPath: path,
			mtime:   fi.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].relPath < out[j].relPath })
	return out, nil
}

func sameRefMTimes(prev *models.SkillPack, files []refFileInfo) bool {
	if len(prev.References) != len(files) {
		return false
	}
	for _, f := range files {
		r, ok := prev.References[f.relPath]
		if !ok {
			return false
		}
		if !r.MTime.Equal(f.mtime) {
			return false
		}
	}
	return true
}

func readAndHash(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(data)
	return string(data), hex.EncodeToString(sum[:]), nil
}

func aggregateRefsChecksum(refs map[string]*models.Reference) string {
	keys := make([]string, 0, len(refs))
	for k := range refs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte(":"))
		h.Write([]byte(refs[k].Checksum))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

var (
	reH1     = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)
	reH2H3   = regexp.MustCompile(`(?m)^##{1,2}\s+(.+?)\s*$`)
	reBold   = regexp.MustCompile(`\*\*([^*\n]{2,40})\*\*`)
	reNonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

func extractTitle(body, absPath string) string {
	if m := reH1.FindStringSubmatch(body); len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	name := filepath.Base(absPath)
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func extractKeywords(body string) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, m := range reH1.FindAllStringSubmatch(body, -1) {
		add(m[1])
	}
	for _, m := range reH2H3.FindAllStringSubmatch(body, -1) {
		add(m[1])
	}
	for _, m := range reBold.FindAllStringSubmatch(body, -1) {
		add(m[1])
	}
	return out
}

// estimateTokens 粗估 token 數：英文 ~ 每 4 字元 1 token，中文每字 1 token
// 實作上以 rune 為單位：ASCII / 4 + 非 ASCII rune 數
func estimateTokens(s string) int {
	ascii := 0
	nonAscii := 0
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			nonAscii++
		}
	}
	return ascii/4 + nonAscii
}
