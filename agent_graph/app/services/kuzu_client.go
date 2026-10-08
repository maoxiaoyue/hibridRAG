// kuzu_client.go — Kùzu-backed graph operations, drop-in replacement for
// AGEClient at the graph_facade level. Method signatures and return shapes
// mirror age_client.go so graph_facade never sees the backend difference.
//
// PoC scope: 5 operations (UpsertEntity, UpsertRelation, SearchEntities,
// GetLocalContext, ExecuteCypher) + EnsureSchema. Kùzu is embedded (no server),
// so this client owns the *kuzu.Database handle for the whole process.
//
// Concurrency: *kuzu.Database is safe for multiple *kuzu.Connection to be
// opened in parallel, but each *kuzu.Connection is single-threaded. We open a
// fresh connection per call and close it at the end. Simpler than pooling and
// avoids the "one shared conn = one query at a time" bottleneck.
package services

import (
	"context"
	"fmt"
	"time"

	"github.com/kuzudb/go-kuzu"

	"agent_graph/app/models"
)

// KuzuClient is the Kùzu-backed counterpart of AGEClient. enabled=false
// makes every operation return ErrGraphDisabled without touching the DB.
type KuzuClient struct {
	db      *kuzu.Database
	enabled bool
}

// NewKuzuClient opens (or creates) a Kùzu database at dbPath. Pass an empty
// path to get an in-memory database (test-only — data doesn't survive restart).
func NewKuzuClient(dbPath string) (*KuzuClient, error) {
	cfg := kuzu.DefaultSystemConfig()
	var db *kuzu.Database
	var err error
	if dbPath == "" {
		db, err = kuzu.OpenInMemoryDatabase(cfg)
	} else {
		db, err = kuzu.OpenDatabase(dbPath, cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("open kuzu database: %w", err)
	}
	return &KuzuClient{db: db, enabled: true}, nil
}

// Close releases the underlying Kùzu database. Safe to call multiple times.
func (c *KuzuClient) Close() error {
	if c.db != nil {
		c.db.Close()
		c.db = nil
	}
	return nil
}

func (c *KuzuClient) Enabled() bool { return c.enabled }

// exec runs one cypher statement with parameter binding, returning each row
// as column-name → parsed Go value (Node / Relationship / scalar).
// cols is the ordered list of RETURN column names; must match the query.
func (c *KuzuClient) exec(ctx context.Context, cypher string, params map[string]any, cols []string) ([]map[string]any, error) {
	if !c.enabled {
		return nil, ErrGraphDisabled
	}
	// ctx is honored at the Go layer only — Kùzu Go binding doesn't take a
	// ctx yet. Check cancellation before we spend the syscall on a fresh conn.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	conn, err := kuzu.OpenConnection(c.db)
	if err != nil {
		return nil, fmt.Errorf("open connection: %w", err)
	}
	defer conn.Close()

	var result *kuzu.QueryResult
	if len(params) == 0 {
		result, err = conn.Query(cypher)
	} else {
		stmt, perr := conn.Prepare(cypher)
		if perr != nil {
			return nil, fmt.Errorf("prepare cypher: %w", perr)
		}
		defer stmt.Close()
		result, err = conn.Execute(stmt, params)
	}
	if err != nil {
		return nil, fmt.Errorf("cypher exec: %w", err)
	}
	defer result.Close()

	var out []map[string]any
	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("next tuple: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, name := range cols {
			v, verr := tuple.GetValue(uint64(i))
			if verr != nil {
				tuple.Close()
				return nil, fmt.Errorf("get column %s: %w", name, verr)
			}
			row[name] = v
		}
		tuple.Close()
		out = append(out, row)
	}
	return out, nil
}

