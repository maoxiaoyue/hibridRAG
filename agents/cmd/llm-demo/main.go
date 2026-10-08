// Command llm-demo 端到端驗證：SKILL.md → persona.Parse → ToInstruction → LLMClient.Chat
//
// 用法：
//
//	# 單輪：給定 prompt 直接執行一次
//	go run ./cmd/llm-demo --skill skills/demo/SKILL.md --prompt "Go 該怎麼處理 graceful shutdown？"
//
//	# 互動模式：未給 --prompt 進入 REPL，保留 conversation history
//	go run ./cmd/llm-demo --skill skills/demo/SKILL.md
//
//	# 顯示渲染後的 instruction（檢查 prompt 品質）
//	go run ./cmd/llm-demo --skill skills/demo/SKILL.md --show-instruction --prompt "你是誰？"
//
// 設定來源：config/llm.yaml（可用 --config 覆寫）
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent/app/services"
	"agent/internal/agent/persona"
)

func main() {
	var (
		skillPath  = flag.String("skill", "", "path to SKILL.md (required)")
		configPath = flag.String("config", "config/llm.yaml", "path to llm config yaml")
		prompt     = flag.String("prompt", "", "single-turn user prompt; omit for REPL mode")
		showInstr  = flag.Bool("show-instruction", false, "print rendered system prompt before chatting")
		temp       = flag.Float64("temp", 0.7, "temperature 0.0~2.0")
		maxTok     = flag.Int("max-tokens", 1024, "max output tokens")
		timeoutSec = flag.Int("timeout", 120, "per-request timeout (seconds)")
	)
	flag.Parse()

	if *skillPath == "" {
		dief("--skill is required (try: --skill skills/demo/SKILL.md)")
	}

	// 1. 載入 LLM config + 建立 client
	cfg, err := services.LoadLLMConfig(*configPath)
	if err != nil {
		dief("load config: %v", err)
	}
	client, err := services.NewLLMClient(*cfg)
	if err != nil {
		dief("new client: %v", err)
	}
	fmt.Printf("📡 LLM ready: provider=%s mode=%s\n", client.Provider(), cfg.Mode)
	switch {
	case cfg.Ollama != nil:
		fmt.Printf("   url=%s model=%s\n", cfg.Ollama.URL, cfg.Ollama.Model)
	case cfg.API != nil:
		fmt.Printf("   provider=%s model=%s\n", cfg.API.Provider, cfg.API.Model)
	}

	// 2. 讀取並解析 SKILL.md
	raw, err := os.ReadFile(*skillPath)
	if err != nil {
		dief("read skill: %v", err)
	}
	result := persona.Parse(string(raw))
	if result.IsEmpty() {
		dief("SKILL.md %q parsed to empty result", *skillPath)
	}
	instruction := result.ToInstruction()
	fmt.Printf("📜 Loaded SKILL.md: %s (agent=%q, %d mental models)\n",
		filepath.Base(*skillPath), result.AgentName, len(result.MentalModels))

	if *showInstr {
		fmt.Println(strings.Repeat("─", 60))
		fmt.Println(instruction)
		fmt.Println(strings.Repeat("─", 60))
	}

	opts := chatOpts{
		temp:       float32(*temp),
		maxTokens:  *maxTok,
		timeoutSec: *timeoutSec,
	}

	// 3. 單輪或 REPL
	if *prompt != "" {
		runSingle(client, instruction, *prompt, opts)
		return
	}
	runREPL(client, instruction, opts)
}

type chatOpts struct {
	temp       float32
	maxTokens  int
	timeoutSec int
}

func runSingle(client services.LLMClient, instruction, prompt string, opts chatOpts) {
	msgs := []services.ChatMessage{
		{Role: services.RoleSystem, Content: instruction},
		{Role: services.RoleUser, Content: prompt},
	}
	fmt.Printf("\n👤 USER\n%s\n", prompt)
	resp := chatOnce(client, msgs, opts)
	if resp == nil {
		os.Exit(1)
	}
	fmt.Printf("\n🤖 BOT\n%s\n", resp.Content)
	printStats(resp, 0)
}

func runREPL(client services.LLMClient, instruction string, opts chatOpts) {
	msgs := []services.ChatMessage{
		{Role: services.RoleSystem, Content: instruction},
	}
	fmt.Println("\n💬 REPL mode (type 'exit' or Ctrl+D to quit; 'reset' to clear history)")
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024) // 允許長 input

	for {
		fmt.Print("\n👤 USER: ")
		if !sc.Scan() {
			fmt.Println()
			return
		}
		line := strings.TrimSpace(sc.Text())
		switch line {
		case "":
			continue
		case "exit", "quit":
			return
		case "reset":
			msgs = msgs[:1] // 保留 system，清掉對話
			fmt.Println("(history cleared)")
			continue
		}

		msgs = append(msgs, services.ChatMessage{Role: services.RoleUser, Content: line})
		resp := chatOnce(client, msgs, opts)
		if resp == nil {
			msgs = msgs[:len(msgs)-1] // 失敗則回滾 user 訊息，讓使用者重試
			continue
		}
		msgs = append(msgs, services.ChatMessage{Role: services.RoleAssistant, Content: resp.Content})
		fmt.Printf("\n🤖 BOT: %s\n", resp.Content)
		printStats(resp, len(msgs)/2)
	}
}

func chatOnce(client services.LLMClient, msgs []services.ChatMessage, opts chatOpts) *services.ChatResponse {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(opts.timeoutSec)*time.Second)
	defer cancel()

	start := time.Now()
	resp, err := client.Chat(ctx, services.ChatRequest{
		Messages:    msgs,
		Temperature: opts.temp,
		MaxTokens:   opts.maxTokens,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  chat failed after %.1fs: %v\n", time.Since(start).Seconds(), err)
		return nil
	}
	resp.Model = fmt.Sprintf("%s (%.1fs)", resp.Model, time.Since(start).Seconds())
	return resp
}

func printStats(resp *services.ChatResponse, turn int) {
	turnStr := ""
	if turn > 0 {
		turnStr = fmt.Sprintf(" turn=%d", turn)
	}
	fmt.Printf("   [model=%s | tokens=%d (in=%d, out=%d) | finish=%s%s]\n",
		resp.Model,
		resp.Usage.TotalTokens, resp.Usage.PromptTokens, resp.Usage.CompletionTokens,
		resp.FinishReason, turnStr)
}

func dief(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "❌ "+format+"\n", args...)
	os.Exit(1)
}
