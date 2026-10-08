package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent/app/models"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmbedSingle 測試單個文本嵌入
func TestEmbedSingle(t *testing.T) {
	// 建立 mock TEI 服務
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/embed", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var req models.TEIEmbedRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, 1, len(req.Inputs))

		// 返回 mock 嵌入向量
		resp := models.TEIEmbedResponse{
			Embeddings: [][]float32{
				make([]float32, 1024), // 模擬 1024 維向量
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// 建立客戶端
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	cfg := TEIConfig{
		Endpoint:     server.URL,
		Model:        "BAAI/bge-m3",
		Dimension:    1024,
		BatchSize:    32,
		Timeout:      5 * time.Second,
		MaxRetries:   2,
		RetryBackoff: 100 * time.Millisecond,
		CacheEnabled: true,
		CacheTTL:     1 * time.Hour,
	}

	client, err := NewTEIClient(cfg, log)
	require.NoError(t, err)
	defer client.Close()

	// 測試嵌入
	result, err := client.Embed(context.Background(), "hello world")
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "hello world", result.Text)
	assert.Equal(t, 1024, len(result.Dense))

	// 驗證快取命中
	result2, err := client.Embed(context.Background(), "hello world")
	require.NoError(t, err)
	assert.True(t, result2.Success)
	assert.Equal(t, result.Dense, result2.Dense)
}

// TestEmbedBatch 測試批量嵌入
func TestEmbedBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req models.TEIEmbedRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)

		// 返回與輸入相同數量的向量
		embeddings := make([][]float32, len(req.Inputs))
		for i := range req.Inputs {
			embeddings[i] = make([]float32, 1024)
		}

		resp := models.TEIEmbedResponse{
			Embeddings: embeddings,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	log := logrus.New()
	cfg := TEIConfig{
		Endpoint:     server.URL,
		Model:        "BAAI/bge-m3",
		Dimension:    1024,
		BatchSize:    2, // 小 batch size 便於測試拆分
		Timeout:      5 * time.Second,
		MaxRetries:   1,
		RetryBackoff: 50 * time.Millisecond,
	}

	client, err := NewTEIClient(cfg, log)
	require.NoError(t, err)
	defer client.Close()

	// 批量嵌入 5 個文本（會被分成 3 個批次：2+2+1）
	req := &models.BatchEmbedRequest{
		Texts:   []string{"text1", "text2", "text3", "text4", "text5"},
		Timeout: 10 * time.Second,
		BatchID: "batch-001",
	}

	result, err := client.EmbedBatch(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 5, result.SuccessCount)
	assert.Equal(t, 0, result.FailureCount)
	assert.Equal(t, 5, len(result.Results))

	for i, res := range result.Results {
		assert.True(t, res.Success, "result %d should succeed", i)
		assert.Equal(t, 1024, len(res.Dense))
	}
}

// TestEmbedTimeout 測試超時處理
func TestEmbedTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 模擬耗時的服務
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models.TEIEmbedResponse{
			Embeddings: [][]float32{make([]float32, 1024)},
		})
	}))
	defer server.Close()

	log := logrus.New()
	cfg := TEIConfig{
		Endpoint:   server.URL,
		Dimension:  1024,
		Timeout:    500 * time.Millisecond, // 短超時
		MaxRetries: 0,
	}

	client, err := NewTEIClient(cfg, log)
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err = client.Embed(ctx, "hello")
	// 應該收到超時或其他錯誤
	assert.Error(t, err)
}

// TestEmbedRetry 測試重試機制
func TestEmbedRetry(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if callCount < 3 {
			// 前 2 次失敗
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		// 第 3 次成功
		resp := models.TEIEmbedResponse{
			Embeddings: [][]float32{make([]float32, 1024)},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	log := logrus.New()
	cfg := TEIConfig{
		Endpoint:     server.URL,
		Dimension:    1024,
		Timeout:      5 * time.Second,
		MaxRetries:   3,
		RetryBackoff: 50 * time.Millisecond,
	}

	client, err := NewTEIClient(cfg, log)
	require.NoError(t, err)
	defer client.Close()

	result, err := client.Embed(context.Background(), "test")
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, 3, callCount) // 應該重試了 2 次
}

// TestHealth 測試健康檢查
func TestHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/health", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	log := logrus.New()
	cfg := TEIConfig{
		Endpoint:  server.URL,
		Dimension: 1024,
	}

	client, err := NewTEIClient(cfg, log)
	require.NoError(t, err)
	defer client.Close()

	err = client.Health(context.Background())
	assert.NoError(t, err)
}

// TestMetrics 測試指標收集
func TestMetrics(t *testing.T) {
	log := logrus.New()
	cfg := TEIConfig{
		Endpoint:     "http://localhost:11434",
		Model:        "BAAI/bge-m3",
		Dimension:    1024,
		BatchSize:    32,
		CacheEnabled: true,
	}

	client, err := NewTEIClient(cfg, log)
	require.NoError(t, err)
	defer client.Close()

	metrics := client.Metrics()
	assert.Equal(t, "http://localhost:11434", metrics["endpoint"])
	assert.Equal(t, "BAAI/bge-m3", metrics["model"])
	assert.Equal(t, 1024, metrics["dimension"])
	assert.Equal(t, true, metrics["cache_enabled"])
}

// TestEmptyInput 測試空輸入
func TestEmptyInput(t *testing.T) {
	log := logrus.New()
	cfg := TEIConfig{
		Endpoint:  "http://localhost:11434",
		Dimension: 1024,
	}

	client, err := NewTEIClient(cfg, log)
	require.NoError(t, err)
	defer client.Close()

	// 空文本
	_, err = client.Embed(context.Background(), "")
	assert.Equal(t, ErrTEIEmptyInput, err)

	// 空批次
	_, err = client.EmbedBatch(context.Background(), &models.BatchEmbedRequest{})
	assert.Equal(t, ErrTEIEmptyInput, err)
}

// BenchmarkEmbed 性能測試
func BenchmarkEmbed(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.TEIEmbedResponse{
			Embeddings: [][]float32{make([]float32, 1024)},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.WarnLevel)

	cfg := TEIConfig{
		Endpoint:     server.URL,
		Dimension:    1024,
		Timeout:      5 * time.Second,
		CacheEnabled: false,
	}

	client, err := NewTEIClient(cfg, log)
	require.NoError(b, err)
	defer client.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Embed(context.Background(), "benchmark text")
		require.NoError(b, err)
	}
}
