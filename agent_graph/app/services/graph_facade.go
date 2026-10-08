// graph_facade.go — thin facade combining the Kùzu graph backend + EmbedClient
// behind the 5 operations controllers (REST + gRPC) expose. Keeps controllers
// free of any Kùzu/Ollama-specific detail.
package services

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"agent_graph/app/models"
)

type GraphFacade struct {
	// Graph is the property-graph backend (embedded Kùzu);
	// Vectors is the ANN store (Kùzu HNSW). Both are interfaces
	// so the concrete wiring lives in main.go
	// and this facade never branches on it.
	Graph   GraphBackend
	Vectors VectorBackend
	Embed   *EmbedClient
	Chat    *ChatClient // IngestText 的 LLM 抽取用；nil = IngestText 停用
	DB      *bun.DB
	// Exporter 圖譜→vault .md 鏡像（nil = 停用）。每個寫入操作成功後 Notify
	// 受影響的實體，由 exporter 的背景迴圈 debounce 後重新匯出。
	Exporter *VaultExporter
	// ConfidenceThreshold IngestText 抽取關係的自動 MERGE 門檻（<=0 → 0.8，
	// 與 sync worker 同一預設）；低於門檻進 proposed_relations 待審。
	ConfidenceThreshold float64
}

// notifyExport 通知 exporter 這些實體剛被寫入（nil exporter = no-op）。
func (f *GraphFacade) notifyExport(names ...string) {
	if f.Exporter != nil {
		f.Exporter.Notify(names...)
	}
}

// degreeMap 全圖 degree（實體名 → 關係數）。best-effort：查詢失敗回 nil，
// 呼叫端把 degree 留 0——degree 是查詢結果的裝飾資訊，不該讓它弄失敗主查詢。
func (f *GraphFacade) degreeMap(ctx context.Context) map[string]int {
	if f.Graph == nil || !f.Graph.Enabled() {
		return nil
	}
	rels, err := f.Graph.ListRelations(ctx)
	if err != nil {
		return nil
	}
	m := make(map[string]int, len(rels))
	for _, r := range rels {
		m[r.Source]++
		m[r.Target]++
	}
	return m
}

func fillDegrees(m map[string]int, entities []models.EntityResult) {
	if m == nil {
		return
	}
	for i := range entities {
		entities[i].Degree = m[entities[i].Name]
	}
}

