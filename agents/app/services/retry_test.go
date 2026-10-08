package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, true},
		{"unavailable", status.Error(codes.Unavailable, "down"), true},
		{"resource", status.Error(codes.ResourceExhausted, ""), true},
		{"aborted", status.Error(codes.Aborted, ""), true},
		{"invalid-arg", status.Error(codes.InvalidArgument, ""), false},
		{"permission", status.Error(codes.PermissionDenied, ""), false},
		{"plain-err", errors.New("boom"), true}, // non-gRPC → conservative retry
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryable(tc.err); got != tc.want {
				t.Fatalf("isRetryable(%v)=%v want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRetryDo_SucceedAfterTransient(t *testing.T) {
	calls := 0
	got, err := retryDo(context.Background(), 3, time.Millisecond,
		func(_ context.Context, _ int) (int, error) {
			calls++
			if calls < 3 {
				return 0, status.Error(codes.Unavailable, "later")
			}
			return 42, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 || calls != 3 {
		t.Fatalf("got=%d calls=%d", got, calls)
	}
}

func TestRetryDo_NotRetryablePermanent(t *testing.T) {
	calls := 0
	_, err := retryDo(context.Background(), 5, time.Millisecond,
		func(_ context.Context, _ int) (int, error) {
			calls++
			return 0, status.Error(codes.InvalidArgument, "bad")
		})
	if err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 call, got %d", calls)
	}
}

func TestRetryDo_ExhaustReturnsLast(t *testing.T) {
	calls := 0
	_, err := retryDo(context.Background(), 2, time.Millisecond,
		func(_ context.Context, _ int) (int, error) {
			calls++
			return 0, status.Error(codes.Unavailable, "down")
		})
	if err == nil {
		t.Fatal("want error")
	}
	if calls != 3 { // maxRetries=2 → 1+2 = 3 attempts
		t.Fatalf("want 3 attempts, got %d", calls)
	}
}

func TestRetryDo_CtxCancelStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(5 * time.Millisecond); cancel() }()
	calls := 0
	_, err := retryDo(ctx, 100, 50*time.Millisecond,
		func(_ context.Context, _ int) (int, error) {
			calls++
			return 0, status.Error(codes.Unavailable, "down")
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if calls < 1 || calls > 5 {
		t.Fatalf("unexpected calls=%d", calls)
	}
}
