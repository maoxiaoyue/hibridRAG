# TEI Client 集成指南

## 概述

TEI Client 是調用 BGE-M3 Text Embeddings Inference 服務的 HTTP 客戶端，支援：
- ✅ 單個文本嵌入
- ✅ 批量嵌入（自動拆分 batch）
- ✅ 自動重試機制
- ✅ 嵌入結果快取
- ✅ 上下文超時控制
- ✅ 健康檢查

---

## 快速開始

### 1️⃣ 初始化客戶端

```go
import (
    "agent/app/services"
    "github.com/sirupsen/logrus"
)

log := logrus.New()

cfg := services.TEIConfig{
    Endpoint:        "http://192.168.50.201:11434",  // BGE-M3 TEI 服務
    Model:           "BAAI/bge-m3",
    Dimension:       1024,
    BatchSize:       32,
    Timeout:         30 * time.Second,
    MaxRetries:      3,
    RetryBackoff:    500 * time.Millisecond,
    CacheEnabled:    true,
    CacheTTL:        24 * time.Hour,
}

client, err := services.NewTEIClient(cfg, log)
if err != nil {
    panic(err)
}
defer client.Close()
```

### 2️⃣ 單個文本嵌入

```go
result, err := client.Embed(ctx, "Hello, world!")
if err != nil {
    panic(err)
}

if result.Success {
    fmt.Printf("Dense vector [1024]: %v\n", result.Dense[:5])
}
```

### 3️⃣ 批量嵌入

```go
req := &models.BatchEmbedRequest{
    Texts:   []string{"text1", "text2", "text3"},
    Timeout: 10 * time.Second,
    Retries: 3,
    BatchID: "batch-001",
}

result, err := client.EmbedBatch(ctx, req)
if err != nil {
    panic(err)
}

fmt.Printf("Success: %d, Failure: %d\n", result.SuccessCount, result.FailureCount)
for _, res := range result.Results {
    if res.Success {
        fmt.Printf("%s -> [1024 dims]\n", res.Text)
    }
}
```

### 4️⃣ 健康檢查

```go
err := client.Health(ctx)
if err != nil {
    fmt.Printf("TEI service unavailable: %v\n", err)
}
```

---

## 錯誤處理

### 常見錯誤

| 錯誤 | 原因 | 解決方案 |
|------|------|--------|
| `ErrTEIServiceUnavailable` | TEI 服務無法連線 | 檢查 `endpoint` 配置和網路 |
| `ErrTEITimeout` | 請求超時 | 增加 `Timeout` 或 `BatchSize` |
| `ErrTEIEmptyInput` | 輸入文本為空 | 驗證輸入資料 |
| `ErrEmbeddingRetryExhaust` | 重試次數已用盡 | 檢查 TEI 服務狀態 |
| `ErrTEIDimensionMismatch` | 向量維度不符 | 檢查 `Dimension` 配置（應為 1024） |

### 錯誤處理模式

```go
result, err := client.Embed(ctx, text)

switch err {
case services.ErrTEITimeout:
    // 處理超時
    log.Error("TEI 請求超時，請增加 Timeout")

case services.ErrTEIServiceUnavailable:
    // 服務不可用，可能需要 fallback
    log.Error("TEI 服務無法連線")

case services.ErrEmbeddingRetryExhaust:
    // 重試已用盡，放棄
    log.Error("嵌入生成失敗，放棄")

default:
    if err != nil {
        log.Errorf("未知錯誤: %v", err)
    }
}
```

---

## 性能優化

### 1. 快取配置

```go
cfg := services.TEIConfig{
    CacheEnabled: true,      // 啟用快取
    CacheTTL:    24 * time.Hour,  // 快取 24 小時
}
```

**快取工作原理**：
- 首次嵌入時調用 TEI 服務
- 結果存入內存快取
- 同一文本的後續請求直接返回快取
- 快取自動過期清理（每 5 分鐘掃一次）

### 2. Batch Size 調整

```go
cfg := services.TEIConfig{
    BatchSize: 64,  // 每個 HTTP 請求嵌入 64 個文本
}
```

**影響**：
- ✅ 增大 batch: 吞吐量 ↑，延遲 ↑
- ❌ 減小 batch: 吞吐量 ↓，延遲 ↓
- 建議：32-64 為平衡點

### 3. 超時設定

```go
cfg := services.TEIConfig{
    Timeout:      30 * time.Second,  // 單次請求超時
    MaxRetries:   3,
    RetryBackoff: 500 * time.Millisecond,
}

// 或按需覆蓋
req := &models.BatchEmbedRequest{
    Texts:   texts,
    Timeout: 60 * time.Second,  // 本次請求用更長超時
}
```

