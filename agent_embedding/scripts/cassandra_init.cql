-- agent_embedding — Cassandra 5.0 schema for dense vector storage
--
-- 啟動時若 cassandra.auto_init=true，服務會自動執行等效的 DDL；
-- 此檔案保留供手動初始化或審查。
--
-- 前置：keyspace 必須先存在
-- CREATE KEYSPACE IF NOT EXISTS mcp
--   WITH REPLICATION = { 'class': 'SimpleStrategy', 'replication_factor': 1 };

USE mcp;

-- chunks_dense: 儲存 chunk 文字 + BGE-M3 1024 維 dense 向量
CREATE TABLE IF NOT EXISTS chunks_dense (
    chunk_id   text PRIMARY KEY,
    agent_id   text,
    doc_id     text,
    text       text,
    model      text,
    embedding  vector<float, 1024>,
    created_at timestamp
);

-- SAI index：依 agent_id 篩選
CREATE CUSTOM INDEX IF NOT EXISTS chunks_dense_agent_idx
    ON chunks_dense(agent_id) USING 'StorageAttachedIndex';

-- SAI index：依 doc_id 篩選 / 刪除
CREATE CUSTOM INDEX IF NOT EXISTS chunks_dense_doc_idx
    ON chunks_dense(doc_id) USING 'StorageAttachedIndex';

-- Vector ANN index：cosine similarity
CREATE CUSTOM INDEX IF NOT EXISTS chunks_dense_vec_idx
    ON chunks_dense(embedding) USING 'StorageAttachedIndex'
    WITH OPTIONS = {'similarity_function': 'cosine'};

-- Sparse 向量在 2026-05-08 決議放棄：Ollama BGE-M3 backend 不出 sparse，
-- 改走 dense-only ANN。先前曾規劃的 chunks_sparse 表不再建立。
