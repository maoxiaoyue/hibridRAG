USE mcp;

DROP INDEX IF EXISTS idx_long_term_memory_importance;
DROP INDEX IF EXISTS idx_long_term_memory_source;
DROP INDEX IF EXISTS idx_long_term_memory_category;
DROP INDEX IF EXISTS idx_long_term_memory_agent;
DROP INDEX IF EXISTS idx_long_term_memory_embedding;

DROP TABLE IF EXISTS long_term_memory;

-- 注意：不主動 DROP KEYSPACE mcp，避免影響 PostgreSQL 同名 db
-- 若需移除：DROP KEYSPACE IF EXISTS mcp;