func (f *GraphFacade) SearchEntities(ctx context.Context, query string, limit int) ([]models.EntityResult, error) {
	entities, err := f.Graph.SearchEntities(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	fillDegrees(f.degreeMap(ctx), entities)
	return entities, nil
}

func (f *GraphFacade) GetLocalContext(ctx context.Context, name string, hops, limit int) (*models.LocalContextResponse, error) {
	resp, err := f.Graph.GetLocalContext(ctx, name, hops, limit)
	if err != nil {
		return nil, err
	}
	m := f.degreeMap(ctx)
	if m != nil {
		resp.Center.Degree = m[resp.Center.Name]
		fillDegrees(m, resp.Neighbors)
	}
	return resp, nil
}

func (f *GraphFacade) ExecuteCypher(ctx context.Context, query string, params map[string]any, cols []string) ([]map[string]any, error) {
	return f.Graph.ExecuteCypher(ctx, query, params, cols)
}

// HybridSearch embeds the query, finds the closest entities by cosine
// similarity, then expands each into its local graph neighborhood. A seed
// whose graph lookup fails (or AGE disabled) is still returned bare — vector
// hits are useful on their own, don't drop them over a traversal error.
func (f *GraphFacade) HybridSearch(ctx context.Context, query string, topK, hops int) (*models.HybridSearchResponse, error) {
	if f.Embed == nil || f.Vectors == nil || !f.Vectors.Enabled() {
		return nil, ErrVectorDisabled
	}
	vec, err := f.Embed.EmbedOne(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	hits, err := f.Vectors.SearchSimilar(ctx, vec, topK)
	if err != nil {
		return nil, err
	}

	resp := &models.HybridSearchResponse{}
	degrees := f.degreeMap(ctx)
	for _, hit := range hits {
		resp.Seeds = append(resp.Seeds, models.EntityResult{
			Name: hit.EntityName, Score: hit.Score, Degree: degrees[hit.EntityName],
		})
		if f.Graph == nil || !f.Graph.Enabled() {
			continue
		}
		if ctxResp, cerr := f.Graph.GetLocalContext(ctx, hit.EntityName, hops, 20); cerr == nil {
			if degrees != nil {
				ctxResp.Center.Degree = degrees[ctxResp.Center.Name]
				fillDegrees(degrees, ctxResp.Neighbors)
			}
			resp.Context = append(resp.Context, *ctxResp)
		}
	}
	return resp, nil
}

// ProposeRelation persists a low-confidence relation candidate for human
// review — never writes to the graph directly (see internal/sync's
// confidenceThreshold gate for the same rule applied during vault sync).
func (f *GraphFacade) ProposeRelation(ctx context.Context, req models.ProposeRelationRequest) (*models.ProposedRelation, error) {
	pr := models.ProposedRelation{
		ID: newID("pr"), SourceEntity: req.SourceEntity, TargetEntity: req.TargetEntity,
		RelationType: req.RelationType, Confidence: req.Confidence, SourceFile: req.SourceFile,
		ExtractedBy: req.ExtractedBy, Status: "pending", CreatedAt: time.Now(),
	}
	if _, err := f.DB.NewInsert().Model(&pr).Exec(ctx); err != nil {
		return nil, fmt.Errorf("insert proposed relation: %w", err)
	}
	return &pr, nil
}

// ListProposed 依 status（空 = pending）回傳待審關係，最舊的排前面（先進先審）。
func (f *GraphFacade) ListProposed(ctx context.Context, status string, limit int) ([]models.ProposedRelation, error) {
	if status == "" {
		status = "pending"
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []models.ProposedRelation
	err := f.DB.NewSelect().Model(&out).
		Where("status = ?", status).
		Order("created_at ASC").
		Limit(limit).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list proposed relations: %w", err)
	}
	return out, nil
}

// ApproveProposed 把一筆 pending 的待審關係 MERGE 進圖譜並標記 approved。
// 圖譜寫入成功才更新狀態——寫入失敗時保持 pending，可重試。
func (f *GraphFacade) ApproveProposed(ctx context.Context, id string) (*models.ProposedRelation, error) {
	pr, err := f.getProposed(ctx, id)
	if err != nil {
		return nil, err
	}
	if pr.Status != "pending" {
		return nil, fmt.Errorf("%w (status=%s)", ErrAlreadyReviewed, pr.Status)
	}
	if f.Graph == nil || !f.Graph.Enabled() {
		return nil, ErrGraphDisabled
	}
	// UpsertRelation 會自動 MERGE 缺少的端點成 stub 節點（同 WikiLink 同步的行為）。
	if err := f.Graph.UpsertRelation(ctx, pr.SourceEntity, pr.TargetEntity, pr.RelationType); err != nil {
		return nil, fmt.Errorf("merge relation into graph: %w", err)
	}
	f.notifyExport(pr.SourceEntity, pr.TargetEntity)
	return f.setProposedStatus(ctx, pr, "approved")
}

// RejectProposed 把一筆 pending 的待審關係標記 rejected（不碰圖譜）。
func (f *GraphFacade) RejectProposed(ctx context.Context, id string) (*models.ProposedRelation, error) {
	pr, err := f.getProposed(ctx, id)
	if err != nil {
		return nil, err
	}
	if pr.Status != "pending" {
		return nil, fmt.Errorf("%w (status=%s)", ErrAlreadyReviewed, pr.Status)
	}
	return f.setProposedStatus(ctx, pr, "rejected")
}

// ── dev_090 graphic_input 配套：直接寫入 + 純文字攝取 ────────────────────────
// 這些寫入操作不暴露給 agents 的 graph_query 工具、gateway 也不轉發——
// 呼叫端只有內網的 graphic_input 服務（人工輸入本身就是審核）。

// UpsertEntityDirect 直接建立/更新實體，並 best-effort 同步 embedding
// （回傳 EmbeddingSynced 讓呼叫端知道 hybrid_search 是否找得到它）。
func (f *GraphFacade) UpsertEntityDirect(ctx context.Context, req models.UpsertEntityRequest) (*models.UpsertEntityResponse, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("entity name is required")
	}
	if f.Graph == nil || !f.Graph.Enabled() {
		return nil, ErrGraphDisabled
	}
	if err := f.Graph.UpsertEntity(ctx, models.EntityResult{
		Name: req.Name, Category: req.Category, Description: req.Description, SourceFile: req.SourceFile,
	}); err != nil {
		return nil, fmt.Errorf("upsert entity: %w", err)
	}
	f.notifyExport(req.Name)
	return &models.UpsertEntityResponse{
		Name:            req.Name,
		EmbeddingSynced: f.syncEmbedding(ctx, req.Name, req.Description),
	}, nil
}

// UpsertRelationDirect 直接 MERGE 一條關係（自動補 stub 端點，同 WikiLink 同步行為）。
func (f *GraphFacade) UpsertRelationDirect(ctx context.Context, req models.UpsertRelationRequest) error {
	if req.Source == "" || req.Target == "" {
		return fmt.Errorf("source and target are required")
	}
	if req.Type == "" {
		req.Type = "related_to"
	}
	if f.Graph == nil || !f.Graph.Enabled() {
		return ErrGraphDisabled
	}
	if err := f.Graph.UpsertRelation(ctx, req.Source, req.Target, req.Type); err != nil {
		return fmt.Errorf("upsert relation: %w", err)
	}
	f.notifyExport(req.Source, req.Target)
	return nil
}

// IngestText 純文字 → LLM 抽取 → 高信心 MERGE / 低信心 proposed_relations +
// 實體 embedding。confidence gate 語意與 internal/sync 的 applyExtraction 一致
// （該版有 per-item logger 與檔案 hash 狀態，這版是無狀態的立即攝取——兩份
// 刻意分開，改抽取「路由規則」時兩處要同步）。
func (f *GraphFacade) IngestText(ctx context.Context, req models.IngestTextRequest) (*models.IngestTextResponse, error) {
	if req.Text == "" {
		return nil, fmt.Errorf("text is required")
	}
	if f.Chat == nil {
		return nil, fmt.Errorf("text ingestion unavailable: LLM extraction client not configured")
	}
	if f.Graph == nil || !f.Graph.Enabled() {
		return nil, ErrGraphDisabled
	}

	threshold := f.ConfidenceThreshold
	if threshold <= 0 {
		threshold = 0.8
	}
	source := req.Source
	if source == "" {
		source = "graphic_input"
	}

	result, err := f.Chat.ExtractEntitiesRelations(ctx, source, req.Text)
	if err != nil {
		return nil, fmt.Errorf("llm extraction: %w", err)
	}

	resp := &models.IngestTextResponse{}
	// 部分失敗時也要把已寫入的實體通知 exporter——鏡像跟著圖譜實際狀態走。
	var touched []string
	defer func() { f.notifyExport(touched...) }()
	for _, e := range result.Entities {
		if e.Name == "" {
			continue
		}
		if err := f.Graph.UpsertEntity(ctx, models.EntityResult{
			Name: e.Name, Category: e.Category, Description: e.Description, SourceFile: source,
		}); err != nil {
			return resp, fmt.Errorf("upsert extracted entity %q: %w", e.Name, err)
		}
		touched = append(touched, e.Name)
		resp.Entities++
		if f.syncEmbedding(ctx, e.Name, e.Description) {
			resp.Embeddings++
		}
	}

	extractedBy := "graphic_input"
	if req.Actor != "" {
		extractedBy = "graphic_input:" + req.Actor
	}
	for _, r := range result.Relations {
		if r.Source == "" || r.Target == "" {
			continue
		}
		if r.Confidence >= threshold {
			if err := f.Graph.UpsertRelation(ctx, r.Source, r.Target, r.Type); err != nil {
				return resp, fmt.Errorf("merge relation %s -> %s: %w", r.Source, r.Target, err)
			}
			touched = append(touched, r.Source, r.Target)
			resp.RelationsMerged++
			continue
		}
		pr := models.ProposedRelation{
			ID: newID("pr"), SourceEntity: r.Source, TargetEntity: r.Target, RelationType: r.Type,
			Confidence: r.Confidence, SourceFile: source, ExtractedBy: extractedBy,
			Status: "pending", CreatedAt: time.Now(),
		}
		if _, err := f.DB.NewInsert().Model(&pr).Exec(ctx); err != nil {
			return resp, fmt.Errorf("propose relation %s -> %s: %w", r.Source, r.Target, err)
		}
		resp.RelationsProposed++
	}
	return resp, nil
}

// syncEmbedding best-effort 把實體描述的 embedding 寫進 entity_embeddings；
// 回傳是否成功（失敗不阻斷圖譜寫入，但呼叫端要知道 hybrid_search 找不找得到）。
func (f *GraphFacade) syncEmbedding(ctx context.Context, name, description string) bool {
	if f.Embed == nil || f.Vectors == nil || !f.Vectors.Enabled() || description == "" {
		return false
	}
	vec, err := f.Embed.EmbedOne(ctx, description)
	if err != nil {
		return false
	}
	return f.Vectors.Upsert(ctx, name, vec) == nil
}

func (f *GraphFacade) getProposed(ctx context.Context, id string) (*models.ProposedRelation, error) {
	var pr models.ProposedRelation
	err := f.DB.NewSelect().Model(&pr).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get proposed relation: %w", err)
	}
	return &pr, nil
}

func (f *GraphFacade) setProposedStatus(ctx context.Context, pr *models.ProposedRelation, status string) (*models.ProposedRelation, error) {
	pr.Status = status
	if _, err := f.DB.NewUpdate().Model(pr).Column("status").WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("update proposed relation status: %w", err)
	}
	return pr, nil
}
