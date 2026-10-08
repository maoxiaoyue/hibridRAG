-- ============================================================================
-- 001_create_memory.up.sql
-- 靜態 + 動態記憶：PersonalMemory / ProceduralMemory / EpisodicMemory / ToolMemory
-- 對應 app/models/memory.go
-- ============================================================================

-- 通用 updated_at 觸發器
CREATE OR REPLACE FUNCTION agent_touch_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ---------------------------------------------------------------------------
-- PersonalMemory：身份卡 + 表達 DNA（每個 agent 一筆）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS personal_memory (
    id             TEXT PRIMARY KEY,
    agent_id       TEXT NOT NULL,
    identity       JSONB NOT NULL,
    expression_dna JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_personal_memory_agent ON personal_memory(agent_id);
CREATE INDEX IF NOT EXISTS idx_personal_memory_identity_gin ON personal_memory USING gin(identity);

CREATE TRIGGER trg_personal_memory_touch BEFORE UPDATE ON personal_memory
    FOR EACH ROW EXECUTE PROCEDURE agent_touch_updated_at();

-- ---------------------------------------------------------------------------
-- ProceduralMemory：心智模型 + 決策啟發式（可多筆；每筆對應 RAG chunk embedding）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS procedural_memory (
    id           TEXT PRIMARY KEY,
    agent_id     TEXT NOT NULL,
    model_name   TEXT NOT NULL,
    content      TEXT NOT NULL,
    heuristics   JSONB,
    embedding_id TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_procedural_memory_agent ON procedural_memory(agent_id);
CREATE INDEX IF NOT EXISTS idx_procedural_memory_name ON procedural_memory(agent_id, model_name);
CREATE INDEX IF NOT EXISTS idx_procedural_memory_embedding ON procedural_memory(embedding_id);

CREATE TRIGGER trg_procedural_memory_touch BEFORE UPDATE ON procedural_memory
    FOR EACH ROW EXECUTE PROCEDURE agent_touch_updated_at();

-- ---------------------------------------------------------------------------
-- EpisodicMemory：對話歷史 / 事件（單輪或整段）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS episodic_memory (
    id          TEXT PRIMARY KEY,
    agent_id    TEXT NOT NULL,
    session_id  TEXT NOT NULL,
    turns       JSONB NOT NULL,
    summary     TEXT,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_episodic_memory_agent ON episodic_memory(agent_id);
CREATE INDEX IF NOT EXISTS idx_episodic_memory_session ON episodic_memory(agent_id, session_id);
CREATE INDEX IF NOT EXISTS idx_episodic_memory_occurred ON episodic_memory(occurred_at DESC);

-- ---------------------------------------------------------------------------
-- ToolMemory：工具簽名 + 使用統計
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tool_memory (
    id          TEXT PRIMARY KEY,
    agent_id    TEXT NOT NULL,
    tool_name   TEXT NOT NULL,
    signature   JSONB NOT NULL,
    usage_stats JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_tool_memory_agent_tool ON tool_memory(agent_id, tool_name);

CREATE TRIGGER trg_tool_memory_touch BEFORE UPDATE ON tool_memory
    FOR EACH ROW EXECUTE PROCEDURE agent_touch_updated_at();
