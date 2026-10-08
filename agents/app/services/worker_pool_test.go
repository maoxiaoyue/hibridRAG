package services

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPool_RunsJobs(t *testing.T) {
	p := NewWorkerPool(2, 4)
	defer p.Stop()
	var n atomic.Int32
	for i := 0; i < 5; i++ {
		if err := p.Submit(context.Background(), func() { n.Add(1) }); err != nil {
			t.Fatal(err)
		}
	}
	// 等所有 job 跑完
	deadline := time.Now().Add(time.Second)
	for n.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if n.Load() != 5 {
		t.Fatalf("want 5 runs, got %d", n.Load())
	}
}

func TestWorkerPool_TrySubmitFullReturnsErr(t *testing.T) {
	p := NewWorkerPool(1, 1)
	defer p.Stop()
	block := make(chan struct{})
	// 第一個 job 卡住 worker
	_ = p.TrySubmit(func() { <-block })
	// 第二個塞滿 queue
	_ = p.TrySubmit(func() {})
	// 第三個應該 ErrPoolFull
	if err := p.TrySubmit(func() {}); err != ErrPoolFull {
		t.Fatalf("want ErrPoolFull, got %v", err)
	}
	close(block)
}

func TestWorkerPool_StopDrains(t *testing.T) {
	p := NewWorkerPool(2, 4)
	var n atomic.Int32
	for i := 0; i < 4; i++ {
		_ = p.Submit(context.Background(), func() {
			time.Sleep(5 * time.Millisecond)
			n.Add(1)
		})
	}
	p.Stop()
	if n.Load() != 4 {
		t.Fatalf("Stop should drain all jobs; got %d", n.Load())
	}
	// Stop 後再投失敗
	if err := p.TrySubmit(func() {}); err != ErrPoolClosed {
		t.Fatalf("want ErrPoolClosed, got %v", err)
	}
}

func TestWorkerPool_JobPanicDoesNotKillWorker(t *testing.T) {
	p := NewWorkerPool(1, 4)
	defer p.Stop()
	_ = p.TrySubmit(func() { panic("boom") })
	var done atomic.Bool
	_ = p.TrySubmit(func() { done.Store(true) })
	deadline := time.Now().Add(time.Second)
	for !done.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !done.Load() {
		t.Fatal("worker died after panic")
	}
}
