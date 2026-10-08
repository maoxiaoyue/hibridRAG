DROP TRIGGER IF EXISTS trg_tool_memory_touch ON tool_memory;
DROP TRIGGER IF EXISTS trg_procedural_memory_touch ON procedural_memory;
DROP TRIGGER IF EXISTS trg_personal_memory_touch ON personal_memory;

DROP TABLE IF EXISTS tool_memory;
DROP TABLE IF EXISTS episodic_memory;
DROP TABLE IF EXISTS procedural_memory;
DROP TABLE IF EXISTS personal_memory;

DROP FUNCTION IF EXISTS agent_touch_updated_at();
