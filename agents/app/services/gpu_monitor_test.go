package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseOllamaPS(t *testing.T) {
	body := `{"models":[
		{"name":"gemma4:e4b","size":8697776128,"size_vram":8697776128,"expires_at":"2026-07-12T15:04:05Z"},
		{"name":"qwen3-vl:4b","size":4200000000,"size_vram":2100000000}
	]}`
	models, err := parseOllamaPS([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2", len(models))
	}
	if models[0].Name != "gemma4:e4b" || models[0].SizeVRAM != models[0].Size {
		t.Errorf("model[0] unexpected: %+v", models[0])
	}
	// 第二顆 size_vram < size = CPU 溢出徵兆（Sample 會轉成 warning）
	if models[1].SizeVRAM >= models[1].Size {
		t.Errorf("model[1] should show partial offload: %+v", models[1])
	}
	if _, err := parseOllamaPS([]byte("not json")); err == nil {
		t.Error("invalid json should error")
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	out := "0, NVIDIA GeForce RTX 4090, 87, 20345, 24564, 71\n" +
		"1, NVIDIA GeForce RTX 3060, 3, 512, 12288, 45\n" +
		"garbage line without enough fields\n"
	stats := parseNvidiaSMI(out)
	if len(stats) != 2 {
		t.Fatalf("stats = %d, want 2 (garbage line skipped)", len(stats))
	}
	g := stats[0]
	if g.Index != 0 || g.UtilPercent != 87 || g.MemUsedMiB != 20345 || g.MemTotalMiB != 24564 || g.TempC != 71 {
		t.Errorf("gpu0 = %+v", g)
	}
	if stats[1].Name != "NVIDIA GeForce RTX 3060" {
		t.Errorf("gpu1 name = %q", stats[1].Name)
	}
	if got := parseNvidiaSMI(""); len(got) != 0 {
		t.Errorf("empty output should yield no stats, got %v", got)
	}
}

func TestGPUMonitorSampleAndWarnings(t *testing.T) {
	// 模擬一個 Ollama host：一顆模型部分溢出到 CPU
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"models":[{"name":"big-model:70b","size":40000000000,"size_vram":20000000000}]}`))
	}))
	defer srv.Close()

	m := NewGPUMonitor([]string{srv.URL, "http://127.0.0.1:1"}, GPUMonitorOptions{}) // 第二個 host 連不上
	snap := m.Sample(context.Background())
	if snap == nil || len(snap.Hosts) != 2 {
		t.Fatalf("snapshot hosts = %+v", snap)
	}
	if !snap.Hosts[0].Reachable || snap.Hosts[0].TotalVRAM != 20000000000 {
		t.Errorf("host[0] = %+v", snap.Hosts[0])
	}
	if snap.Hosts[1].Reachable {
		t.Error("host[1] should be unreachable")
	}
	// 應有兩個警告：CPU 溢出 + host 離線
	var offloadWarn, unreachableWarn bool
	for _, w := range snap.Warnings {
		if strings.Contains(w, "partially offloaded") {
			offloadWarn = true
		}
		if strings.Contains(w, "unreachable") {
			unreachableWarn = true
		}
	}
	if !offloadWarn || !unreachableWarn {
		t.Errorf("warnings = %v, want offload + unreachable", snap.Warnings)
	}
	// 快取更新
	if m.Last() != snap {
		t.Error("Last() should return the sampled snapshot")
	}
}

func TestCheckBeforeInferenceUsesCache(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	m := NewGPUMonitor([]string{srv.URL}, GPUMonitorOptions{StaleAfter: time.Hour})
	ctx := context.Background()
	m.CheckBeforeInference(ctx)
	m.CheckBeforeInference(ctx)
	m.CheckBeforeInference(ctx)
	if calls != 1 {
		t.Errorf("fresh cache should suppress re-sampling: %d calls, want 1", calls)
	}

	// nil receiver 容忍（未接線時 no-op）
	var nilMon *GPUMonitor
	if nilMon.CheckBeforeInference(ctx) != nil || nilMon.Last() != nil {
		t.Error("nil monitor should be a no-op")
	}
	nilMon.Run(ctx) // 不 panic 即可（立即 return）
}
