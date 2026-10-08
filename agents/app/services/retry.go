// retry.go — Dispatcher 重試輔助：依 DownstreamDefaults.MaxRetries / RetryBackoff
// 對 transient error 自動重試。
//
// 重試規則（保守 / 不重試會放大副作用的錯）：
//   - context.DeadlineExceeded（504 timeout）
//   - context.Canceled  → 不重試（caller 主動取消）
//   - gRPC codes：Unavailable / DeadlineExceeded / ResourceExhausted / Aborted
//   - 其他（含 InvalidArgument / NotFound / PermissionDenied / Unauthenticated）→ 不重試
//
// 退避：指數退避 + 隨機 jitter（±25%），上限 5s，避免雷霆。
package services

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// isRetryable 判斷一個錯誤是否值得重試。
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Unavailable, codes.DeadlineExceeded,
			codes.ResourceExhausted, codes.Aborted:
			return true
		}
		return false
	}
	// 非 gRPC 錯誤（如 dial 連線錯）保守視為可重試
	return true
}

// backoffFor 第 attempt 次（0-based）的退避時間。
// base 為 RetryBackoff；上限 5s；±25% jitter。
func backoffFor(attempt int, base time.Duration) time.Duration {
	if base <= 0 {
		base = 200 * time.Millisecond
	}
	// 指數退避：base * 2^attempt
	d := base << attempt
	const cap = 5 * time.Second
	if d > cap {
		d = cap
	}
	// jitter ±25%
	jitter := float64(d) * (rand.Float64()*0.5 - 0.25) //nolint:gosec
	return d + time.Duration(jitter)
}

// retryDo 執行 fn 最多 maxRetries+1 次；errReportable=true 時不重試（即使 isRetryable）。
// 回傳最後一次的結果。
//
// ctx 取消時立即返回。
func retryDo[T any](
	ctx context.Context,
	maxRetries int,
	base time.Duration,
	fn func(ctx context.Context, attempt int) (T, error),
) (T, error) {
	var (
		zero T
		last T
		err  error
	)
	if maxRetries < 0 {
		maxRetries = 0
	}
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		last, err = fn(ctx, attempt)
		if err == nil {
			return last, nil
		}
		if attempt == maxRetries || !isRetryable(err) {
			return last, err
		}
		// 退避；ctx 取消時提早離開
		wait := backoffFor(attempt, base)
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(wait):
		}
	}
	return last, err
}
