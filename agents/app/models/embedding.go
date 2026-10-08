package models

import "time"

// TEIEmbedRequest — TEI /embed 端點的請求
type TEIEmbedRequest struct {
	Inputs []string `json:"inputs"`
}

// TEIEmbedResponse — TEI /embed 端點的回應（Dense + Sparse）
type TEIEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Tokens     []int       `json:"tokens,omitempty"` // 詞元統計（可選）
}

// DenseEmbedding — Dense 向量儲存結構（BGE-M3 1024 維）
type DenseEmbedding struct {
	ChunkID   string
	Vector    []float32 // 1024 維
	CreatedAt time.Time
}

// SparseEmbedding — Sparse 向量儲存結構（Token ID -> Weight）
type SparseEmbedding struct {
	ChunkID   string
	Indices   []int32   // Token ID
	Values    []float32 // Weight
	CreatedAt time.Time
}

// EmbedResult — 單次嵌入結果
type EmbedResult struct {
	Text    string
	Dense   []float32           // Dense vector [1024]
	Sparse  map[int32]float32   // Sparse {token_id: weight}
	Success bool
	Error   string
}

// BatchEmbedRequest — 批次嵌入請求
type BatchEmbedRequest struct {
	Texts    []string
	Timeout  time.Duration // 本次請求超時
	Retries  int           // 重試次數
	BatchID  string        // 用於追蹤
}

// BatchEmbedResult — 批次嵌入結果
type BatchEmbedResult struct {
	BatchID  string
	Results  []*EmbedResult
	Duration time.Duration
	Error    string
	// 統計
	SuccessCount int
	FailureCount int
}
