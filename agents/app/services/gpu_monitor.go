// gpu_monitor.go — Ollama / GPU 使用狀況監控。
//
// 兩條觀測腿（GPU 通常在遠端 Ollama 主機上，agents 不一定與 GPU 同機）：
//  1. Ollama `/api/ps`（遠端可用）：每個 host 目前載入的模型、各自佔用的
//     VRAM（size_vram）。size_vram < size 代表模型部分溢出到 CPU RAM——VRAM
//     不夠的典型徵兆，推理會顯著變慢，值得警告。
//  2. 本機 `nvidia-smi`（best-effort）：agents 與 GPU 同機部署時才有意義；
//     找不到執行檔就自動停用，不影響第 1 條腿。
//
// 三個消費面：
//   - CheckBeforeInference：推理（Agent 1-4 管線）開始前呼叫，快取過期才重新
//     取樣（不會每輪工具迭代都打一次），超過門檻只記 warning 不擋請求——
//     GPU 忙不該讓對話直接失敗，慢總比錯好。
//   - Run：背景 goroutine，每 idleInterval（預設 10 分鐘）檢查一次；若期間
//     已有推理觸發的取樣（快取還新）就跳過——「空閒時才觀測」的語意。
//   - REST GET /api/monitor/gpu：回最近快照（?refresh=1 強制重取樣）。
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GPUSnapshot 一次觀測結果。
type GPUSnapshot struct {
	TakenAt  time.Time          `json:"taken_at"`
	Hosts    []OllamaHostStatus `json:"hosts"`              // 每個 Ollama host 的 /api/ps 結果
	Local    []LocalGPUStat     `json:"local,omitempty"`    // 本機 nvidia-smi（空 = 不可用/未啟用）
	Warnings []string           `json:"warnings,omitempty"` // 超過門檻/離線/CPU 溢出等警告
}

// OllamaHostStatus 一個 Ollama host 的載入狀態。
type OllamaHostStatus struct {
	URL       string              `json:"url"`
	Reachable bool                `json:"reachable"`
	Error     string              `json:"error,omitempty"`
	Models    []OllamaLoadedModel `json:"models,omitempty"`
	TotalVRAM int64               `json:"total_vram_bytes"` // 已載入模型的 VRAM 佔用總和
}

// OllamaLoadedModel /api/ps 回報的一顆已載入模型。
type OllamaLoadedModel struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size_bytes"`      // 模型佔用的總記憶體
	SizeVRAM  int64     `json:"size_vram_bytes"` // 其中在 VRAM 的部分；< Size = 部分溢出到 CPU
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// LocalGPUStat 本機 nvidia-smi 的一張卡。
type LocalGPUStat struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	UtilPercent int    `json:"util_percent"`
	MemUsedMiB  int    `json:"mem_used_mib"`
	MemTotalMiB int    `json:"mem_total_mib"`
	TempC       int    `json:"temp_c"`
}

// GPUMonitorOptions NewGPUMonitor 的設定；零值都有合理預設。
type GPUMonitorOptions struct {
	StaleAfter      time.Duration // 推理前檢查：快取超過此時長就重新取樣；<=0 → 30s
	IdleInterval    time.Duration // 背景觀測間隔；<=0 → 10 分鐘
	UseNvidiaSMI    bool          // 嘗試本機 nvidia-smi（找不到執行檔自動停用）
	UtilWarnPercent int           // 本機 GPU 使用率警告門檻；<=0 → 95
	VRAMWarnPercent int           // 本機 VRAM 佔用率警告門檻；<=0 → 90
}

// GPUMonitor 監控 Ollama host 與（可選）本機 GPU。方法皆容忍 nil receiver，
// 未接線時所有呼叫都是 no-op——與 AgentServer 其他可選依賴同一慣例。
type GPUMonitor struct {
	urls         []string
	http         *http.Client
	smiPath      string // 空 = 本機 nvidia-smi 不可用
	staleAfter   time.Duration
	idleInterval time.Duration
	utilWarn     int
	vramWarn     int

	mu   sync.Mutex
	last *GPUSnapshot
}

