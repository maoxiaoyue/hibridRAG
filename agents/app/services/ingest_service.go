package services

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"

	"agent/app/models"
	"agent/internal/eventbus"
	"agent/internal/logger"
)

// IngestService 處理 SKILL.md / references 攝取任務
// 實際的 chunk + embed 流程由 internal/agent/ingest 執行（此處只負責建立/查詢 job）
type IngestService struct {
	db      *bun.DB
	runner  IngestRunner // 可注入非同步執行器，單元測試可用 nil
	bus     eventbus.Publisher
	streams IngestStreams
}

// IngestStreams 由呼叫端注入的 stream 名稱（避免硬編碼）
type IngestStreams struct {
	Started   string
	Completed string
	Failed    string
}

// EventBusPublisher 為 eventbus.Publisher 的對外別名
// 供 controllers 注入時使用，避免 controllers 直接 import internal/eventbus
type EventBusPublisher = eventbus.Publisher

// IngestRunner 抽象化實際攝取工作（由呼叫方提供實作）
//   - Skill 攝取：解析 SKILL.md → 寫 PersonalMemory / ProceduralMemory → chunk references → embed
//   - Reference 攝取：chunk → embed → 寫 Cassandra(dense) + PG(sparse)
type IngestRunner interface {
	RunSkill(ctx context.Context, job *models.IngestJob, req models.IngestSkillRequest) error
	RunReferences(ctx context.Context, job *models.IngestJob, req models.IngestReferencesRequest) error
}

func NewIngestService(db *bun.DB, runner IngestRunner) *IngestService {
	return &IngestService{db: db, runner: runner, bus: eventbus.NewNopPublisher()}
}

// WithEventBus 注入 Event Bus publisher 與 stream 名稱（在 main 啟動時呼叫）
func (s *IngestService) WithEventBus(bus eventbus.Publisher, streams IngestStreams) *IngestService {
	if bus == nil {
		bus = eventbus.NewNopPublisher()
	}
	s.bus = bus
	s.streams = streams
	return s
}

// emit 發送事件，失敗僅記 log
func (s *IngestService) emit(stream string, ev eventbus.Event) {
	if s.bus == nil || stream == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := s.bus.Publish(ctx, stream, ev); err != nil {
		logger.Warning("eventbus publish %s: %v", stream, err)
	}
}

// EnqueueSkill 建立 skill 攝取任務（非同步執行）
func (s *IngestService) EnqueueSkill(req models.IngestSkillRequest) (*models.IngestJob, error) {
	job := &models.IngestJob{
		ID:         newID("job"),
		AgentID:    req.AgentID,
		Kind:       models.IngestKindSkill,
		SourcePath: req.SkillPath,
		Status:     models.IngestStatusPending,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if _, err := s.db.NewInsert().Model(job).Exec(context.Background()); err != nil {
		return nil, err
	}
	s.dispatchSkill(job, req)
	return job, nil
}

// EnqueueReferences 建立 references 批次攝取任務
func (s *IngestService) EnqueueReferences(req models.IngestReferencesRequest) (*models.IngestJob, error) {
	src := ""
	if len(req.Paths) > 0 {
		src = req.Paths[0]
	}
	job := &models.IngestJob{
		ID:         newID("job"),
		AgentID:    req.AgentID,
		Kind:       models.IngestKindReference,
		SourcePath: src,
		Status:     models.IngestStatusPending,
		Metadata:   map[string]interface{}{"paths": req.Paths, "tags": req.Tags},
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if _, err := s.db.NewInsert().Model(job).Exec(context.Background()); err != nil {
		return nil, err
	}
	s.dispatchReferences(job, req)
	return job, nil
}

// GetJob 查詢任務狀態
func (s *IngestService) GetJob(id string) (*models.IngestJob, error) {
	j := new(models.IngestJob)
	err := s.db.NewSelect().Model(j).Where("id = ?", id).Scan(context.Background())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return j, nil
}

func (s *IngestService) dispatchSkill(job *models.IngestJob, req models.IngestSkillRequest) {
	if s.runner == nil {
		return
	}
	go func() {
		ctx := context.Background()
		s.markRunning(job)
		err := s.runner.RunSkill(ctx, job, req)
		s.markDone(job, err)
	}()
}

func (s *IngestService) dispatchReferences(job *models.IngestJob, req models.IngestReferencesRequest) {
	if s.runner == nil {
		return
	}
	go func() {
		ctx := context.Background()
		s.markRunning(job)
		err := s.runner.RunReferences(ctx, job, req)
		s.markDone(job, err)
	}()
}

func (s *IngestService) markRunning(job *models.IngestJob) {
	job.Status = models.IngestStatusRunning
	job.UpdatedAt = time.Now()
	_, _ = s.db.NewUpdate().Model(job).WherePK().
		Column("status", "updated_at").Exec(context.Background())

	s.emit(s.streams.Started, eventbus.Event{
		Type:    eventbus.EventIngestStarted,
		AgentID: job.AgentID,
		Payload: map[string]interface{}{
			"job_id":      job.ID,
			"kind":        string(job.Kind),
			"source_path": job.SourcePath,
		},
	})
}

func (s *IngestService) markDone(job *models.IngestJob, runErr error) {
	now := time.Now()
	job.UpdatedAt = now
	job.FinishedAt = &now
	if runErr != nil {
		job.Status = models.IngestStatusFailed
		job.Error = runErr.Error()
	} else {
		job.Status = models.IngestStatusCompleted
	}
	_, _ = s.db.NewUpdate().Model(job).WherePK().
		Column("status", "error", "updated_at", "finished_at", "done_chunks", "total_chunks").
		Exec(context.Background())

	if runErr != nil {
		s.emit(s.streams.Failed, eventbus.Event{
			Type:    eventbus.EventIngestFailed,
			AgentID: job.AgentID,
			Payload: map[string]interface{}{
				"job_id":       job.ID,
				"kind":         string(job.Kind),
				"error":        runErr.Error(),
				"done_chunks":  job.DoneChunks,
				"total_chunks": job.TotalChunks,
			},
		})
	} else {
		s.emit(s.streams.Completed, eventbus.Event{
			Type:    eventbus.EventIngestCompleted,
			AgentID: job.AgentID,
			Payload: map[string]interface{}{
				"job_id":       job.ID,
				"kind":         string(job.Kind),
				"done_chunks":  job.DoneChunks,
				"total_chunks": job.TotalChunks,
				"duration_ms":  now.Sub(job.CreatedAt).Milliseconds(),
			},
		})
	}
}
