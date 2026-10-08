package services

import (
	"context"
	"time"
)

// contextWithSeconds 統一的 second-level timeout wrapper。
func contextWithSeconds(parent context.Context, sec int) (context.Context, context.CancelFunc) {
	if sec <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, time.Duration(sec)*time.Second)
}
