// cmd/cleanup — Kùzu 圖譜的 example.com 清理（dry-run 優先）。
//
// 刪除 description 或 source_file 含 "example.com" 的 Entity 節點（web_browse 測試
// 殘留）。PG + Cassandra 的清理是另一支：agent_memory/cmd/cleanup。
//
// 重要：Kùzu 是 embedded 單行程鎖檔——**agent_graph 服務執行中無法開檔**，跑此工具
// 前要先停掉 agent_graph 服務。Windows 需把 kuzu_shared.dll 加進 PATH（見 CLAUDE.md）。
//
// 用法：
//
//	go run ./cmd/cleanup                # 預覽（dry-run）
//	go run ./cmd/cleanup --apply        # 真的刪（DETACH DELETE 連帶關係）
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"agent_graph/app/services"
	"agent_graph/config"
)

const marker = "example.com"

// whereExample 命中條件：description 或 source_file 含 example.com。
const whereExample = `n.description CONTAINS $m OR n.source_file CONTAINS $m`

func main() {
	var (
		cfgPath = flag.String("config", "config/config.yaml", "config file")
		apply   = flag.Bool("apply", false, "actually delete (default: dry-run, count only)")
	)
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal("load config: %v", err)
	}
	kuzuPath := cfg.Graph.KuzuPath
	if kuzuPath == "" {
		kuzuPath = "data/graph.kz"
	}

	mode := "DRY-RUN (nothing deleted; pass --apply to execute)"
	if *apply {
		mode = "APPLY (nodes will be deleted)"
	}
	fmt.Printf("=== agent_graph Kùzu cleanup — %s ===\n\n", mode)

	kc, err := services.NewKuzuClient(kuzuPath)
	if err != nil {
		fatal("open Kùzu at %s: %v (is agent_graph still running? it holds a single-process lock)", kuzuPath, err)
	}
	defer kc.Close()
	ctx := context.Background()

	// 計數
	rows, err := kc.ExecuteCypher(ctx,
		`MATCH (n:Entity) WHERE `+whereExample+` RETURN count(n) AS c`,
		map[string]any{"m": marker}, []string{"c"})
	if err != nil {
		fatal("count query: %v", err)
	}
	n := scalarInt(rows)
	fmt.Printf("[example.com] Kùzu Entity nodes: %d\n", n)

	if *apply && n > 0 {
		if _, err := kc.ExecuteCypher(ctx,
			`MATCH (n:Entity) WHERE `+whereExample+` DETACH DELETE n`,
			map[string]any{"m": marker}, nil); err != nil {
			fatal("delete: %v", err)
		}
		fmt.Printf("\n=== APPLY: %d node(s) deleted ===\n", n)
		return
	}
	fmt.Printf("\n=== %s: %d node(s) %s ===\n", mode, n,
		map[bool]string{true: "deleted", false: "would be deleted"}[*apply])
	if !*apply && n > 0 {
		fmt.Println("Re-run with --apply to delete them (stop agent_graph first).")
	}
}

func scalarInt(rows []map[string]any) int {
	if len(rows) == 0 {
		return 0
	}
	switch v := rows[0]["c"].(type) {
	case int64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