// NewGPUMonitor urls 為要觀測的 Ollama base URL 清單（如 http://192.168.50.104:11434）。
func NewGPUMonitor(urls []string, opt GPUMonitorOptions) *GPUMonitor {
	if opt.StaleAfter <= 0 {
		opt.StaleAfter = 30 * time.Second
	}
	if opt.IdleInterval <= 0 {
		opt.IdleInterval = 10 * time.Minute
	}
	if opt.UtilWarnPercent <= 0 {
		opt.UtilWarnPercent = 95
	}
	if opt.VRAMWarnPercent <= 0 {
		opt.VRAMWarnPercent = 90
	}
	m := &GPUMonitor{
		urls:         urls,
		http:         &http.Client{Timeout: 5 * time.Second},
		staleAfter:   opt.StaleAfter,
		idleInterval: opt.IdleInterval,
		utilWarn:     opt.UtilWarnPercent,
		vramWarn:     opt.VRAMWarnPercent,
	}
	if opt.UseNvidiaSMI {
		if p, err := exec.LookPath("nvidia-smi"); err == nil {
			m.smiPath = p
		} else {
			log.Printf("[gpu-monitor] nvidia-smi not found on this host — local GPU stats disabled (Ollama /api/ps only)")
		}
	}
	return m
}

// Last 最近一次快照（可能為 nil：還沒取樣過）。
func (m *GPUMonitor) Last() *GPUSnapshot {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

// CheckBeforeInference 推理開始前的狀態確認：快取過期才重新取樣（同一輪工具
// 迭代不會重複打），警告已在 Sample 內記 log。永不阻擋推理、永不回傳錯誤。
func (m *GPUMonitor) CheckBeforeInference(ctx context.Context) *GPUSnapshot {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	fresh := m.last != nil && time.Since(m.last.TakenAt) < m.staleAfter
	last := m.last
	m.mu.Unlock()
	if fresh {
		return last
	}
	sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return m.Sample(sctx)
}

// Run 背景觀測迴圈：每 idleInterval 醒來一次，若快取仍新（期間有推理觸發過
// 取樣）就跳過——空閒時才主動觀測。ctx 取消即退出。
func (m *GPUMonitor) Run(ctx context.Context) {
	if m == nil {
		return
	}
	ticker := time.NewTicker(m.idleInterval)
	defer ticker.Stop()
	log.Printf("[gpu-monitor] idle watcher active: hosts=%v interval=%s nvidia-smi=%v",
		m.urls, m.idleInterval, m.smiPath != "")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			fresh := m.last != nil && time.Since(m.last.TakenAt) < m.idleInterval
			m.mu.Unlock()
			if fresh {
				continue // 非空閒（推理路徑剛取樣過），這輪不重複觀測
			}
			sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			snap := m.Sample(sctx)
			cancel()
			log.Printf("[gpu-monitor] idle check: %s", summarize(snap))
		}
	}
}

// Sample 取一次新快照並更新快取；警告記進 snapshot 並打 log。
func (m *GPUMonitor) Sample(ctx context.Context) *GPUSnapshot {
	if m == nil {
		return nil
	}
	snap := &GPUSnapshot{TakenAt: time.Now()}

	for _, u := range m.urls {
		host := OllamaHostStatus{URL: u}
		models, err := m.fetchOllamaPS(ctx, u)
		if err != nil {
			host.Error = err.Error()
			snap.Warnings = append(snap.Warnings, fmt.Sprintf("ollama %s unreachable: %v", u, err))
		} else {
			host.Reachable = true
			host.Models = models
			for _, md := range models {
				host.TotalVRAM += md.SizeVRAM
				// size_vram < size：模型沒有完整放進 VRAM，部分層跑 CPU——推理會慢很多
				if md.Size > 0 && md.SizeVRAM < md.Size {
					pct := float64(md.SizeVRAM) / float64(md.Size) * 100
					snap.Warnings = append(snap.Warnings, fmt.Sprintf(
						"model %s on %s only %.0f%% in VRAM (partially offloaded to CPU — inference will be slow)",
						md.Name, u, pct))
				}
			}
		}
		snap.Hosts = append(snap.Hosts, host)
	}

	if m.smiPath != "" {
		if stats, err := m.sampleNvidiaSMI(ctx); err != nil {
			snap.Warnings = append(snap.Warnings, "nvidia-smi failed: "+err.Error())
		} else {
			snap.Local = stats
			for _, g := range stats {
				if g.UtilPercent >= m.utilWarn {
					snap.Warnings = append(snap.Warnings, fmt.Sprintf(
						"GPU%d (%s) utilization %d%% >= %d%%", g.Index, g.Name, g.UtilPercent, m.utilWarn))
				}
				if g.MemTotalMiB > 0 && g.MemUsedMiB*100/g.MemTotalMiB >= m.vramWarn {
					snap.Warnings = append(snap.Warnings, fmt.Sprintf(
						"GPU%d (%s) VRAM %d/%d MiB >= %d%%", g.Index, g.Name, g.MemUsedMiB, g.MemTotalMiB, m.vramWarn))
				}
			}
		}
	}

	for _, w := range snap.Warnings {
		log.Printf("[gpu-monitor] WARN: %s", w)
	}

	m.mu.Lock()
	m.last = snap
	m.mu.Unlock()
	return snap
}

