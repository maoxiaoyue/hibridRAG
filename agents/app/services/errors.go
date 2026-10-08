package services

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// newID generates a short random ID of the form "<prefix>_<16 hex chars>".
func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

var (
	ErrNotFound  = errors.New("resource not found")
	ErrDuplicate = errors.New("resource already exists")

	// TEI 相關錯誤
	ErrTEIServiceUnavailable = errors.New("TEI service unavailable")
	ErrTEIInvalidResponse    = errors.New("TEI invalid response format")
	ErrTEITimeout            = errors.New("TEI request timeout")
	ErrTEIEmptyInput         = errors.New("TEI input is empty")
	ErrTEIDimensionMismatch  = errors.New("TEI dimension mismatch")

	// Embedding 相關錯誤
	ErrEmbeddingFailed       = errors.New("embedding generation failed")
	ErrEmbeddingRetryExhaust = errors.New("embedding retry exhausted")
)