// stringProp 從屬性 map 取字串值；缺鍵/非字串回空字串。
//（原在 agtype.go；AGE 路徑移除後搬到唯一使用者這裡。）
func stringProp(props map[string]any, key string) string {
	v, ok := props[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// nodeToEntity converts a Kùzu Node into the domain-level EntityResult.
func nodeToEntity(n kuzu.Node) models.EntityResult {
	return models.EntityResult{
		Name:        stringProp(n.Properties, "name"),
		Category:    stringProp(n.Properties, "category"),
		Description: stringProp(n.Properties, "description"),
		SourceFile:  stringProp(n.Properties, "source_file"),
	}
}

// UpsertEntity creates or updates the Entity node keyed by name.
func (c *KuzuClient) UpsertEntity(ctx context.Context, e models.EntityResult) error {
	_, err := c.exec(ctx, `
		MERGE (n:Entity {name: $name})
		SET n.category = $category,
		    n.description = $description,
		    n.source_file = $source_file,
		    n.updated_at = $updated_at
		RETURN n.name
	`, map[string]any{
		"name":        e.Name,
		"category":    e.Category,
		"description": e.Description,
		"source_file": e.SourceFile,
		"updated_at":  time.Now().UTC(),
	}, []string{"name"})
	return err
}

// UpsertRelation creates or updates a RELATES_TO edge between two entities,
// auto-creating missing endpoints as stub nodes. relType is stored as an edge
// property (not a dynamic edge label) — mirrors the AGE version so the on-disk
// data model stays identical.
func (c *KuzuClient) UpsertRelation(ctx context.Context, source, target, relType string) error {
	_, err := c.exec(ctx, `
		MERGE (a:Entity {name: $source})
		MERGE (b:Entity {name: $target})
		MERGE (a)-[r:RELATES_TO {type: $rel_type}]->(b)
		RETURN r.type
	`, map[string]any{
		"source":   source,
		"target":   target,
		"rel_type": relType,
	}, []string{"type"})
	return err
}

// SearchEntities does a case-insensitive substring match on name/description.
func (c *KuzuClient) SearchEntities(ctx context.Context, query string, limit int) ([]models.EntityResult, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := c.exec(ctx, `
		MATCH (n:Entity)
		WHERE lower(n.name) CONTAINS lower($query)
		   OR lower(n.description) CONTAINS lower($query)
		RETURN n
		LIMIT $limit
	`, map[string]any{"query": query, "limit": int64(limit)}, []string{"n"})
	if err != nil {
		return nil, err
	}
	out := make([]models.EntityResult, 0, len(rows))
	for _, row := range rows {
		n, ok := row["n"].(kuzu.Node)
		if !ok {
			continue
		}
		out = append(out, nodeToEntity(n))
	}
	return out, nil
}

// GetLocalContext returns an entity's immediate relations plus (when hops>1)
// the wider set of reachable entity names. Structure mirrors AGEClient.
func (c *KuzuClient) GetLocalContext(ctx context.Context, entityName string, hops, limit int) (*models.LocalContextResponse, error) {
	if hops <= 0 {
		hops = 1
	}
	if hops > 4 {
		hops = 4
	}
	if limit <= 0 {
		limit = 20
	}

	centerRows, err := c.exec(ctx,
		`MATCH (center:Entity {name: $name}) RETURN center LIMIT 1`,
		map[string]any{"name": entityName}, []string{"center"})
	if err != nil {
		return nil, err
	}
	if len(centerRows) == 0 {
		return nil, ErrNotFound
	}
	centerNode, _ := centerRows[0]["center"].(kuzu.Node)
	resp := &models.LocalContextResponse{Center: nodeToEntity(centerNode)}

	directRows, err := c.exec(ctx, `
		MATCH (center:Entity {name: $name})-[r:RELATES_TO]-(other:Entity)
		RETURN r, other
		LIMIT $limit
	`, map[string]any{"name": entityName, "limit": int64(limit)}, []string{"r", "other"})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, row := range directRows {
		other, ok := row["other"].(kuzu.Node)
		if !ok {
			continue
		}
		entity := nodeToEntity(other)
		if !seen[entity.Name] {
			resp.Neighbors = append(resp.Neighbors, entity)
			seen[entity.Name] = true
		}
		if edge, ok := row["r"].(kuzu.Relationship); ok {
			resp.Relations = append(resp.Relations, models.RelationResult{
				Source: entityName,
				Target: entity.Name,
				Type:   stringProp(edge.Properties, "type"),
			})
		}
	}

	if hops > 1 {
		// Kùzu supports variable-length paths with the same *min..max syntax as
		// standard openCypher, so hops is interpolated via %d (numeric, bounded).
		cypher := fmt.Sprintf(`
			MATCH (center:Entity {name: $name})-[:RELATES_TO*2..%d]-(far:Entity)
			RETURN DISTINCT far
			LIMIT $limit
		`, hops)
		farRows, err := c.exec(ctx, cypher,
			map[string]any{"name": entityName, "limit": int64(limit)}, []string{"far"})
		if err != nil {
			return nil, err
		}
		for _, row := range farRows {
			far, ok := row["far"].(kuzu.Node)
			if !ok {
				continue
			}
			entity := nodeToEntity(far)
			if !seen[entity.Name] {
				resp.Neighbors = append(resp.Neighbors, entity)
				seen[entity.Name] = true
			}
		}
	}
	return resp, nil
}

// ListEntities dumps every entity node. 供 vault 鏡像匯出與 degree 計算——
// 個人知識庫規模（數千節點）一次撈全量在 embedded DB 上是微秒級操作。
func (c *KuzuClient) ListEntities(ctx context.Context) ([]models.EntityResult, error) {
	rows, err := c.exec(ctx, `MATCH (n:Entity) RETURN n`, nil, []string{"n"})
	if err != nil {
		return nil, err
	}
	out := make([]models.EntityResult, 0, len(rows))
	for _, row := range rows {
		n, ok := row["n"].(kuzu.Node)
		if !ok {
			continue
		}
		out = append(out, nodeToEntity(n))
	}
	return out, nil
}

// ListRelations dumps every relation with its true direction（GetLocalContext
// 的無向遍歷會把 center 一律當 Source，方向不可信；鏡像匯出需要真實方向）。
func (c *KuzuClient) ListRelations(ctx context.Context) ([]models.RelationResult, error) {
	rows, err := c.exec(ctx, `
		MATCH (a:Entity)-[r:RELATES_TO]->(b:Entity)
		RETURN a.name AS source, b.name AS target, r.type AS type
	`, nil, []string{"source", "target", "type"})
	if err != nil {
		return nil, err
	}
	out := make([]models.RelationResult, 0, len(rows))
	for _, row := range rows {
		src, _ := row["source"].(string)
		tgt, _ := row["target"].(string)
		typ, _ := row["type"].(string)
		if src == "" || tgt == "" {
			continue
		}
		out = append(out, models.RelationResult{Source: src, Target: tgt, Type: typ})
	}
	return out, nil
}

// ExecuteCypher is the low-level escape hatch. Node/Relationship values are
// flattened to the same {id, label, properties, ...} shape AGEClient produces,
// so tool callers see a stable JSON contract across backends.
func (c *KuzuClient) ExecuteCypher(ctx context.Context, cypher string, params map[string]any, cols []string) ([]map[string]any, error) {
	rows, err := c.exec(ctx, cypher, params, cols)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		flat := make(map[string]any, len(row))
		for k, v := range row {
			switch t := v.(type) {
			case kuzu.Node:
				flat[k] = map[string]any{
					"id":         formatInternalID(t.ID),
					"label":      t.Label,
					"properties": t.Properties,
				}
			case kuzu.Relationship:
				flat[k] = map[string]any{
					"id":         formatInternalID(t.ID),
					"label":      t.Label,
					"start_id":   formatInternalID(t.SourceID),
					"end_id":     formatInternalID(t.DestinationID),
					"properties": t.Properties,
				}
			default:
				flat[k] = t
			}
		}
		out = append(out, flat)
	}
	return out, nil
}

// formatInternalID stringifies Kùzu's (tableID, offset) tuple. AGE returns a
// single int64; we keep a stable string so downstream JSON marshalling is
// well-defined even when tableID or offset overflow JS Number precision.
func formatInternalID(id kuzu.InternalID) string {
	return fmt.Sprintf("%d:%d", id.TableID, id.Offset)
}