// ── Ollama /api/ps ───────────────────────────────────────────────────────────

// ollamaPSResp /api/ps 的回應形狀（只取需要的欄位）。
type ollamaPSResp struct {
	Models []struct {
		Name      string    `json:"name"`
		Size      int64     `json:"size"`
		SizeVRAM  int64     `json:"size_vram"`
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"models"`
}

func (m *GPUMonitor) fetchOllamaPS(ctx context.Context, baseURL string) ([]OllamaLoadedModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+"/api/ps", nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseOllamaPS(body)
}

func parseOllamaPS(body []byte) ([]OllamaLoadedModel, error) {
	var r ollamaPSResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode /api/ps: %w", err)
	}
	out := make([]OllamaLoadedModel, 0, len(r.Models))
	for _, md := range r.Models {
		out = append(out, OllamaLoadedModel{
			Name: md.Name, Size: md.Size, SizeVRAM: md.SizeVRAM, ExpiresAt: md.ExpiresAt,
		})
	}
	return out, nil
}

// ── 本機 nvidia-smi ──────────────────────────────────────────────────────────

func (m *GPUMonitor) sampleNvidiaSMI(ctx context.Context) ([]LocalGPUStat, error) {
	cmd := exec.CommandContext(ctx, m.smiPath,
		"--query-gpu=index,name,utilization.gpu,memory.used,memory.total,temperature.gpu",
		"--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseNvidiaSMI(string(out)), nil
}

// parseNvidiaSMI 解析 csv,noheader,nounits 輸出；壞行跳過不失敗。
func parseNvidiaSMI(out string) []LocalGPUStat {
	var stats []LocalGPUStat
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 6 {
			continue
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		idx, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		util, _ := strconv.Atoi(fields[2])
		memUsed, _ := strconv.Atoi(fields[3])
		memTotal, _ := strconv.Atoi(fields[4])
		temp, _ := strconv.Atoi(fields[5])
		stats = append(stats, LocalGPUStat{
			Index: idx, Name: fields[1], UtilPercent: util,
			MemUsedMiB: memUsed, MemTotalMiB: memTotal, TempC: temp,
		})
	}
	return stats
}

// summarize 一行摘要（idle check log 用）。
func summarize(s *GPUSnapshot) string {
	if s == nil {
		return "no snapshot"
	}
	var parts []string
	for _, h := range s.Hosts {
		if !h.Reachable {
			parts = append(parts, fmt.Sprintf("%s: UNREACHABLE", h.URL))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %d model(s) loaded, %.1f GiB VRAM",
			h.URL, len(h.Models), float64(h.TotalVRAM)/(1<<30)))
	}
	for _, g := range s.Local {
		parts = append(parts, fmt.Sprintf("GPU%d %d%% util %d/%d MiB %d°C",
			g.Index, g.UtilPercent, g.MemUsedMiB, g.MemTotalMiB, g.TempC))
	}
	if len(s.Warnings) > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s)", len(s.Warnings)))
	}
	if len(parts) == 0 {
		return "nothing to report"
	}
	return strings.Join(parts, "; ")
}
