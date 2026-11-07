// Port de los tests de batches/búsqueda de test_local_graph_backend.py.
package graph

import (
	"testing"
)

// A3: batch.get debe exponer .status/.progress con atributos.
func TestBatchStatusAndProgressAttributes(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	batch, err := c.BatchCreate(map[string]any{"graph_id": "g1"})
	if err != nil {
		t.Fatal(err)
	}
	if batch.BatchID == "" {
		t.Fatal("batch_id vacío")
	}
	summary, err := c.BatchGet(batch.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]bool{"draft": true, "processing": true, "succeeded": true, "partial": true}
	if !valid[summary.Status] {
		t.Errorf("status = %q", summary.Status)
	}
}

// A4: list_items debe exponer .items/.next_cursor (no .data).
func TestListItemsExposesItemsAndCursor(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	batch, _ := c.BatchCreate(map[string]any{"graph_id": "g1"})
	items := []BatchItemInput{}
	for i := 0; i < 3; i++ {
		items = append(items, BatchItemInput{
			GraphID: "g1", Data: "texo_" + itoa(i), DataType: "text",
			SourceDescription: "t",
			Metadata:          map[string]any{"chunk_index": float64(i), "chunk_sha256": "x"},
		})
	}
	if _, err := c.BatchAdd(batch.BatchID, items); err != nil {
		t.Fatal(err)
	}
	// _finalize_batch síncrono (el Python lo llama directo en el test)
	c.DB.FinalizeBatch(batch.BatchID)
	page, err := c.BatchListItems(batch.BatchID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items = %d", len(page.Items))
	}
	if page.NextCursor != nil {
		t.Errorf("next_cursor = %v", *page.NextCursor)
	}
	first := page.Items[0]
	if first.EpisodeUUID == "" || first.Status != "succeeded" || first.SequenceIndex != 0 {
		t.Errorf("first = %+v", first)
	}
}

// B1: paginación >100 devuelve header zep-next-cursor.
func TestPaginationNextCursorHeader(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	for i := 0; i < 150; i++ {
		if _, err := c.DB.AddNode("g1", "N"+itoa(i), []string{"Entity"}, "s", nil); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := c.NodeGetByGraphID("g1", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 100 || resp.NextCursor != "100" {
		t.Fatalf("page1: %d nodes, cursor=%q", len(resp.Data), resp.NextCursor)
	}
	resp2, err := c.NodeGetByGraphID("g1", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp2.Data) != 50 || resp2.NextCursor != "" {
		t.Fatalf("page2: %d nodes, cursor=%q", len(resp2.Data), resp2.NextCursor)
	}
}

// B5: query con metacaracteres FTS no crashea.
func TestSearchSanitizedQueryNoCrash(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	alice, _ := c.DB.AddNode("g1", "Alice", []string{"Entity", "Person"}, "Directora", nil)
	bob, _ := c.DB.AddNode("g1", "Bob", []string{"Entity", "Person"}, "", nil)
	if _, err := c.DB.AddEdge("g1", bob, alice, "KNOWS", "Bob conoce a Alice", nil); err != nil {
		t.Fatal(err)
	}
	r := c.Search("g1", `texto con "comillas" y (paréntesis)`, 5, "edges")
	if r.Edges == nil || r.Nodes == nil {
		t.Error("resultado nil")
	}
}

// B4: queries CJK fallback a scan con puntuación.
func TestCJKSearchUsesScan(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	z3, _ := c.DB.AddNode("g1", "张三", []string{"Entity", "Person"}, "estudiante", nil)
	teacher, _ := c.DB.AddNode("g1", "王老师", []string{"Entity", "Person"}, "profesor", nil)
	_ = z3
	if _, err := c.DB.AddEdge("g1", z3, teacher, "LIKES", "张三点赞了王老师发布的帖子", nil); err != nil {
		t.Fatal(err)
	}
	r := c.Search("g1", "王老师", 5, "edges")
	if len(r.Edges) < 1 {
		t.Fatalf("CJK scan edges = %d", len(r.Edges))
	}
}

// B8: dedup por (graph_id, name); repetido retorna uuid existente y enriquece.
func TestNodeDedupByName(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	u1, _ := c.DB.AddNode("g1", "Alice", []string{"Entity"}, "", nil)
	u2, _ := c.DB.AddNode("g1", "Alice", []string{"Entity"}, "resumen nuevo", nil)
	if u1 != u2 {
		t.Errorf("uuids %q != %q", u1, u2)
	}
	node, _ := c.DB.GetNode(u1)
	if node == nil || node.Summary != "resumen nuevo" {
		t.Fatalf("summary = %+v", node)
	}
}

// B2: batch.list debe exponer .batches/.next_cursor compatible con la
// búsqueda por operation_id.
func TestBatchListCompatibleWithFindByOperationID(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	batch, _ := c.BatchCreate(map[string]any{"graph_id": "g1", "mirofish_operation_id": "op_1"})
	page, err := c.BatchList(100, 0)
	if err != nil {
		t.Fatal(err)
	}
	matches := 0
	for _, b := range page.Batches {
		if op, ok := b.Metadata["mirofish_operation_id"]; ok && op == "op_1" && b.BatchID == batch.BatchID {
			matches++
		}
	}
	if matches != 1 {
		t.Errorf("matches = %d", matches)
	}
}

// A2/B3: set_ontology canónico (dicts) exponiendo nombres para el extractor.
func TestSetOntologyCanonical(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	err := c.SetOntologyCanonica("g1",
		map[string]any{"Persona": map[string]any{"name": "Persona", "description": "A person", "attributes": []any{}}},
		map[string]any{"CONOCE_A": map[string]any{"name": "CONOCE_A", "description": "knows", "attributes": []any{}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	onto := c.DB.GetOntology("g1")
	ents, _ := onto["entity_types"].([]any)
	edges, _ := onto["edge_types"].([]any)
	var hasPersona, hasConoce bool
	for _, e := range ents {
		if m, ok := e.(map[string]any); ok && fmtString(m["name"]) == "Persona" {
			hasPersona = len(fmtString(m["description"])) > 0
		}
	}
	for _, e := range edges {
		if m, ok := e.(map[string]any); ok && fmtString(m["name"]) == "CONOCE_A" {
			hasConoce = true
		}
	}
	if !hasPersona || !hasConoce {
		t.Errorf("ontology = %v / %v", ents, edges)
	}
}

// B3: la ontología leída por el extractor tiene entity_types con name.
func TestOntologyReachesLocalExtractionFormat(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	if err := c.SetOntologyCanonica("g1",
		map[string]any{"Persona": map[string]any{"name": "Persona", "description": "A person"}},
		map[string]any{}); err != nil {
		t.Fatal(err)
	}
	onto := c.DB.GetOntology("g1")
	ents, _ := onto["entity_types"].([]any)
	if len(ents) == 0 || fmtString(ents[0].(map[string]any)["name"]) != "Persona" {
		t.Errorf("entity_types = %v", ents)
	}
}

func fmtString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
