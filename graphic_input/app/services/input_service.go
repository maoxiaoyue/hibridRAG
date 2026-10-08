// input_service.go — graphic_input 核心：驗證/重複偵測/雙路徑路由/批次/輸入紀錄。
// REST 與 gRPC controller 都委派到這裡，邏輯只有一份。
package services

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"graphic_input/app/models"
)

// Targets 文件雙路徑的路由選項。
const (
	TargetObsidian = "obsidian"
	TargetPG       = "pg"
	TargetBoth     = "both"
	// TargetTemp 知識沉澱：寫進 vault/temp/（未分類），由 agent_graph 的 digest
	// worker 挑走分類、搬進分類目錄後才攝取進圖譜。不走 PG 直寫、不進 inbox。
	TargetTemp = "temp"
)

// InputService 聚合 graph client、vault writer、vision OCR 與 log DB。
type InputService struct {
	Graph  *GraphClient
	Vault  *VaultWriter
	Vision *VisionClient
	DB     *bun.DB
}

// ── 結構化輸入 ───────────────────────────────────────────────────────────────

// CheckDuplicates 回相似實體候選（substring match，排除完全同名——同名是更新不是重複）。
func (s *InputService) CheckDuplicates(ctx context.Context, name string) ([]models.SimilarCandidate, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	hits, err := s.Graph.SearchEntities(ctx, name, 5)
	if err != nil {
		return nil, err
	}
	out := make([]models.SimilarCandidate, 0, len(hits))
	for _, h := range hits {
		if strings.EqualFold(h.Name, name) {
			continue
		}
		out = append(out, models.SimilarCandidate{Name: h.Name, Category: h.Category, Description: h.Description})
	}
	return out, nil
}

// UpsertEntity 結構化建立/更新實體。force=false 且有相似候選時不寫入、回候選清單
// （使用者選「合併到既有」= 改用既有名稱重送，或「確認新建」= force=true 重送）。
func (s *InputService) UpsertEntity(ctx context.Context, req models.UpsertEntityRequest) (*models.UpsertEntityResponse, error) {
	name := strings.TrimSpace(req.Entity.Name)
	if name == "" {
		return nil, fmt.Errorf("entity.name is required")
	}

	if !req.Force {
		candidates, err := s.CheckDuplicates(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("duplicate check: %w", err)
		}
		if len(candidates) > 0 {
			s.logInput(ctx, req.Actor, "upsert_entity", "-", map[string]any{"entity": req.Entity}, "rejected_duplicate")
			return &models.UpsertEntityResponse{Written: false, Candidates: candidates}, nil
		}
	}

	embSynced, err := s.Graph.UpsertEntity(ctx, name, strings.TrimSpace(req.Entity.Category),
		strings.TrimSpace(req.Entity.Description), sourceTag(req.Actor))
	if err != nil {
		s.logInput(ctx, req.Actor, "upsert_entity", "-", map[string]any{"entity": req.Entity}, "error:"+err.Error())
		return nil, err
	}
	s.logInput(ctx, req.Actor, "upsert_entity", "-", map[string]any{"entity": req.Entity}, "ok")
	return &models.UpsertEntityResponse{Written: true, EmbeddingSynced: embSynced}, nil
}

// UpsertRelation 結構化建立關係。
func (s *InputService) UpsertRelation(ctx context.Context, req models.UpsertRelationRequest) error {
	src, tgt := strings.TrimSpace(req.Source), strings.TrimSpace(req.Target)
	if src == "" || tgt == "" {
		return fmt.Errorf("source and target are required")
	}
	relType := strings.TrimSpace(req.Type)
	if relType == "" {
		relType = "related_to"
	}
	err := s.Graph.UpsertRelation(ctx, src, tgt, relType)
	result := "ok"
	if err != nil {
		result = "error:" + err.Error()
	}
	s.logInput(ctx, req.Actor, "upsert_relation", "-",
		map[string]any{"source": src, "target": tgt, "type": relType}, result)
	return err
}