### 4. 並發控制

```go
// 使用 semaphore 限制並發
sem := make(chan struct{}, 10)  // 最多 10 個並發請求

for _, text := range texts {
    sem <- struct{}{}
    go func(t string) {
        defer func() { <-sem }()
        result, _ := client.Embed(ctx, t)
        // 處理 result
    }(text)
}
```

---

## 集成到 Embedding Service

### 結構設計

```go
// internal/services/embedding_service.go
type EmbeddingService struct {
    teiClient  *services.TEIClient
    db         *bun.DB
    cassandra  *gocql.Session
}

func (s *EmbeddingService) EmbedAndStore(ctx context.Context, text string) error {
    // 1. 調用 TEI
    result, err := s.teiClient.Embed(ctx, text)
    if err != nil {
        return err
    }

    // 2. 存入 Cassandra (Dense)
    chunkID := generateID()
    if err := s.storeInCassandra(ctx, chunkID, result.Dense); err != nil {
        return err
    }

    // 3. 存入 PostgreSQL (Sparse + Metadata)
    // ...

    return nil
}
```

### 與 Ingest Pipeline 整合

```go
// 在 IngestService 中使用
type IngestService struct {
    db         *bun.DB
    embedding  *EmbeddingService  // 注入 EmbeddingService
}

func (s *IngestService) ProcessChunk(ctx context.Context, chunk string) error {
    return s.embedding.EmbedAndStore(ctx, chunk)
}
```

---

## 監控和指標

### 收集指標

```go
metrics := client.Metrics()
// 輸出：
// {
//   "endpoint": "http://192.168.50.201:11434",
//   "model": "BAAI/bge-m3",
//   "dimension": 1024,
//   "cache_size": 1024,
//   "cache_enabled": true
// }
```

### 集成 Prometheus

```go
// 定期報告指標
ticker := time.NewTicker(1 * time.Minute)
for range ticker.C {
    metrics := client.Metrics()
    promCacheSize.Set(float64(metrics["cache_size"].(int)))
}
```

---

## 測試

### 運行測試

```bash
# 運行所有測試
go test -v ./app/services -run TestEmbed

# 性能測試
go test -bench=BenchmarkEmbed ./app/services

# 覆蓋率
go test -cover ./app/services
```

### Mock 服務

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    resp := models.TEIEmbedResponse{
        Embeddings: [][]float32{make([]float32, 1024)},
    }
    json.NewEncoder(w).Encode(resp)
}))
defer server.Close()

cfg := services.TEIConfig{
    Endpoint:  server.URL,
    Dimension: 1024,
}
client, _ := services.NewTEIClient(cfg, nil)
```

---

## 故障排除

### 問題 1：連線失敗

```
error: TEI service unavailable
```

**檢查**：
```bash
# 測試 TEI 服務是否運行
curl http://192.168.50.201:11434/health

# 檢查防火牆
telnet 192.168.50.201 11434
```

### 問題 2：維度不符

```
error: TEI dimension mismatch: expected 1024, got 768
```

**原因**：TEI 返回的向量維度與配置不符

**解決**：
- 確認 BGE-M3 模型（應輸出 1024 維）
- 檢查 `Dimension` 配置

### 問題 3：超時頻繁

```
error: TEI request timeout
```

**優化**：
```go
cfg.Timeout = 60 * time.Second        // 增加超時
cfg.BatchSize = 16                    // 減小 batch（更快）
cfg.MaxRetries = 5                    // 增加重試
```

---

## 配置範例

### 開發環境

```yaml
embedding:
  provider: "tei"
  tei_endpoint: "http://localhost:11434"
  model: "BAAI/bge-m3"
  dimension: 1024
  batch_size: 32
  timeout: 30s
  max_retries: 3
  cache_enabled: false  # 開發時不快取
```

### 生產環境

```yaml
embedding:
  provider: "tei"
  tei_endpoint: "http://192.168.50.201:11434"
  model: "BAAI/bge-m3"
  dimension: 1024
  batch_size: 64        # 更大 batch
  timeout: 60s          # 更長超時
  max_retries: 5        # 更多重試
  cache_enabled: true
  cache_ttl: 24h
```

---

## 下一步

1. ✅ **TEI Client 實作** (完成)
2. ⬜ **Embedding Service** - 整合 chunking + TEI + 存儲
3. ⬜ **Hybrid Search** - Dense + Sparse + RRF
4. ⬜ **Context Assembly** - 組裝最終 prompt
