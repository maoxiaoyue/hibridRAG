// evalrun — 跑 eval dataset：對每個案例呼叫 agents 的 Chat，
// 對有 expected.answer 的案例算 BLEU，印總表 + 寫結果 JSON。
//
//	go run ./cmd/run -cases cases -addr 127.0.0.1:9091
//
// LLM-as-judge（agent_critic）尚未接上——見 evals 計畫 Phase 3。目前只有
// BLEU 這條訊號；沒有 expected.answer 的案例（例如只給 key_points 的）
// 會被標記 skipped，不計入分數，留給 Phase 3 評。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"evals/pkg/agentclient"
	"evals/pkg/bleu"
	"evals/pkg/dataset"
)

// caseResult 單一案例的跑分結果，同時是 JSON 輸出的一筆。
type caseResult struct {
	ID       string  `json:"id"`
	AgentID  string  `json:"agent_id"`
	Category string  `json:"category"`
	Question string  `json:"question"`
	Answer   string  `json:"answer,omitempty"`
	Expected string  `json:"expected,omitempty"`
	BLEU     float64 `json:"bleu,omitempty"`
	Skipped  string  `json:"skipped,omitempty"` // 非空 = 未評分（原因見此欄位）
	Error    string  `json:"error,omitempty"`   // 非空 = Chat 呼叫失敗
}

func main() {
	casesDir := flag.String("cases", "cases", "eval 案例目錄（*.yaml）")
	addr := flag.String("addr", "127.0.0.1:9091", "agents gRPC 位址（host:port）")
	insecureTLS := flag.Bool("insecure-tls", true, "跳過 TLS 憑證驗證（agents 預設自簽）")
	agentFilter := flag.String("agent", "", "只跑指定 agent_id（空 = 全部）")
	categoryFilter := flag.String("category", "", "只跑指定 category（空 = 全部）")
	timeout := flag.Duration("timeout", 60*time.Second, "單一案例逾時")
	outPath := flag.String("out", filepath.Join("results", "latest.json"), "結果 JSON 輸出路徑")
	flag.Parse()

	cases, err := dataset.LoadDir(*casesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load dataset: %v\n", err)
		os.Exit(1)
	}
	if errs := dataset.Validate(cases); len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "dataset 未通過驗證，先跑 `go run ./cmd/validate` 修好：")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  %v\n", e)
		}
		os.Exit(1)
	}

	client, err := agentclient.New(*addr, *insecureTLS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect agents %s: %v\n", *addr, err)
		os.Exit(1)
	}
	defer client.Close()

	var results []caseResult
	for _, c := range cases {
		if *agentFilter != "" && c.AgentID != *agentFilter {
			continue
		}
		if *categoryFilter != "" && c.Category != *categoryFilter {
			continue
		}
		results = append(results, runCase(client, c, *timeout))
	}

	printTable(results)

	if err := writeResults(*outPath, results); err != nil {
		fmt.Fprintf(os.Stderr, "write results: %v\n", err)
	} else {
		fmt.Printf("\nresults written to %s\n", *outPath)
	}
}

func runCase(client *agentclient.Client, c dataset.Case, timeout time.Duration) caseResult {
	r := caseResult{
		ID:       c.ID,
		AgentID:  c.AgentID,
		Category: c.Category,
		Question: c.Input.Question,
		Expected: c.Expected.Answer,
	}

	answer, err := client.Ask(context.Background(), c.AgentID, c.Input.Question, timeout)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.Answer = answer

	if c.Expected.Answer == "" {
		r.Skipped = "no expected.answer（BLEU 需要參考答案；key_points 類案例留給 LLM-as-judge，見 evals 計畫 Phase 3）"
		return r
	}
	r.BLEU = bleu.Score(answer, c.Expected.Answer)
	return r
}

func printTable(results []caseResult) {
	var scored, skipped, failed int
	var bleuSum float64

	fmt.Printf("%-14s %-10s %-10s %-8s %s\n", "ID", "AGENT", "CATEGORY", "BLEU", "STATUS")
	fmt.Println(strings.Repeat("-", 78))
	for _, r := range results {
		status := "ok"
		bleuStr := "-"
		switch {
		case r.Error != "":
			status = "ERROR: " + r.Error
			failed++
		case r.Skipped != "":
			status = "skipped"
			skipped++
		default:
			bleuStr = fmt.Sprintf("%.3f", r.BLEU)
			bleuSum += r.BLEU
			scored++
		}
		fmt.Printf("%-14s %-10s %-10s %-8s %s\n", r.ID, r.AgentID, r.Category, bleuStr, status)
	}

	fmt.Println(strings.Repeat("-", 78))
	fmt.Printf("total=%d scored=%d skipped=%d failed=%d", len(results), scored, skipped, failed)
	if scored > 0 {
		fmt.Printf(" avg_bleu=%.3f", bleuSum/float64(scored))
	}
	fmt.Println()
}

func writeResults(path string, results []caseResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
