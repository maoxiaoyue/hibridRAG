-- ============================================================================
-- Cassandra 5.0 — Agent Vector Tables (Rollback)
-- 用法：cqlsh -f 002_create_vector_tables.down.cql
-- ============================================================================

USE agent;

-- Drop all vector-related tables in reverse dependency order
DROP TABLE IF EXISTS vector_anomalies;
DROP TABLE IF EXISTS vector_embeddings_batch;
DROP TABLE IF EXISTS vector_clusters;
DROP TABLE IF EXISTS vector_similarity_cache;
DROP TABLE IF EXISTS vector_search_history;
DROP TABLE IF EXISTS vector_metadata;
