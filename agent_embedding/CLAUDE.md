# agent_embedding — BGE-M3 Embedding + Hybrid Search

> 監聽埠：`:8502`（預設）
> 設定檔：`config/config.yaml`

---

## 功能概述

`agent_embedding` 負責 BGE-M3 dense embedding（Ollama/TEI 後端）+ Cassandra ANN 向量儲存 + PG 全文檢索，並做 RRF Hybrid Search 融合。`agents` 服務透過 `internal/agent/vectordb/http_client.go` 把 embed+store 操作委派給本服務。也是 Event Bus（Redis Stream）的發布端，upsert/search/delete 完成後會發事件供其他服務訂閱。

---

## API 端點（已驗證）

| Method | Path | 說明 |
|--------|------|------|
| `GET` | `/health` | 健康檢查 |
| `POST` | `/api/embed` | 單筆文字 embedding |
| `POST` | `/api/embed/batch` | 批次 embedding |
| `POST` | `/api/upsert` | 文字 chunk → embed + Cassandra 儲存 |
| `GET` | `/api/search` | dense-only 向量搜尋 |
| `GET` | `/api/search/hybrid` | dense ANN + sparse + RRF Fusion |
| `DELETE` | `/api/chunks` | 刪除 chunk |

---

## 依賴

- Ollama 或 TEI（BGE-M3 embedding 後端，`config.yaml` 的 `embedding.base_url`）
- Cassandra（dense 向量儲存，SAI ANN）
- Redis（embedding 結果快取 + Event Bus Stream）
- PostgreSQL（`postgres.enabled: true` 時做 FTS，支援 hybrid search 的 sparse 端；未啟用時 `/api/search/hybrid` 回 503）

**被呼叫方**：`agents/internal/agent/vectordb/http_client.go`（agents → agent_embedding，內部呼叫）

---

## 修正紀錄

已遷移至專案 [memory/20260703_log.md](../memory/20260703_log.md)（「二、全服務 HTTP/2 + 真 TLS」節）。
