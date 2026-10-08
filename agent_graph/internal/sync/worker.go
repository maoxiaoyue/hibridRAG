// worker.go — orchestrates Obsidian vault sync: full scan on startup, then
// (if enabled) fsnotify-driven incremental re-sync. Deterministic WikiLink
// relations are always MERGEd directly; LLM-extracted relations are
// auto-merged above ConfidenceThreshold and otherwise written to
// proposed_relations for human review — see CLAUDE.md "主動式知識整理" design.
package sync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/maoxiaoyue/hypgo/pkg/logger"
	"github.com/uptrace/bun"

	"agent_graph/app/config"
	"agent_graph/app/models"
	"agent_graph/app/services"
)

// EmbedClient / ChatClient narrowed to what Worker needs (nil-able — a
// missing chat/embed client just disables that enrichment step, not the
// deterministic WikiLink sync).
type EmbedClient interface {
	EmbedOne(ctx context.Context, input string) ([]float32, error)
}

type ChatClient interface {
	ExtractEntitiesRelations(ctx context.Context, title, body string) (services.ExtractionResult, error)
}

// Worker syncs an Obsidian vault into the AGE graph (+ optional embeddings).
type Worker struct {
	vault               config.VaultConfig
	age                 services.GraphBackend
	vectors             services.VectorBackend
	embed               EmbedClient
	chat                ChatClient
	exporter            *services.VaultExporter
	db                  *bun.DB
	log                 *logger.Logger
	confidenceThreshold float64
}

// Option configures optional Worker dependencies.
type Option func(*Worker)

func WithEmbed(c EmbedClient) Option { return func(w *Worker) { w.embed = c } }
func WithChat(c ChatClient) Option   { return func(w *Worker) { w.chat = c } }

// WithExporter 接上圖譜→vault 鏡像匯出：SyncFile 寫完圖譜後通知受影響實體。
func WithExporter(e *services.VaultExporter) Option { return func(w *Worker) { w.exporter = e } }

// excludedDirsAbs sync 掃描/監聽要排除的子目錄絕對路徑：鏡像匯出目錄（避免
// exporter 產出的鏡像檔被再攝取的迴圈）+ 暫存目錄（temp 未分類、還不進圖譜）。
func (w *Worker) excludedDirsAbs() []string {
	if w.vault.Path == "" {
		return nil
	}
	names := w.vault.ExcludedDirNames()
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, filepath.Clean(filepath.Join(w.vault.Path, n)))
	}
	return out
}

