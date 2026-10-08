// kuzu_poc_test.go — end-to-end round-trip PoC over the Kùzu-backed graph.
// Verifies build (CGO + kuzu shared lib) and the 5 core operations against a
// throwaway on-disk database (in-memory would skip persistence which is one
// thing we want to confirm works).
//
// Run with:  go test -run TestKuzuPoC -v ./app/services/
package services

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"agent_graph/app/models"
)

func TestKuzuPoC(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "poc.kz")

	client, err := NewKuzuClient(dbPath)
	if err != nil {
		t.Fatalf("open kuzu: %v", err)
	}
	defer client.Close()

	if !client.Enabled() {
		t.Fatal("client should be enabled after successful open")
	}

	ctx := context.Background()
	if err := EnsureKuzuSchema(ctx, client, 8); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	// Idempotency: running twice must not fail.
	if err := EnsureKuzuSchema(ctx, client, 8); err != nil {
		t.Fatalf("ensure schema (second run): %v", err)
	}

	if err := EnsureKuzuVectorSchema(ctx, client, "entity_embedding_idx"); err != nil {
		t.Fatalf("ensure vector schema: %v", err)
	}
	if err := EnsureKuzuVectorSchema(ctx, client, "entity_embedding_idx"); err != nil {
		t.Fatalf("ensure vector schema (second run): %v", err)
	}

	// ---- upserts ---------------------------------------------------------
	entities := []models.EntityResult{
		{Name: "Alice", Category: "Person", Description: "Founder of Acme", SourceFile: "notes/alice.md"},
		{Name: "Bob", Category: "Person", Description: "CTO at Acme", SourceFile: "notes/bob.md"},
		{Name: "Acme", Category: "Company", Description: "A company founded in 2020", SourceFile: "notes/acme.md"},
	}
	for _, e := range entities {
		if err := client.UpsertEntity(ctx, e); err != nil {
			t.Fatalf("upsert %q: %v", e.Name, err)
		}
	}

	// Idempotent upsert: same key, updated description.
	if err := client.UpsertEntity(ctx, models.EntityResult{
		Name: "Alice", Category: "Person",
		Description: "Founder and CEO of Acme", SourceFile: "notes/alice.md",
	}); err != nil {
		t.Fatalf("re-upsert Alice: %v", err)
	}

	relations := []struct{ src, tgt, typ string }{
		{"Alice", "Acme", "founded"},
		{"Bob", "Acme", "works_at"},
	}
	for _, r := range relations {
		if err := client.UpsertRelation(ctx, r.src, r.tgt, r.typ); err != nil {
			t.Fatalf("upsert relation %s→%s: %v", r.src, r.tgt, err)
		}
	}

	// ---- SearchEntities --------------------------------------------------
	// Substring "founder" should match the (updated) Alice description.
	hits, err := client.SearchEntities(ctx, "founder", 5)
	if err != nil {
		t.Fatalf("search entities: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit for 'founder', got 0")
	}
	foundAlice := false
	for _, h := range hits {
		if h.Name == "Alice" {
			foundAlice = true
			if h.Description == "" {
				t.Errorf("Alice hit missing description")
			}
		}
	}
	if !foundAlice {
		t.Errorf("expected Alice in search results, got: %+v", hits)
	}
	t.Logf("SearchEntities('founder') → %d hits", len(hits))

	// ---- GetLocalContext (1 hop) -----------------------------------------
	ctxResp, err := client.GetLocalContext(ctx, "Acme", 1, 20)
	if err != nil {
		t.Fatalf("GetLocalContext hop=1: %v", err)
	}
	if ctxResp.Center.Name != "Acme" {
		t.Errorf("center should be Acme, got %q", ctxResp.Center.Name)
	}
	// Acme has two 1-hop neighbors: Alice (founded) and Bob (works_at).
	if len(ctxResp.Neighbors) < 2 {
		t.Errorf("expected ≥2 neighbors for Acme, got %d: %+v", len(ctxResp.Neighbors), ctxResp.Neighbors)
	}
	if len(ctxResp.Relations) < 2 {
		t.Errorf("expected ≥2 relations for Acme, got %d: %+v", len(ctxResp.Relations), ctxResp.Relations)
	}
	relTypes := map[string]bool{}
	for _, r := range ctxResp.Relations {
		relTypes[r.Type] = true
	}
	if !relTypes["founded"] || !relTypes["works_at"] {
		t.Errorf("expected relation types {founded, works_at}, got %v", relTypes)
	}
	t.Logf("GetLocalContext('Acme', hop=1) → center=%s, %d neighbors, %d relations",
		ctxResp.Center.Name, len(ctxResp.Neighbors), len(ctxResp.Relations))

	// ---- GetLocalContext (2 hop) — Alice reaches Bob via Acme -----------
	ctxResp2, err := client.GetLocalContext(ctx, "Alice", 2, 20)
	if err != nil {
		t.Fatalf("GetLocalContext hop=2: %v", err)
	}
	reachable := map[string]bool{}
	for _, n := range ctxResp2.Neighbors {
		reachable[n.Name] = true
	}
	if !reachable["Bob"] {
		t.Errorf("expected Bob reachable from Alice within 2 hops, got neighbors: %v", reachable)
	}
	t.Logf("GetLocalContext('Alice', hop=2) → neighbors=%v", reachable)

	// ---- GetLocalContext (miss) ------------------------------------------
	if _, err := client.GetLocalContext(ctx, "NoSuchThing", 1, 5); err != ErrNotFound {
		t.Errorf("expected ErrNotFound for missing center, got %v", err)
	}

	// ---- ExecuteCypher (Node flattening) ---------------------------------
	raw, err := client.ExecuteCypher(ctx,
		`MATCH (n:Entity) WHERE n.name = $name RETURN n`,
		map[string]any{"name": "Bob"},
		[]string{"n"})
	if err != nil {
		t.Fatalf("ExecuteCypher: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected 1 row for Bob, got %d", len(raw))
	}
	nodeMap, ok := raw[0]["n"].(map[string]any)
	if !ok {
		t.Fatalf("expected n to be flattened map, got %T", raw[0]["n"])
	}
	for _, want := range []string{"id", "label", "properties"} {
		if _, ok := nodeMap[want]; !ok {
			t.Errorf("flattened node missing key %q", want)
		}
	}
	// The flat map must survive JSON encoding — this is the contract callers
	// rely on when the ExecuteCypher result is shipped through the gRPC tool.
	if _, err := json.Marshal(raw); err != nil {
		t.Errorf("flattened result not JSON-safe: %v", err)
	}
	t.Logf("ExecuteCypher(Bob) → %+v", nodeMap)

	// ---- ExecuteCypher (Relationship flattening) -------------------------
	rawRel, err := client.ExecuteCypher(ctx,
		`MATCH (a:Entity {name: $name})-[r:RELATES_TO]->(b:Entity) RETURN r, b.name`,
		map[string]any{"name": "Alice"},
		[]string{"r", "target"})
	if err != nil {
		t.Fatalf("ExecuteCypher rel: %v", err)
	}
	if len(rawRel) == 0 {
		t.Fatal("expected at least one outgoing relation from Alice")
	}
	relMap, ok := rawRel[0]["r"].(map[string]any)
	if !ok {
		t.Fatalf("expected r to be flattened map, got %T", rawRel[0]["r"])
	}
	for _, want := range []string{"id", "label", "start_id", "end_id", "properties"} {
		if _, ok := relMap[want]; !ok {
			t.Errorf("flattened rel missing key %q", want)
		}
	}
	t.Logf("ExecuteCypher(Alice→) → rel=%+v target=%v", relMap, rawRel[0]["target"])

	// ---- KuzuVectorStore: upsert + ANN search --------------------------
	vs := NewKuzuVectorStore(client, true)
	if !vs.Enabled() {
		t.Fatal("vector store should be enabled")
	}
	// 8-dim toy vectors — three well-separated points; queries land near one.
	vectors := map[string][]float32{
		"Alice": {1, 0, 0, 0, 0, 0, 0, 0},
		"Bob":   {0, 1, 0, 0, 0, 0, 0, 0},
		"Acme":  {0, 0, 1, 0, 0, 0, 0, 0},
	}
	for name, v := range vectors {
		if err := vs.Upsert(ctx, name, v); err != nil {
			t.Fatalf("vector upsert %q: %v", name, err)
		}
	}
	// A query vector very close to Bob's should rank Bob first.
	vhits, err := vs.SearchSimilar(ctx, []float32{0.05, 0.95, 0.05, 0, 0, 0, 0, 0}, 3)
	if err != nil {
		t.Fatalf("vector search: %v", err)
	}
	if len(vhits) == 0 {
		t.Fatal("expected ≥1 vector hit, got 0")
	}
	if vhits[0].EntityName != "Bob" {
		t.Errorf("expected top hit=Bob, got %q (score=%v)", vhits[0].EntityName, vhits[0].Score)
	}
	if vhits[0].Score <= 0.5 {
		t.Errorf("expected Bob score > 0.5 (near-identical), got %v", vhits[0].Score)
	}
	t.Logf("SearchSimilar(nearBob) → %+v", vhits)
}
