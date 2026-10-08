-- ============================================================================
-- Cassandra 5.0 — agent keyspace + dense 向量表（BGE-M3 1024 維）
-- 用法：cqlsh -f 001_create_keyspace.cql
-- ============================================================================
-- ---------------------------------------------------------------------------
-- chunks_dense：BGE-M3 dense 向量 (float32 × 1024)
-- 以 chunk_id 為 PK；SAI index 支援 ANN 查詢
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS chunks_dense (
    chunk_id   text PRIMARY KEY,
    agent_id   text,
    doc_id     text,
    model      text,
    embedding  vector<float, 1024>,
    created_at timestamp
);

-- SAI ANN index（Cassandra 5.0+）
CREATE CUSTOM INDEX IF NOT EXISTS idx_chunks_dense_embedding
    ON chunks_dense(embedding)
    USING 'sai'
    WITH OPTIONS = {'similarity_function': 'COSINE'};

CREATE CUSTOM INDEX IF NOT EXISTS idx_chunks_dense_agent
    ON chunks_dense(agent_id)
    USING 'sai';

CREATE CUSTOM INDEX IF NOT EXISTS idx_chunks_dense_doc
    ON chunks_dense(doc_id)
    USING 'sai';