// underExcludedDir path 是否位於任一排除目錄內（含目錄本身）。
func (w *Worker) underExcludedDir(path string) bool {
	p := filepath.Clean(path)
	for _, ex := range w.excludedDirsAbs() {
		if strings.EqualFold(p, ex) ||
			strings.HasPrefix(strings.ToLower(p), strings.ToLower(ex+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

// NewWorker builds a Worker. db is required (SyncState/ProposedRelation
// persistence); age/vectors may be disabled clients (see their Enabled()).
func NewWorker(vault config.VaultConfig, age services.GraphBackend, vectors services.VectorBackend, db *bun.DB, log *logger.Logger, confidenceThreshold float64, opts ...Option) *Worker {
	if confidenceThreshold <= 0 {
		confidenceThreshold = 0.8
	}
	w := &Worker{vault: vault, age: age, vectors: vectors, db: db, log: log, confidenceThreshold: confidenceThreshold}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Start runs an initial full scan, then (if vault.watch) launches the
// fsnotify watch loop in a background goroutine. A missing/empty vault path
// disables sync entirely (query-only mode) — not an error.
func (w *Worker) Start(ctx context.Context) {
	if w.vault.Path == "" {
		w.log.Info("Obsidian sync disabled (md.yaml vault.path is empty)")
		return
	}
	if _, err := os.Stat(w.vault.Path); err != nil {
		w.log.Warningf("Obsidian sync disabled: vault path %s not accessible: %v", w.vault.Path, err)
		return
	}

	go func() {
		n, err := w.FullScan(ctx)
		if err != nil {
			w.log.Warningf("initial vault scan error: %v", err)
		}
		w.log.Infof("Obsidian vault scan complete: %d files synced", n)

		if w.vault.Watch {
			w.watchLoop(ctx)
		}
	}()
}

// FullScan walks the vault once, syncing every matching file. Per-file errors
// are logged and skipped — one bad note shouldn't abort the whole scan.
func (w *Worker) FullScan(ctx context.Context) (int, error) {
	exts := extSet(w.vault.Extensions())
	n := 0
	err := filepath.WalkDir(w.vault.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			w.log.Warningf("walk %s: %v", path, err)
			return nil
		}
		if d.IsDir() {
			if w.underExcludedDir(path) {
				return fs.SkipDir // 鏡像匯出/暫存目錄不攝取
			}
			return nil
		}
		if !exts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if serr := w.SyncFile(ctx, path); serr != nil {
			w.log.Warningf("sync %s: %v", path, serr)
			return nil
		}
		n++
		return nil
	})
	return n, err
}

// SyncFile parses one file and MERGEs its entity/relations into the graph.
// Idempotent: unchanged content (by hash) is a no-op.
func (w *Worker) SyncFile(ctx context.Context, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	hash := sha256Hex(raw)

	var state models.SyncState
	err = w.db.NewSelect().Model(&state).Where("source_file = ?", path).Scan(ctx)
	if err == nil && state.ContentHash == hash {
		return nil // unchanged since last sync
	}

	note := ParseNote(path, raw)

	// touched 收集本次寫進圖譜的實體名，結尾一次通知鏡像匯出。
	var touched []string
	if w.age != nil && w.age.Enabled() {
		entity := models.EntityResult{
			Name: note.Title, Category: note.Category, Description: note.Description, SourceFile: path,
		}
		if err := w.age.UpsertEntity(ctx, entity); err != nil {
			return fmt.Errorf("upsert entity %q: %w", note.Title, err)
		}
		touched = append(touched, note.Title)
		for _, link := range note.WikiLinks {
			if err := w.age.UpsertRelation(ctx, note.Title, link, "links_to"); err != nil {
				w.log.Warningf("upsert relation %s -> %s: %v", note.Title, link, err)
				continue
			}
			touched = append(touched, link)
		}
	}

	if w.chat != nil {
		result, err := w.chat.ExtractEntitiesRelations(ctx, note.Title, note.Body)
		if err != nil {
			w.log.Warningf("llm extraction for %s: %v", path, err)
		} else {
			touched = append(touched, w.applyExtraction(ctx, path, result)...)
		}
	}

	if w.exporter != nil && len(touched) > 0 {
		w.exporter.Notify(touched...)
	}

	if w.embed != nil && w.vectors != nil && w.vectors.Enabled() && note.Description != "" {
		vec, err := w.embed.EmbedOne(ctx, note.Description)
		if err != nil {
			w.log.Warningf("embed %s: %v", note.Title, err)
		} else if err := w.vectors.Upsert(ctx, note.Title, vec); err != nil {
			w.log.Warningf("store embedding for %s: %v", note.Title, err)
		}
	}

	_, err = w.db.NewInsert().
		Model(&models.SyncState{SourceFile: path, ContentHash: hash, LastSyncedAt: time.Now()}).
		On("CONFLICT (source_file) DO UPDATE").
		Set("content_hash = EXCLUDED.content_hash").
		Set("last_synced_at = EXCLUDED.last_synced_at").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("record sync state: %w", err)
	}
	return nil
}

// applyExtraction routes LLM-extracted entities/relations: entities are
// always upserted (a stub node is low-risk even if the name is imperfect);
// relations at/above confidenceThreshold auto-merge, below it they land in
// proposed_relations for human review instead of touching the graph.
// 回傳實際寫進圖譜的實體名（供 vault 鏡像匯出通知）。
func (w *Worker) applyExtraction(ctx context.Context, sourceFile string, result services.ExtractionResult) []string {
	var touched []string
	if w.age != nil && w.age.Enabled() {
		for _, e := range result.Entities {
			if e.Name == "" {
				continue
			}
			if err := w.age.UpsertEntity(ctx, models.EntityResult{
				Name: e.Name, Category: e.Category, Description: e.Description, SourceFile: sourceFile,
			}); err != nil {
				w.log.Warningf("upsert LLM-extracted entity %q: %v", e.Name, err)
				continue
			}
			touched = append(touched, e.Name)
		}
	}

	for _, r := range result.Relations {
		if r.Source == "" || r.Target == "" {
			continue
		}
		if r.Confidence >= w.confidenceThreshold {
			if w.age != nil && w.age.Enabled() {
				if err := w.age.UpsertRelation(ctx, r.Source, r.Target, r.Type); err != nil {
					w.log.Warningf("auto-merge relation %s -> %s: %v", r.Source, r.Target, err)
				} else {
					touched = append(touched, r.Source, r.Target)
				}
			}
			continue
		}
		pr := models.ProposedRelation{
			ID: newProposalID(), SourceEntity: r.Source, TargetEntity: r.Target, RelationType: r.Type,
			Confidence: r.Confidence, SourceFile: sourceFile, ExtractedBy: "ollama-llm",
			Status: "pending", CreatedAt: time.Now(),
		}
		if _, err := w.db.NewInsert().Model(&pr).Exec(ctx); err != nil {
			w.log.Warningf("propose relation %s -> %s: %v", r.Source, r.Target, err)
		}
	}
	return touched
}

// watchLoop debounces fsnotify events per-path before re-syncing — editors
// routinely fire several Write events for one save.
func (w *Worker) watchLoop(ctx context.Context) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		w.log.Warningf("fsnotify init failed: %v — vault watch disabled (initial scan still ran)", err)
		return
	}
	defer watcher.Close()

	if err := addRecursive(watcher, w.vault.Path, w.excludedDirsAbs()); err != nil {
		w.log.Warningf("fsnotify watch %s failed: %v — vault watch disabled", w.vault.Path, err)
		return
	}
	w.log.Infof("Obsidian vault watch active: %s", w.vault.Path)

	exts := extSet(w.vault.Extensions())
	debounce := w.vault.Debounce()
	timers := map[string]*time.Timer{}
	var mu sync.Mutex

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 || !exts[strings.ToLower(filepath.Ext(ev.Name))] {
				continue
			}
			if w.underExcludedDir(ev.Name) {
				continue // 鏡像匯出/暫存目錄的變動不回攝取
			}
			path := ev.Name
			mu.Lock()
			if t, exists := timers[path]; exists {
				t.Stop()
			}
			timers[path] = time.AfterFunc(debounce, func() {
				if err := w.SyncFile(context.Background(), path); err != nil {
					w.log.Warningf("sync %s: %v", path, err)
				}
				mu.Lock()
				delete(timers, path)
				mu.Unlock()
			})
			mu.Unlock()
		case werr, ok := <-watcher.Errors:
			if !ok {
				return
			}
			w.log.Warningf("fsnotify error: %v", werr)
		}
	}
}

// addRecursive adds root and every subdirectory to the watcher — fsnotify
// doesn't watch recursively on its own. Subdirectories created after startup
// won't be picked up until the next restart (documented limitation; a full
// re-scan on start recovers any content missed this way). skipDirs（鏡像匯出
// + 暫存目錄）整棵跳過。
func addRecursive(watcher *fsnotify.Watcher, root string, skipDirs []string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			clean := filepath.Clean(path)
			for _, skip := range skipDirs {
				if skip != "" && strings.EqualFold(clean, skip) {
					return fs.SkipDir
				}
			}
			if werr := watcher.Add(path); werr != nil {
				return fmt.Errorf("watch %s: %w", path, werr)
			}
		}
		return nil
	})
}

func extSet(exts []string) map[string]bool {
	set := make(map[string]bool, len(exts))
	for _, e := range exts {
		set[strings.ToLower(e)] = true
	}
	return set
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newProposalID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "pr_" + hex.EncodeToString(b)
}