// ── 批次匯入 ─────────────────────────────────────────────────────────────────

// ParseBatch 把 BatchImportRequest 正規化成列清單（CSV 或 rows 擇一）。
func ParseBatch(req models.BatchImportRequest) ([]models.BatchRow, error) {
	if strings.TrimSpace(req.CSV) != "" {
		return parseCSVRows(req.CSVKind, req.CSV)
	}
	if len(req.Rows) == 0 {
		return nil, fmt.Errorf("either rows or csv is required")
	}
	return req.Rows, nil
}

func parseCSVRows(kind, raw string) ([]models.BatchRow, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "entity" && kind != "relation" {
		return nil, fmt.Errorf("csv_kind must be entity or relation")
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimSpace(raw)))
	reader.FieldsPerRecord = -1 // 允許每列欄位數不同（entity 的 category/description 可省略）
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse csv: %w", err)
	}
	var rows []models.BatchRow
	for _, rec := range records {
		for i := range rec {
			rec[i] = strings.TrimSpace(rec[i])
		}
		if kind == "entity" {
			// name,category,description（category/description 可省略）
			row := models.BatchRow{Kind: "entity", Entity: &models.EntityInput{Name: rec[0]}}
			if len(rec) > 1 {
				row.Entity.Category = rec[1]
			}
			if len(rec) > 2 {
				row.Entity.Description = strings.Join(rec[2:], ",")
			}
			rows = append(rows, row)
		} else {
			// source,type,target
			if len(rec) < 3 {
				rows = append(rows, models.BatchRow{Kind: "relation", Relation: &models.UpsertRelationRequest{Source: rec[0]}})
				continue
			}
			rows = append(rows, models.BatchRow{Kind: "relation", Relation: &models.UpsertRelationRequest{
				Source: rec[0], Type: rec[1], Target: rec[2],
			}})
		}
	}
	return rows, nil
}

// BatchImport 逐列執行；單列失敗不中斷。實體列不做重複偵測攔截（批次語意是
// 「照單全收」，MERGE 冪等；要逐筆確認的用單筆 API）。
func (s *InputService) BatchImport(ctx context.Context, actor string, rows []models.BatchRow) *models.BatchImportResponse {
	resp := &models.BatchImportResponse{}
	for i, row := range rows {
		var err error
		switch strings.ToLower(row.Kind) {
		case "entity":
			if row.Entity == nil || strings.TrimSpace(row.Entity.Name) == "" {
				err = fmt.Errorf("entity.name is required")
			} else {
				_, err = s.Graph.UpsertEntity(ctx, strings.TrimSpace(row.Entity.Name),
					strings.TrimSpace(row.Entity.Category), strings.TrimSpace(row.Entity.Description), sourceTag(actor))
			}
		case "relation":
			if row.Relation == nil || strings.TrimSpace(row.Relation.Source) == "" || strings.TrimSpace(row.Relation.Target) == "" {
				err = fmt.Errorf("relation.source and relation.target are required")
			} else {
				relType := strings.TrimSpace(row.Relation.Type)
				if relType == "" {
					relType = "related_to"
				}
				err = s.Graph.UpsertRelation(ctx, strings.TrimSpace(row.Relation.Source),
					strings.TrimSpace(row.Relation.Target), relType)
			}
		default:
			err = fmt.Errorf("unknown kind %q (want entity or relation)", row.Kind)
		}

		result := "ok"
		if err != nil {
			result = "error:" + err.Error()
			resp.Failed++
		} else {
			resp.OK++
		}
		resp.Rows = append(resp.Rows, models.BatchRowResult{Index: i, Result: result})
	}
	s.logInput(ctx, actor, "batch_import", "-",
		map[string]any{"rows": len(rows), "ok": resp.OK, "failed": resp.Failed}, "ok")
	return resp
}

// ── 文件雙路徑輸入 ───────────────────────────────────────────────────────────

