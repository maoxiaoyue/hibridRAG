-- ============================================================================
-- 002_create_shared_memory.up.sql
-- SharedMemory：跨 agent 協調用的 KV pool，以 pool_id（非 agent_id）隔離
-- 對應 app/models/shared_memory.go
--
-- 注意：實際建表由 app/models.AutoMigrate()（Bun CreateTable IfNotExists +
-- 欄位上的 unique:uq_shared_memory_pool_key 標籤）在服務啟動時自動執行；
-- 本檔案只是文件化的對照版本，供未來改走正式 migration 工具時參考。
-- ============================================================================

CREATE TABLE IF NOT EXISTS shared_memory (
    id         TEXT PRIMARY KEY,
    pool_id    TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      JSONB NOT NULL,
    written_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_shared_memory_pool_key ON shared_memory(pool_id, key);
CREATE INDEX IF NOT EXISTS idx_shared_memory_pool ON shared_memory(pool_id);

CREATE TRIGGER trg_shared_memory_touch BEFORE UPDATE ON shared_memory
    FOR EACH ROW EXECUTE PROCEDURE agent_touch_updated_at();
