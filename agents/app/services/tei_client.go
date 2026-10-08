package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"agent/app/models"
	"github.com/sirupsen/logrus"
)

// TEIConfig — TEI 客戶端組態
type TEIConfig struct {
	Endpoint        string        // http://host:port
	Model           string        // "BAAI/bge-m3"
	Dimension       int           // 1024
	BatchSize       int           // 批次大小 (32)
	Timeout         time.Duration // 單次請求超時 (30s)
	MaxRetries      int           // 最大重試次數 (3)
	RetryBackoff    time.Duration // 重試退避時間 (500ms)
	CacheEnabled    bool          // 是否快取嵌入結果
	CacheTTL        time.Duration // 快取時長
}

// TEIClient — BGE-M3 TEI HTTP 客戶端
type TEIClient struct {
	cfg       TEIConfig
	httpCli   *http.Client
	log       *logrus.Logger
	embedMu   sync.RWMutex // 保護並發存取
	cache     map[string]*cachedEmbedding
	cacheMu   sync.RWMutex
	closeOnce sync.Once
	done      chan struct{}
}

// cachedEmbedding — 快取的嵌入向量
type cachedEmbedding struct {
	dense  []float32
	expiry time.Time
}

// NewTEIClient 建立 TEI 客戶端
func NewTEIClient(cfg TEIConfig, log *logrus.Logger) (*TEIClient, error) {
	// 驗證配置
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("TEI endpoint cannot be empty")
	}
	if cfg.Dimension <= 0 {
		cfg.Dimension = 1024
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 32
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 3
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 500 * time.Millisecond
	}

	if log == nil {
		log = logrus.New()
	}

	// 建立 HTTP 客戶端（自訂超時）
	httpCli := &http.Client{
		Timeout: cfg.Timeout,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
			MaxConnsPerHost:     10,
			IdleConnTimeout:     30 * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}

	client := &TEIClient{
		cfg:     cfg,
		httpCli: httpCli,
		log:     log,
		cache:   make(map[string]*cachedEmbedding),
		done:    make(chan struct{}),
	}

	// 若啟用快取，啟動快取清理 goroutine
	if cfg.CacheEnabled && cfg.CacheTTL > 0 {
		go client.cleanupCache()
	}

	return client, nil
}

// Embed 單個文本嵌入（帶重試）
func (c *TEIClient) Embed(ctx context.Context, text string) (*models.EmbedResult, error) {
	if text == "" {
		return nil, ErrTEIEmptyInput
	}

	// 檢查快取
	if c.cfg.CacheEnabled {
		if cached := c.getFromCache(text); cached != nil {
			c.log.Debugf("cache hit for text: %s", truncate(text, 50))
			return &models.EmbedResult{
				Text:    text,
				Dense:   cached,
				Success: true,
			}, nil
		}
	}

	// 帶重試的嵌入
	result := &models.EmbedResult{Text: text}
	var lastErr error

	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			// 退避重試
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.cfg.RetryBackoff * time.Duration(attempt)):
			}
			c.log.Warnf("TEI retry attempt %d/%d for text: %s", attempt, c.cfg.MaxRetries, truncate(text, 50))
		}

		dense, err := c.embedWithTimeout(ctx, []string{text})
		if err == nil && len(dense) > 0 {
			result.Dense = dense[0]
			result.Success = true

			// 寫入快取
			if c.cfg.CacheEnabled {
				c.saveToCache(text, dense[0])
			}
			return result, nil
		}

		lastErr = err
	}

	result.Success = false
	result.Error = fmt.Sprintf("embedding failed after %d retries: %v", c.cfg.MaxRetries+1, lastErr)
	c.log.Errorf("TEI embed failed: %s", result.Error)
	return result, ErrEmbeddingRetryExhaust
}

