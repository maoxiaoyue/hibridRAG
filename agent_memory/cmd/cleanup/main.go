// cmd/cleanup — 資料清理工具（dry-run 優先）。
//
// 兩件清理（依使用者 2026-07-10 指示）：
//  1. --demo        刪除「對話紀錄」表（episodic_memory）裡 agent_id='demo' 的列。
//                   刻意只碰對話紀錄，不動 personal/procedural/tool_memory（那是 persona 設定）。
//  2. --example-com 刪除內容含 "example.com" 的列，掃 mcp 庫全部相關 PG 表 + Cassandra
//                   long_term_memory（web_browse 測試殘留）。
//
// 預設 dry-run：只印「會刪幾筆、哪些表」，不真的刪。加 --apply 才執行 DELETE。
// Kùzu 圖譜實體的 example.com 清理是另一支：agent_graph/cmd/cleanup（Kùzu 單行程鎖檔，
// 需 agent_graph 服務停機才能開檔）。
//
// 用法（在能連到 192.168.50.201:5433 的機器上）：
//
//	go run ./cmd/cleanup --demo --example-com            # 預覽
//	go run ./cmd/cleanup --demo --example-com --apply    # 真的刪
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"agent_memory/config"
	"agent_memory/internal/cassandra"
	"agent_memory/internal/db"
)

const marker = "example.com"

// pgTarget 一張表 + 判斷「含 example.com」的 WHERE 條件（JSONB 欄位 cast text 再比對）。
type pgTarget struct {
	table string
	where string
}

var exampleTargets = []pgTarget{
	{"episodic_memory", "turns::text ILIKE '%' || $1 || '%' OR summary ILIKE '%' || $1 || '%'"},
	{"procedural_memory", "content ILIKE '%' || $1 || '%' OR heuristics::text ILIKE '%' || $1 || '%'"},
	{"personal_memory", "identity::text ILIKE '%' || $1 || '%' OR expression_dna::text ILIKE '%' || $1 || '%'"},
	{"tool_memory", "signature::text ILIKE '%' || $1 || '%' OR usage_stats::text ILIKE '%' || $1 || '%'"},
	{"shared_memory", "value::text ILIKE '%' || $1 || '%'"},
	{"ingest_jobs", "source_path ILIKE '%' || $1 || '%' OR metadata::text ILIKE '%' || $1 || '%'"},
	{"proposed_relations", "source_entity ILIKE '%' || $1 || '%' OR target_entity ILIKE '%' || $1 || '%' OR source_file ILIKE '%' || $1 || '%'"},
	{"graph_sync_state", "source_file ILIKE '%' || $1 || '%'"},
}

