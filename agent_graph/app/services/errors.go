package services

import "errors"

var (
	ErrNotFound = errors.New("not found")
	// ErrGraphDisabled 圖譜後端（Kùzu）未就緒時，圖譜相關操作一律回這個錯誤。
	ErrGraphDisabled = errors.New("graph store disabled")
	// ErrVectorDisabled 向量索引（Kùzu HNSW）未就緒時，向量相關操作一律回這個錯誤。
	ErrVectorDisabled = errors.New("vector store disabled")
	// ErrAlreadyReviewed 待審關係已被核准/拒絕過，不可重複審核。
	ErrAlreadyReviewed = errors.New("proposed relation already reviewed")
)
