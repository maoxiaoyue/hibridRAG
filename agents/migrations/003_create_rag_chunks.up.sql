-- ============================================================================
-- 003_create_rag_chunks.up.sql
-- RAG chunks：文件 + 分塊 + BGE-M3 sparse 權重（④ 存儲層 PG 側）
--
-- dense 向量寫 Cassandra 5.0 SAI（另見 migrations/cassandra/*.cql）
-- PG 這邊保存：
--   - documents：原始來源檔案 metadata
--   - chunks：分塊文字與對應 Cassandra chunk_id（便於 PG 查詢原文）
--   - chunks_sparse：BGE-M3 sparse 權重 (token_id → weight)
-- ============================================================================

-- ---------------------------------------------------------------------------
-- documents：攝取來源（SKILL.md / references/*.md / 其他）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS documents (
    id         TEXT PRIMARY KEY,            -- doc_id（UUID 或 hash）
    agent_id   TEXT NOT NULL,
    job_id     TEXT,                        -- 對應 ingest_jobs.id（可為 NULL）
    kind       TEXT NOT NULL,               -- skill / reference / procedural
    source     TEXT NOT NULL,               -- 原始路徑或 URL
    mime       TEXT,
    title      TEXT,
    tags       TEXT[],
    metadata   JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_documents_agent ON documents(agent_id);
CREATE INDEX IF NOT EXISTS idx_documents_kind ON documents(agent_id, kind);
CREATE INDEX IF NOT EXISTS idx_documents_job ON documents(job_id);
CREATE INDEX IF NOT EXISTS idx_documents_tags_gin ON documents USING gin(tags);

CREATE TRIGGER trg_documents_touch BEFORE UPDATE ON documents
    FOR EACH ROW EXECUTE PROCEDURE agent_touch_updated_at();

-- ---------------------------------------------------------------------------
-- chunks：分塊原文 + metadata
--   - chunk_id 同步給 Cassandra dense 向量表使用
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS chunks (
    id          TEXT PRIMARY KEY,           -- chunk_id
    doc_id      TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    agent_id    TEXT NOT NULL,
    idx         INTEGER NOT NULL,           -- 同一 doc 內的序號（0-based）
    strategy    TEXT NOT NULL,              -- markdown / json / plain / llm
    text        TEXT NOT NULL,
    char_count  INTEGER NOT NULL,
    token_count INTEGER,
    metadata    JSONB,                      -- heading / section / lang / ...
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_chunks_agent ON chunks(agent_id);
CREATE INDEX IF NOT EXISTS idx_chunks_doc ON chunks(doc_id, idx);
-- 供 PG 端全文備援（非 BGE-M3 sparse，但在 sparse 服務未就緒時可頂替）
CREATE INDEX IF NOT EXISTS idx_chunks_text_trgm ON chunks USING gin(text gin_trgm_ops);

-- 需要 pg_trgm extension（若未啟用則上面那行會失敗 → 請先 CREATE EXTENSION）
-- 預設下面這行不啟用，讓 DBA 自行決定；若要用全文備援則取消註解：
-- CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ---------------------------------------------------------------------------
-- chunks_sparse：BGE-M3 SPLADE / sparse 權重
--   token_weights: {"<token_id>": <weight>, ...}
--   用 JSONB 而非 HSTORE，方便存 float32 並與 Go 端對齊
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS chunks_sparse (
    chunk_id      TEXT PRIMARY KEY REFERENCES chunks(id) ON DELETE CASCADE,
    agent_id      TEXT NOT NULL,
    doc_id        TEXT NOT NULL,
    token_weights JSONB NOT NULL,
    model         TEXT NOT NULL DEFAULT 'bge-m3',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_chunks_sparse_agent ON chunks_sparse(agent_id);
CREATE INDEX IF NOT EXISTS idx_chunks_sparse_doc ON chunks_sparse(doc_id);
CREATE INDEX IF NOT EXISTS idx_chunks_sparse_tokens_gin ON chunks_sparse USING gin(token_weights);