func main() {
	var (
		cfgPath    = flag.String("config", "config/config.yaml", "config file")
		doDemo     = flag.Bool("demo", false, "delete agent_id='demo' conversation records (episodic_memory)")
		doExample  = flag.Bool("example-com", false, "delete rows containing example.com (PG + Cassandra)")
		apply      = flag.Bool("apply", false, "actually delete (default: dry-run, count only)")
	)
	flag.Parse()

	if !*doDemo && !*doExample {
		fmt.Println("nothing to do: pass --demo and/or --example-com (add --apply to execute)")
		os.Exit(2)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal("load config: %v", err)
	}

	mode := "DRY-RUN (nothing deleted; pass --apply to execute)"
	if *apply {
		mode = "APPLY (rows will be deleted)"
	}
	fmt.Printf("=== agent_memory cleanup — %s ===\n\n", mode)

	if _, err := db.Init(&cfg.Database); err != nil {
		fatal("connect PG: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	total := 0
	if *doDemo {
		total += cleanupDemoPG(ctx, *apply)
	}
	if *doExample {
		total += cleanupExamplePG(ctx, *apply)
		total += cleanupExampleCassandra(ctx, cfg, *apply)
	}

	fmt.Printf("\n=== %s: %d row(s) %s ===\n", mode, total,
		map[bool]string{true: "deleted", false: "would be deleted"}[*apply])
	if !*apply && total > 0 {
		fmt.Println("Re-run with --apply to delete them.")
	}
}

// cleanupDemoPG episodic_memory 裡 agent_id='demo'。
func cleanupDemoPG(ctx context.Context, apply bool) int {
	if !tableExists(ctx, "episodic_memory") {
		fmt.Println("[demo] episodic_memory table not present — skipped")
		return 0
	}
	n := pgCount(ctx, "episodic_memory", "agent_id = $1", "demo")
	fmt.Printf("[demo] episodic_memory agent_id='demo': %d row(s)\n", n)
	if apply && n > 0 {
		pgDelete(ctx, "episodic_memory", "agent_id = $1", "demo")
	}
	return n
}

// cleanupExamplePG 掃各表含 example.com 的列。
func cleanupExamplePG(ctx context.Context, apply bool) int {
	total := 0
	for _, t := range exampleTargets {
		if !tableExists(ctx, t.table) {
			continue // 該服務的表未 migrate，跳過
		}
		n := pgCount(ctx, t.table, t.where, marker)
		if n == 0 {
			continue
		}
		fmt.Printf("[example.com] PG %s: %d row(s)\n", t.table, n)
		total += n
		if apply {
			pgDelete(ctx, t.table, t.where, marker)
		}
	}
	if total == 0 {
		fmt.Println("[example.com] PG: no matching rows")
	}
	return total
}

// cleanupExampleCassandra long_term_memory 全表掃描，in-Go 過濾 example.com。
// Cassandra 沒有 LIKE，非 key 欄位也不能直接 WHERE，故全掃 + Go 端判斷 + 依 PK 刪。
func cleanupExampleCassandra(ctx context.Context, cfg *config.Config, apply bool) int {
	if len(cfg.Cassandra.Hosts) == 0 {
		fmt.Println("[example.com] Cassandra: not configured — skipped")
		return 0
	}
	if _, err := cassandra.Init(cfg.Cassandra); err != nil {
		fmt.Printf("[example.com] Cassandra: connect failed (%v) — skipped\n", err)
		return 0
	}
	defer cassandra.Close()
	sess := cassandra.Get().Session()

	iter := sess.Query(`SELECT memory_id, content, summary, source FROM long_term_memory`).WithContext(ctx).Iter()
	var id, content, summary, source string
	var hitIDs []string
	for iter.Scan(&id, &content, &summary, &source) {
		if containsMarker(content) || containsMarker(summary) || containsMarker(source) {
			hitIDs = append(hitIDs, id)
		}
	}
	if err := iter.Close(); err != nil {
		fmt.Printf("[example.com] Cassandra scan error: %v\n", err)
		return 0
	}
	fmt.Printf("[example.com] Cassandra long_term_memory: %d row(s)\n", len(hitIDs))
	if apply {
		for _, mid := range hitIDs {
			if err := sess.Query(`DELETE FROM long_term_memory WHERE memory_id = ?`, mid).WithContext(ctx).Exec(); err != nil {
				fmt.Printf("  delete %s failed: %v\n", mid, err)
			}
		}
	}
	return len(hitIDs)
}

func containsMarker(s string) bool { return strings.Contains(strings.ToLower(s), marker) }

// ── PG helpers（用 hidb 的 bun ORM 跑 raw SQL）────────────────────────────────

func tableExists(ctx context.Context, table string) bool {
	var reg *string
	err := db.DB().NewRaw("SELECT to_regclass(?)", "public."+table).Scan(ctx, &reg)
	return err == nil && reg != nil
}

func pgCount(ctx context.Context, table, where, arg string) int {
	var n int
	q := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", table, where)
	if err := db.DB().NewRaw(q, arg).Scan(ctx, &n); err != nil {
		fmt.Printf("  count %s failed: %v\n", table, err)
		return 0
	}
	return n
}

func pgDelete(ctx context.Context, table, where, arg string) {
	q := fmt.Sprintf("DELETE FROM %s WHERE %s", table, where)
	if _, err := db.DB().NewRaw(q, arg).Exec(ctx); err != nil {
		fmt.Printf("  delete %s failed: %v\n", table, err)
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
