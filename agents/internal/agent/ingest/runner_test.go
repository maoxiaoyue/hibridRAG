package ingest_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"agent/app/models"
	"agent/internal/agent/ingest"
)

// ---- mock MemoryWriter ----

type mockMemory struct {
	personal   []models.CreatePersonalMemoryRequest
	procedural []models.CreateProceduralMemoryRequest
	errPersonal  error
	errProcedural error
}

func (m *mockMemory) UpsertPersonal(_ context.Context, req models.CreatePersonalMemoryRequest) (*models.PersonalMemory, error) {
	if m.errPersonal != nil {
		return nil, m.errPersonal
	}
	m.personal = append(m.personal, req)
	return &models.PersonalMemory{ID: "pm_test", AgentID: req.AgentID}, nil
}

func (m *mockMemory) UpsertProcedural(_ context.Context, req models.CreateProceduralMemoryRequest) (*models.ProceduralMemory, error) {
	if m.errProcedural != nil {
		return nil, m.errProcedural
	}
	m.procedural = append(m.procedural, req)
	return &models.ProceduralMemory{ID: "prm_test", AgentID: req.AgentID, ModelName: req.ModelName}, nil
}

// ---- helpers ----

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeTemp: %v", err)
	}
	return path
}

func makeJob(agentID string) *models.IngestJob {
	return &models.IngestJob{ID: "job_test", AgentID: agentID, Kind: models.IngestKindSkill}
}

// ---- tests ----

const fullSKILL = `# Test Agent

## Identity
- name: TestBot
- role: Tester
- domain: QA

## Expression DNA
- tone: neutral

## Mental Models

### Model A
Content of model A.

**Heuristics**
- rule1: do this first

### Model B
Content of model B.
`

func TestRunSkill_Full(t *testing.T) {
	mem := &mockMemory{}
	runner := ingest.NewRunner(mem)
	path := writeTemp(t, fullSKILL)
	job := makeJob("agent-1")

	req := models.IngestSkillRequest{AgentID: "agent-1", SkillPath: path}
	if err := runner.RunSkill(context.Background(), job, req); err != nil {
		t.Fatalf("RunSkill returned error: %v", err)
	}

	// PersonalMemory: 1 upsert
	if len(mem.personal) != 1 {
		t.Errorf("personal upserts = %d, want 1", len(mem.personal))
	}
	if mem.personal[0].AgentID != "agent-1" {
		t.Errorf("AgentID = %q", mem.personal[0].AgentID)
	}
	if mem.personal[0].Identity["name"] != "TestBot" {
		t.Errorf("Identity[name] = %v", mem.personal[0].Identity["name"])
	}

	// ProceduralMemory: 2 upserts
	if len(mem.procedural) != 2 {
		t.Errorf("procedural upserts = %d, want 2", len(mem.procedural))
	}
	if mem.procedural[0].ModelName != "Model A" {
		t.Errorf("procedural[0].ModelName = %q", mem.procedural[0].ModelName)
	}
	if mem.procedural[1].ModelName != "Model B" {
		t.Errorf("procedural[1].ModelName = %q", mem.procedural[1].ModelName)
	}

	// Job progress: total=3 (1 personal + 2 procedural), done=3
	if job.TotalChunks != 3 {
		t.Errorf("TotalChunks = %d, want 3", job.TotalChunks)
	}
	if job.DoneChunks != 3 {
		t.Errorf("DoneChunks = %d, want 3", job.DoneChunks)
	}
}

func TestRunSkill_NoMentalModels(t *testing.T) {
	md := `# Slim Agent
## Identity
- role: Helper
`
	mem := &mockMemory{}
	runner := ingest.NewRunner(mem)
	path := writeTemp(t, md)
	job := makeJob("agent-2")

	if err := runner.RunSkill(context.Background(), job, models.IngestSkillRequest{
		AgentID: "agent-2", SkillPath: path,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mem.personal) != 1 {
		t.Errorf("personal = %d, want 1", len(mem.personal))
	}
	if len(mem.procedural) != 0 {
		t.Errorf("procedural = %d, want 0", len(mem.procedural))
	}
	if job.TotalChunks != 1 || job.DoneChunks != 1 {
		t.Errorf("progress: total=%d done=%d, want 1/1", job.TotalChunks, job.DoneChunks)
	}
}

func TestRunSkill_FileNotFound(t *testing.T) {
	runner := ingest.NewRunner(&mockMemory{})
	job := makeJob("agent-3")
	err := runner.RunSkill(context.Background(), job, models.IngestSkillRequest{
		AgentID: "agent-3", SkillPath: "/nonexistent/SKILL.md",
	})
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestRunSkill_EmptyFile(t *testing.T) {
	runner := ingest.NewRunner(&mockMemory{})
	job := makeJob("agent-4")
	path := writeTemp(t, "")
	err := runner.RunSkill(context.Background(), job, models.IngestSkillRequest{
		AgentID: "agent-4", SkillPath: path,
	})
	if err == nil {
		t.Error("expected error for empty SKILL.md")
	}
}

func TestRunSkill_PersonalError(t *testing.T) {
	mem := &mockMemory{errPersonal: errDB}
	runner := ingest.NewRunner(mem)
	path := writeTemp(t, fullSKILL)
	job := makeJob("agent-5")
	err := runner.RunSkill(context.Background(), job, models.IngestSkillRequest{
		AgentID: "agent-5", SkillPath: path,
	})
	if err == nil {
		t.Error("expected error when personal upsert fails")
	}
}

func TestRunSkill_ProceduralError(t *testing.T) {
	mem := &mockMemory{errProcedural: errDB}
	runner := ingest.NewRunner(mem)
	path := writeTemp(t, fullSKILL)
	job := makeJob("agent-6")
	err := runner.RunSkill(context.Background(), job, models.IngestSkillRequest{
		AgentID: "agent-6", SkillPath: path,
	})
	if err == nil {
		t.Error("expected error when procedural upsert fails")
	}
}

func TestRunReferences_NotImplemented(t *testing.T) {
	runner := ingest.NewRunner(&mockMemory{})
	job := makeJob("agent-7")
	err := runner.RunReferences(context.Background(), job, models.IngestReferencesRequest{
		AgentID: "agent-7", Paths: []string{"a.md", "b.md"},
	})
	if err == nil {
		t.Error("RunReferences should return error (not yet implemented)")
	}
	// job.TotalChunks 應反映路徑數
	if job.TotalChunks != 2 {
		t.Errorf("TotalChunks = %d, want 2", job.TotalChunks)
	}
}

var errDB = fmt.Errorf("db connection refused")