// EmbedBatch 批量嵌入文本
func (c *TEIClient) EmbedBatch(ctx context.Context, req *models.BatchEmbedRequest) (*models.BatchEmbedResult, error) {
	if len(req.Texts) == 0 {
		return nil, ErrTEIEmptyInput
	}

	startTime := time.Now()
	result := &models.BatchEmbedResult{
		BatchID: req.BatchID,
		Results: make([]*models.EmbedResult, len(req.Texts)),
	}

	// 若未指定超時，使用預設
	if req.Timeout <= 0 {
		req.Timeout = c.cfg.Timeout
	}

	// 建立帶超時的 context
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	// 按 batch size 分割
	for batchStart := 0; batchStart < len(req.Texts); batchStart += c.cfg.BatchSize {
		batchEnd := batchStart + c.cfg.BatchSize
		if batchEnd > len(req.Texts) {
			batchEnd = len(req.Texts)
		}

		batch := req.Texts[batchStart:batchEnd]

		// 帶重試的批次嵌入
		maxRetries := c.cfg.MaxRetries
		if req.Retries > 0 {
			maxRetries = req.Retries
		}

		var dense [][]float32
		var err error

		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					result.Error = ctx.Err().Error()
					result.Duration = time.Since(startTime)
					return result, ctx.Err()
				case <-time.After(c.cfg.RetryBackoff * time.Duration(attempt)):
				}
				c.log.Warnf("TEI batch retry attempt %d/%d (batch %d-%d)", attempt, maxRetries, batchStart, batchEnd)
			}

			dense, err = c.embedWithTimeout(ctx, batch)
			if err == nil {
				break
			}
		}

		if err != nil {
			// 批次失敗，標記所有項為失敗
			for i, text := range batch {
				result.Results[batchStart+i] = &models.EmbedResult{
					Text:    text,
					Success: false,
					Error:   err.Error(),
				}
				result.FailureCount++
			}
			result.Error = fmt.Sprintf("batch %d-%d failed: %v", batchStart, batchEnd, err)
			continue
		}

		// 批次成功，保存結果
		for i, vector := range dense {
			if vector != nil {
				result.Results[batchStart+i] = &models.EmbedResult{
					Text:    batch[i],
					Dense:   vector,
					Success: true,
				}
				result.SuccessCount++

				// 寫入快取
				if c.cfg.CacheEnabled {
					c.saveToCache(batch[i], vector)
				}
			} else {
				result.Results[batchStart+i] = &models.EmbedResult{
					Text:    batch[i],
					Success: false,
					Error:   "nil vector returned",
				}
				result.FailureCount++
			}
		}
	}

	result.Duration = time.Since(startTime)
	c.log.Infof("batch embed completed: %d success, %d failed, duration: %v",
		result.SuccessCount, result.FailureCount, result.Duration)

	return result, nil
}

// embedWithTimeout 內部方法：實際呼叫 TEI /embed 端點
func (c *TEIClient) embedWithTimeout(ctx context.Context, texts []string) ([][]float32, error) {
	c.embedMu.Lock()
	defer c.embedMu.Unlock()

	// 構建請求
	reqBody := models.TEIEmbedRequest{Inputs: texts}
	reqData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// 發送請求
	req, err := http.NewRequestWithContext(ctx, "POST", c.cfg.Endpoint+"/embed", bytes.NewBuffer(reqData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpCli.Do(req)
	if err != nil {
		// 區分超時和其他錯誤
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrTEITimeout
		}
		if _, ok := err.(net.Error); ok {
			return nil, fmt.Errorf("network error: %w", err)
		}
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// 檢查 HTTP 狀態碼
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("TEI returned %d: %s", resp.StatusCode, string(body))
	}

	// 解析回應
	var respBody models.TEIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return nil, fmt.Errorf("decode response: %w", ErrTEIInvalidResponse)
	}

	// 驗證維度
	if len(respBody.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embedding count mismatch: expected %d, got %d", len(texts), len(respBody.Embeddings))
	}

	for _, emb := range respBody.Embeddings {
		if len(emb) != c.cfg.Dimension {
			return nil, fmt.Errorf("embedding dimension mismatch: expected %d, got %d", c.cfg.Dimension, len(emb))
		}
	}

	return respBody.Embeddings, nil
}

// --- 快取方法 ---

func (c *TEIClient) getFromCache(text string) []float32 {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()

	if cached, ok := c.cache[text]; ok {
		// 檢查是否已過期
		if time.Now().Before(cached.expiry) {
			return cached.dense
		}
	}
	return nil
}

func (c *TEIClient) saveToCache(text string, dense []float32) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	c.cache[text] = &cachedEmbedding{
		dense:  dense,
		expiry: time.Now().Add(c.cfg.CacheTTL),
	}
}

// cleanupCache 定期清理過期快取（背景執行）
func (c *TEIClient) cleanupCache() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			c.cacheMu.Lock()
			now := time.Now()
			for key, cached := range c.cache {
				if now.After(cached.expiry) {
					delete(c.cache, key)
				}
			}
			c.cacheMu.Unlock()
			c.log.Debugf("cache cleanup: %d entries", len(c.cache))
		}
	}
}

// Health 檢查 TEI 服務健康狀態
func (c *TEIClient) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.cfg.Endpoint+"/health", nil)
	if err != nil {
		return fmt.Errorf("create health check request: %w", err)
	}

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", ErrTEIServiceUnavailable)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TEI health check returned %d", resp.StatusCode)
	}

	return nil
}

// Close 關閉客戶端
func (c *TEIClient) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.httpCli != nil && c.httpCli.Transport != nil {
			c.httpCli.CloseIdleConnections()
		}
	})
	return nil
}

// Metrics 返回客戶端統計資訊
func (c *TEIClient) Metrics() map[string]interface{} {
	c.cacheMu.RLock()
	cacheSize := len(c.cache)
	c.cacheMu.RUnlock()

	return map[string]interface{}{
		"endpoint":     c.cfg.Endpoint,
		"model":        c.cfg.Model,
		"dimension":    c.cfg.Dimension,
		"batch_size":   c.cfg.BatchSize,
		"cache_size":   cacheSize,
		"cache_enabled": c.cfg.CacheEnabled,
	}
}

// 工具函數

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
