-- 004_drop_pg_chunks_sparse (down) — 還原 PG chunks_sparse 表
-- 僅供 rollback；不建議再次使用此表。token_weights 改用 Cassandra map。

CREATE TABLE IF NOT EXISTS chunks_sparse (
    chunk_id      TEXT PRIMARY KEY,
    agent_id      TEXT NOT NULL,
    doc_id        TEXT NOT NULL,
    token_weights JSONB NOT NULL,
    model         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_chunks_sparse_agent_id
    ON chunks_sparse(agent_id);
CREATE INDEX IF NOT EXISTS idx_chunks_sparse_doc_id
    ON chunks_sparse(doc_id);
CREATE INDEX IF NOT EXISTS idx_chunks_sparse_token_weights
    ON chunks_sparse USING GIN (token_weights);
