-- 004_drop_pg_chunks_sparse — 廢棄 PG chunks_sparse 表
--
-- Rationale:
--   架構決策「向量資料一律 Cassandra」（2026-05-08 確認）。BGE-M3 sparse
--   向量改寫 Cassandra `chunks_sparse(token_weights map<text,float>)`，由
--   agent_embedding 服務的 EnsureSchema 自動建立；PG 的 JSONB 版本廢棄。
--
--   原 003_create_rag_chunks.up.sql 仍會建這張 PG 表，未來 003 也會同步刪
--   掉相關 DDL；但既有環境已建表，先用 004 做明確 DROP，新環境會直接跳過
--   PG 那段（003 已修）。
--
--   chunks 表（FTS 用）保留：full-text search fallback 仍依賴它。

DROP INDEX IF EXISTS idx_chunks_sparse_token_weights;
DROP INDEX IF EXISTS idx_chunks_sparse_doc_id;
DROP INDEX IF EXISTS idx_chunks_sparse_agent_id;
DROP TABLE IF EXISTS chunks_sparse;
