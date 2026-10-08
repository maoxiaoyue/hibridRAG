-- ============================================================================
-- 002_create_ingest.up.sql
-- 知識攝取管線：ingest_jobs（③）
-- 對應 app/models/ingest.go
-- ============================================================================

CREATE TABLE IF NOT EXISTS ingest_jobs (
    id           TEXT PRIMARY KEY,
    agent_id     TEXT NOT NULL,
    kind         TEXT NOT NULL,              -- skill / reference
    source_path  TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending', -- pending/running/completed/failed
    total_chunks INTEGER NOT NULL DEFAULT 0,
    done_chunks  INTEGER NOT NULL DEFAULT 0,
    error        TEXT,
    metadata     JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_ingest_jobs_agent ON ingest_jobs(agent_id);
CREATE INDEX IF NOT EXISTS idx_ingest_jobs_status ON ingest_jobs(status);
CREATE INDEX IF NOT EXISTS idx_ingest_jobs_kind ON ingest_jobs(agent_id, kind);
CREATE INDEX IF NOT EXISTS idx_ingest_jobs_created ON ingest_jobs(created_at DESC);

CREATE TRIGGER trg_ingest_jobs_touch BEFORE UPDATE ON ingest_jobs
    FOR EACH ROW EXECUTE PROCEDURE agent_touch_updated_at();
