-- ============================================================================
-- Cassandra 5.0 — Agent Vector Tables (BGE-M3 1024 維)
-- 用法：cqlsh -f 002_create_vector_tables.cql
-- ============================================================================

USE mcp;

-- ---------------------------------------------------------------------------
-- vector_metadata：向量元數據表
-- 儲存向量相關的標籤、分類、來源等資訊
-- PK: chunk_id; 可按 agent_id 或 category 查詢
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS vector_metadata (
    chunk_id   text PRIMARY KEY,
    agent_id   text,
    category   text,
    source     text,
    tags       set<text>,
    title      text,
    description text,
    metadata_json text,
    created_at timestamp
);

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_metadata_agent
    ON vector_metadata(agent_id)
    USING 'sai';

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_metadata_category
    ON vector_metadata(category)
    USING 'sai';


-- ---------------------------------------------------------------------------
-- vector_search_history：向量搜索歷史表
-- 記錄每次向量搜索查詢、結果、耗時等
-- PK: (agent_id, search_id); 支援按時間範圍查詢
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS vector_search_history (
    agent_id   text,
    search_id  text,
    user_id    text,
    query_text text,
    query_embedding vector<float, 1024>,
    top_k      int,
    similarity_threshold float,
    result_count int,
    top_result_ids list<text>,
    top_result_scores list<float>,
    duration_ms int,
    created_at timestamp,
    PRIMARY KEY ((agent_id), search_id)
) WITH CLUSTERING ORDER BY (search_id DESC);

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_search_history_user
    ON vector_search_history(user_id)
    USING 'sai';

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_search_history_created
    ON vector_search_history(created_at)
    USING 'sai';


-- ---------------------------------------------------------------------------
-- vector_similarity_cache：相似度評分緩存表
-- 快取已計算的向量對相似度，避免重複計算
-- PK: (chunk_id_a, chunk_id_b); 或反向查詢用 (chunk_id_b, chunk_id_a)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS vector_similarity_cache (
    chunk_id_a text,
    chunk_id_b text,
    similarity_score float,
    similarity_metric text,  -- 'cosine', 'euclidean', 'dot'
    computed_at timestamp,
    expires_at timestamp,
    PRIMARY KEY ((chunk_id_a), chunk_id_b)
);

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_similarity_cache_score
    ON vector_similarity_cache(similarity_score)
    USING 'sai';


-- ---------------------------------------------------------------------------
-- vector_clusters：向量聚類表
-- 儲存聚類中心點及其相關向量
-- PK: (agent_id, cluster_id); 支援層級化聚類
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS vector_clusters (
    agent_id      text,
    cluster_id    text,
    parent_cluster_id text,
    cluster_center vector<float, 1024>,
    cluster_label text,
    chunk_count   int,
    member_ids    list<text>,
    inertia       float,
    radius        float,
    created_at    timestamp,
    updated_at    timestamp,
    PRIMARY KEY ((agent_id), cluster_id)
) WITH CLUSTERING ORDER BY (cluster_id ASC);

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_clusters_parent
    ON vector_clusters(parent_cluster_id)
    USING 'sai';

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_clusters_center
    ON vector_clusters(cluster_center)
    USING 'sai'
    WITH OPTIONS = {'similarity_function': 'COSINE'};


-- ---------------------------------------------------------------------------
-- vector_embeddings_batch：批次嵌入記錄表
-- 追踪批次生成的向量，用於版本控制和重新計算
-- PK: (batch_id); 支援按 agent_id 和狀態查詢
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS vector_embeddings_batch (
    batch_id        text PRIMARY KEY,
    agent_id        text,
    doc_id          text,
    model_name      text,
    model_version   text,
    chunk_count     int,
    dimension       int,
    batch_status    text,  -- 'pending', 'processing', 'completed', 'failed'
    error_message   text,
    stats_min_score float,
    stats_max_score float,
    stats_avg_score float,
    created_at      timestamp,
    completed_at    timestamp
);

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_embeddings_batch_agent
    ON vector_embeddings_batch(agent_id)
    USING 'sai';

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_embeddings_batch_status
    ON vector_embeddings_batch(batch_status)
    USING 'sai';

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_embeddings_batch_doc
    ON vector_embeddings_batch(doc_id)
    USING 'sai';


-- ---------------------------------------------------------------------------
-- vector_anomalies：向量異常檢測表
-- 記錄異常向量（離群值、品質低落等）
-- PK: (agent_id, anomaly_id); 支援按類型查詢
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS vector_anomalies (
    agent_id      text,
    anomaly_id    text,
    chunk_id      text,
    anomaly_type  text,  -- 'outlier', 'low_quality', 'duplicate', 'invalid'
    anomaly_score float,
    anomaly_detail text,
    flag_count    int,
    is_reviewed   boolean,
    reviewed_by   text,
    reviewed_at   timestamp,
    created_at    timestamp,
    PRIMARY KEY ((agent_id), anomaly_id)
) WITH CLUSTERING ORDER BY (anomaly_id DESC);

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_anomalies_chunk
    ON vector_anomalies(chunk_id)
    USING 'sai';

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_anomalies_type
    ON vector_anomalies(anomaly_type)
    USING 'sai';

CREATE CUSTOM INDEX IF NOT EXISTS idx_vector_anomalies_score
    ON vector_anomalies(anomaly_score)
    USING 'sai';