// DocFile 一個待處理的上傳檔案。
type DocFile struct {
	Name string
	Data []byte
}

// UploadDocuments 逐檔抽文字 + 依 targets 路由；單檔失敗不中斷整批。
func (s *InputService) UploadDocuments(ctx context.Context, targets, actor string, tags []string, files []DocFile) ([]models.DocResult, error) {
	targets = strings.ToLower(strings.TrimSpace(targets))
	switch targets {
	case TargetObsidian, TargetPG, TargetBoth, TargetTemp:
	default:
		return nil, fmt.Errorf("targets must be obsidian, pg, both, or temp")
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files")
	}

	results := make([]models.DocResult, 0, len(files))
	for _, f := range files {
		r := s.processDoc(ctx, targets, actor, tags, f)
		logResult := "ok"
		if r.Error != "" {
			logResult = "error:" + r.Error
		}
		s.logInput(ctx, actor, "upload_doc", targets, map[string]any{
			"file": f.Name, "chars": r.ExtractedChars, "obsidian_path": r.ObsidianPath,
			"pg_entities": r.PGEntities, "pg_merged": r.PGRelationsMerged, "pg_proposed": r.PGRelationsProposed,
		}, logResult)
		results = append(results, r)
	}
	return results, nil
}

func (s *InputService) processDoc(ctx context.Context, targets, actor string, tags []string, f DocFile) models.DocResult {
	r := models.DocResult{File: f.Name}

	text, err := ExtractText(ctx, s.Vision, f.Name, f.Data)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.ExtractedChars = len(text)

	title := strings.TrimSuffix(filepath.Base(f.Name), filepath.Ext(f.Name))

	// temp：落 vault/temp/（未分類），交給 digest worker 分類。與 obsidian（inbox）
	// 差別只在落點目錄，其餘 frontmatter/attachment 邏輯相同。
	if targets == TargetObsidian || targets == TargetBoth || targets == TargetTemp {
		opt := NoteOptions{Title: title, Body: text, Actor: actor, OriginalFile: f.Name, Tags: tags}
		if targets == TargetTemp {
			opt.Dir = s.Vault.TempDirName()
		}
		if ClassifyFile(f.Name) == KindImage {
			opt.Attachment, opt.AttachName = f.Data, f.Name
		}
		path, werr := s.Vault.WriteNote(opt)
		if werr != nil {
			r.Error = werr.Error()
			return r
		}
		r.ObsidianPath = path
	}

	if targets == TargetPG || targets == TargetBoth {
		stats, ierr := s.Graph.IngestText(ctx, f.Name, text, actor)
		if ierr != nil {
			r.Error = ierr.Error()
			return r
		}
		r.PGEntities = stats.Entities
		r.PGRelationsMerged = stats.RelationsMerged
		r.PGRelationsProposed = stats.RelationsProposed
		r.PGEmbeddings = stats.Embeddings
	}
	return r
}

// ── 輸入紀錄 ─────────────────────────────────────────────────────────────────

// ListLog 最近的輸入紀錄（新的在前）。
func (s *InputService) ListLog(ctx context.Context, limit int) ([]models.InputLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var out []models.InputLog
	if err := s.DB.NewSelect().Model(&out).Order("created_at DESC").Limit(limit).Scan(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// logInput best-effort 寫入紀錄（失敗不影響主流程）。
func (s *InputService) logInput(ctx context.Context, actor, action, target string, payload map[string]any, result string) {
	if s.DB == nil {
		return
	}
	rec := models.InputLog{
		ID: "gi_" + randHex(8), Actor: actor, Action: action, Target: target,
		Payload: payload, Result: result, CreatedAt: time.Now(),
	}
	_, _ = s.DB.NewInsert().Model(&rec).Exec(ctx)
}

func sourceTag(actor string) string {
	if actor == "" {
		return "graphic_input"
	}
	return "graphic_input:" + actor
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
