// worker_pool.go — 推理工作的有界 worker pool（限流）。
//
// 場景：agent_grpc.AgentServer.Inbound 收到 InboundReq 後，
//   - 原本：`go process(...)` 直接開無界 goroutine（突發流量會打爆 LLM / DB）
//   - 改為：投到 WorkerPool；佇列滿時 TrySubmit 回 false，handler 回 429
//
// 設計：
//   - N workers 各跑 for-range queue 直到 Stop()
//   - queue 容量 = QueueSize（含 in-flight）；用 buffered channel 達成 backpressure
//   - TrySubmit 非阻塞；Submit 會阻塞到有空位或 ctx 取消
//   - Stop() 關 channel 並等所有 worker 結束（drain in-flight）
package services

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrPoolFull 表示 worker pool 佇列已滿，應回上游 429 / busy。
var ErrPoolFull = errors.New("worker pool: queue full")

// ErrPoolClosed 表示 pool 已停止接收新工作。
var ErrPoolClosed = errors.New("worker pool: closed")

// Job 是 worker pool 執行的單位。
type Job func()

// WorkerPool 有界 goroutine pool。
type WorkerPool struct {
	queue    chan Job
	workers  int
	wg       sync.WaitGroup
	closed   atomic.Bool
	inflight atomic.Int64
}

// NewWorkerPool 建立並啟動 pool。workers<=0 → 8；queueSize<=0 → workers*4。
func NewWorkerPool(workers, queueSize int) *WorkerPool {
	if workers <= 0 {
		workers = 8
	}
	if queueSize <= 0 {
		queueSize = workers * 4
	}
	p := &WorkerPool{
		queue:   make(chan Job, queueSize),
		workers: workers,
	}
	p.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go p.run()
	}
	return p
}

func (p *WorkerPool) run() {
	defer p.wg.Done()
	for job := range p.queue {
		p.inflight.Add(1)
		// 保護 worker：任何 panic 不殺整個 pool
		func() {
			defer func() { _ = recover() }()
			job()
		}()
		p.inflight.Add(-1)
	}
}

// TrySubmit 非阻塞投遞；佇列滿回 ErrPoolFull。
func (p *WorkerPool) TrySubmit(j Job) error {
	if p.closed.Load() {
		return ErrPoolClosed
	}
	select {
	case p.queue <- j:
		return nil
	default:
		return ErrPoolFull
	}
}

// Submit 阻塞投遞；ctx 取消時回 ctx.Err()。
func (p *WorkerPool) Submit(ctx context.Context, j Job) error {
	if p.closed.Load() {
		return ErrPoolClosed
	}
	select {
	case p.queue <- j:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Inflight 返回正在執行的 job 數（不含已佇列未取）。
func (p *WorkerPool) Inflight() int64 { return p.inflight.Load() }

// Queued 返回目前在 channel 佇列中等待的 job 數。
func (p *WorkerPool) Queued() int { return len(p.queue) }

// Workers 返回 worker 數量。
func (p *WorkerPool) Workers() int { return p.workers }

// Stop 停止接收新工作，等待所有 in-flight + queued job 完成後返回。
func (p *WorkerPool) Stop() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	close(p.queue)
	p.wg.Wait()
}
