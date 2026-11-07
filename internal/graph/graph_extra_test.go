// Port del resto de test_local_graph_backend.py: NotFoundError, node edges,
// episodios con barrera, proceso completo de batch con LLM fake y progreso.
package graph

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mirofish/internal/llm"
)

// Episode get + NotFoundError.
func TestEpisodeGetAndNotFound(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	ep, err := c.AddEpisode("g1", "text", "ep text", "2026-01-01T00:00:00+00:00", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ep.UUID == "" {
		t.Fatal("episode sin uuid")
	}
	e, err := c.EpisodeGet(ep.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if e.UUID != ep.UUID {
		t.Errorf("uuid %q != %q", e.UUID, ep.UUID)
	}
	if _, err := c.EpisodeGet("nonexistent"); err == nil {
		t.Fatal("esperado NotFoundError")
	} else if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("tipo de error = %T", err)
	}
}

// B9: node.get_edges funciona con graph_id (antes AttributeError latente).
func TestNodeLevelEdgesCompletion(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	a, _ := c.DB.AddNode("g1", "A", []string{"Entity"}, "", nil)
	b, _ := c.DB.AddNode("g1", "B", []string{"Entity"}, "", nil)
	if _, err := c.DB.AddEdge("g1", a, b, "KNOWS", "A knows B", nil); err != nil {
		t.Fatal(err)
	}
	nodes, _ := c.DB.GetAllNodes("g1")
	r, err := c.NodeGetEdges(nodes[0].UUID, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Edges) != 1 {
		t.Errorf("edges = %d", len(r.Edges))
	}
	// sin graph_id → error explícito (no AttributeError)
	if _, err := c.NodeGetEdges(nodes[0].UUID, ""); err == nil {
		t.Error("esperado error graph_id requerido")
	}
}

// Not-found de nodos/batches.
func TestNotFoundPaths(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	if _, err := c.NodeGet("node_inexistente"); err == nil {
		t.Error("node: esperado NotFoundError")
	}
	if _, err := c.BatchGet("batch_inexistente"); err == nil {
		t.Error("batch: esperado NotFoundError")
	}
	if _, err := c.GetGraph("g_none"); err == nil {
		t.Error("graph: esperado NotFoundError")
	}
}

// Regresión: un cursor mayor que el número de items hacía items[cursor:] y
// entraba en pánico (slice bounds out of range).
func TestPaginationCursorBeyondEndDoesNotPanic(t *testing.T) {
	disableLLM(t)
	c := testClient(t)
	batch, err := c.BatchCreate(map[string]any{"graph_id": "g1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.BatchAdd(batch.BatchID, []BatchItemInput{
		{GraphID: "g1", Data: "uno", DataType: "text"},
	}); err != nil {
		t.Fatal(err)
	}

	items, err := c.BatchListItems(batch.BatchID, 100, 500)
	if err != nil {
		t.Fatalf("BatchListItems: %v", err)
	}
	if len(items.Items) != 0 {
		t.Errorf("items = %d, want 0", len(items.Items))
	}

	batches, err := c.BatchList(10, 500)
	if err != nil {
		t.Fatalf("BatchList: %v", err)
	}
	if len(batches.Batches) != 0 {
		t.Errorf("batches = %d, want 0", len(batches.Batches))
	}
}

// fakeLLMProvider — proveedor OpenAI-compat que devuelve extracción fija.
func fakeLLMProvider(t *testing.T, entitiesJSON, edgesJSON string) {
	t.Helper()
	before := LLMFactory
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		body := `{"model":"fake","choices":[{"finish_reason":"stop","message":
			{"role":"assistant","content":` + jsonEscape(`{"entities": `+entitiesJSON+`, "edges": `+edgesJSON+`}`) + `}}]}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	LLMFactory = func() (*llm.Client, error) {
		return &llm.Client{APIKey: "k", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}, nil
	}
	t.Cleanup(func() {
		LLMFactory = before
		srv.Close()
	})
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Extracción end-to-end con LLM fake: episodio → background goroutine →
// nodos/aristas persistidos + processed=1 (barrera liberada).
func TestEpisodeExtractionBackgroundPersistsEntities(t *testing.T) {
	fakeLLMProvider(t,
		`[{"name":"Ana","type":"Person","summary":"propietaria","attributes":{}},
		  {"name":"Cafe Mio","type":"Place","summary":"","attributes":{}}]`,
		`[{"source":"Ana","target":"Cafe Mio","name":"VISITS","fact":"Ana visita Cafe Mio"}]`,
	)
	c := testClient(t)
	if err := c.SetOntologyCanonica("g1",
		map[string]any{"Person": map[string]any{"name": "Person", "description": "people"}},
		map[string]any{}); err != nil {
		t.Fatal(err)
	}
	ep, err := c.AddEpisode("g1", "text", "Ana visita Cafe Mio y compra cafe.", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Processed {
		t.Log("processed ya true: la extracción terminó muy rápido")
	}
	// la barrera (b8/A6): processed PASA a true siempre tras terminar
	waitProcessed(t, c, ep.UUID, 3)

	nodes, _ := c.DB.GetAllNodes("g1")
	names := map[string]bool{}
	for _, n := range nodes {
		names[n.Name] = true
	}
	if !names["Ana"] || !names["Cafe Mio"] {
		t.Errorf("nodos persistidos incorrectos: %v", names)
	}
	edges, _ := c.DB.GetAllEdges("g1")
	if len(edges) != 1 || edges[0].Fact != "Ana visita Cafe Mio" {
		t.Errorf("edges = %+v", edges)
	}
}

// Progreso incremental de sub-lotes (A6) con extracción fake por chunk.
func TestBatchProgressWithSublotRelease(t *testing.T) {
	calls := 0
	fakeLLMProviderCounting(t, &calls,
		`[{"name":"E1","type":"Entity","summary":"","attributes":{}}]`, `[]`)
	c := testClient(t)
	// 8 textos > CHUNK_SIZE 6 → dos sub-lotes
	items := []BatchItemInput{}
	for i := 0; i < 8; i++ {
		items = append(items, BatchItemInput{GraphID: "g1", Data: "texto " + strings.Repeat("x", 20) + itoa(i), DataType: "text"})
	}
	batch, _ := c.BatchCreate(map[string]any{"graph_id": "g1"})
	if _, err := c.BatchAdd(batch.BatchID, items); err != nil {
		t.Fatal(err)
	}
	// procesa síncronamente (el Python dispara y el test espera con poll)
	c.DB.FinalizeBatch(batch.BatchID)

	summary, err := c.BatchGet(batch.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Status != "succeeded" {
		t.Errorf("status = %q", summary.Status)
	}
	// al finalizar: 8/8 liberados → percent 100
	if summary.Progress.SucceededItems != 8 || summary.Progress.PercentComplete < 100 {
		t.Errorf("progress = %+v", summary.Progress)
	}
}

func fakeLLMProviderCounting(t *testing.T, counter *int, entitiesJSON, edgesJSON string) {
	t.Helper()
	before := LLMFactory
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		*counter++
		body := `{"model":"fake","choices":[{"finish_reason":"stop","message":
			{"role":"assistant","content":` + jsonEscape(`{"entities": `+entitiesJSON+`, "edges": `+edgesJSON+`}`) + `}}]}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	LLMFactory = func() (*llm.Client, error) {
		return &llm.Client{APIKey: "k", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}, nil
	}
	t.Cleanup(func() {
		LLMFactory = before
		srv.Close()
	})
}

// waitProcessed sondea la barrera del episodio. El bucle original no dormía:
// 300 consultas a SQLite terminaban en pocos milisegundos, antes de que la
// goroutine de extracción (que hace una llamada HTTP) pudiera marcar
// processed=1, así que el test fallaba de forma intermitente.
func waitProcessed(t *testing.T, c *Client, epUUID string, seconds int) {
	t.Helper()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for time.Now().Before(deadline) {
		ep, err := c.DB.GetEpisode(epUUID)
		if err != nil {
			t.Fatal(err)
		}
		if ep != nil && ep.Processed {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("barrera de episodio nunca liberada (processed=1)")
}
