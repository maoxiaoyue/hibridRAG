-- ============================================================================
-- 001_create_long_term_memory.up.cql
-- 長期記憶（Long-Term Memory）向量表
--
-- 用途：跨 session 持久化的語意記憶片段，可被 ANN 相似度搜尋
-- 來源：可由 EpisodicMemory 整理而來、外部知識攝入或使用者指定保存
-- 嵌入：BGE-M3 dense（float32 × 1024）
-- ============================================================================

-- Keyspace（如已存在則略過）
CREATE KEYSPACE IF NOT EXISTS mcp
    WITH REPLICATION = {'class': 'SimpleStrategy', 'replication_factor': 1};

USE mcp;

-- ---------------------------------------------------------------------------
-- long_term_memory：長期記憶主表
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS long_term_memory (
    memory_id      text PRIMARY KEY,
    agent_id       text,
    category       text,                  -- knowledge / experience / concept / fact / preference
    source         text,                  -- 來源：episodic_id / ingest_id / manual
    content        text,                  -- 原始記憶內容
    summary        text,                  -- 壓縮後摘要（給 LLM context 用）
    metadata       map<text, text>,       -- 自由欄位（tags, related_entity, etc.）
    embedding      vector<float, 1024>,   -- BGE-M3 dense
    importance     float,                 -- 0.0 ~ 1.0，重要度評分
    created_at     timestamp,
    last_accessed  timestamp,
    access_count   int
);

-- ---------------------------------------------------------------------------
-- SAI ANN 索引（Cassandra 5.0+，COSINE 相似度）
-- ---------------------------------------------------------------------------
CREATE CUSTOM INDEX IF NOT EXISTS idx_long_term_memory_embedding
    ON long_term_memory(embedding)
    USING 'sai'
    WITH OPTIONS = {'similarity_function': 'COSINE'};

-- ---------------------------------------------------------------------------
-- 二級 SAI 索引（agent_id / category / source 過濾）
-- ---------------------------------------------------------------------------
CREATE CUSTOM INDEX IF NOT EXISTS idx_long_term_memory_agent
    ON long_term_memory(agent_id)
    USING 'sai';

CREATE CUSTOM INDEX IF NOT EXISTS idx_long_term_memory_category
    ON long_term_memory(category)
    USING 'sai';

CREATE CUSTOM INDEX IF NOT EXISTS idx_long_term_memory_source
    ON long_term_memory(source)
    USING 'sai';

CREATE CUSTOM INDEX IF NOT EXISTS idx_long_term_memory_importance
    ON long_term_memory(importance)
    USING 'sai';
