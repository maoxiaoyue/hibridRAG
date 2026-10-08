USE agent;
DROP INDEX IF EXISTS idx_chunks_dense_doc;
DROP INDEX IF EXISTS idx_chunks_dense_agent;
DROP INDEX IF EXISTS idx_chunks_dense_embedding;
DROP TABLE IF EXISTS chunks_dense;
DROP KEYSPACE IF EXISTS agent;
