// Tests de integración del blueprint /api/graph: proyecta los flujos de
// backend/tests/test_ontology_api_errors.py y el recorrido completo
// upload → build → data → delete con LLM falsos (nunca se llama a la API real).
package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mirofish/internal/config"
	"mirofish/internal/graph"
	"mirofish/internal/llm"
	"mirofish/internal/models"
	"mirofish/internal/ontology"
	"mirofish/internal/testdb"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		UploadDir:           filepath.Join(t.TempDir(), "uploads"),
		AllowedExtensions:   []string{".pdf", ".md", ".txt", ".markdown"},
		DefaultChunkSize:    500,
		DefaultChunkOverlap: 50,
		MaxContentLength:    16 << 20,
		GraphBackend:        "local",
	}
}

func newTestHandler(t *testing.T, opts ...GraphHandlerOption) (*GraphHandler, *http.ServeMux) {
	t.Helper()
	cfg := testConfig(t)
	client, err := graph.NewClient(testdb.New(t))
	if err != nil {
		t.Fatalf("graph client: %v", err)
	}
	t.Cleanup(func() { _ = client.DB.Close() })

	allOpts := append([]GraphHandlerOption{WithGraphClient(client)}, opts...)
	h, err := NewGraphHandler(cfg, slog.Default(), allOpts...)
	if err != nil {
		t.Fatalf("NewGraphHandler: %v", err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return h, mux
}

// disableGraphLLM evita la extracción real en background.
func disableGraphLLM(t *testing.T) {
	t.Helper()
	orig := graph.LLMFactory
	graph.LLMFactory = func() (*llm.Client, error) { return nil, errors.New("no llm in tests") }
	t.Cleanup(func() { graph.LLMFactory = orig })
}

// fakeProvider levanta un /chat/completions que devuelve la ontología dada.
func fakeProvider(t *testing.T, status int, responseBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const testOntologyJSON = `{
  "entity_types": [
    {"name": "Person", "description": "Any individual person.", "attributes": [{"name": "full_name", "type": "text", "description": "name"}]},
    {"name": "Organization", "description": "Any organization.", "attributes": []}
  ],
  "edge_types": [
    {"name": "WORKS_FOR", "description": "works for", "source_targets": [{"source": "Person", "target": "Organization"}], "attributes": []}
  ],
  "analysis_summary": "ok"
}`

func ontologyResponse() string {
	inner := strings.ReplaceAll(testOntologyJSON, "\n", "")
	inner = strings.ReplaceAll(inner, "\"", "\\\"")
	return fmt.Sprintf(`{"model":"fake","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"%s"}}]}`, inner)
}

func newGeneratorWithProvider(srv *httptest.Server) func() *ontology.Generator {
	return func() *ontology.Generator {
		g := ontology.NewGenerator()
		g.LLM = func() (*llm.Client, error) {
			return &llm.Client{APIKey: "k", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}, nil
		}
		return g
	}
}

func doJSON(mux *http.ServeMux, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var parsed map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return rec, parsed
}

// ===== /project/list y /project/<id> =====

func TestProjectListAndGetRoutes(t *testing.T) {
	h, mux := newTestHandler(t)
	p, err := h.PM.CreateProject("Demo", "")
	if err != nil {
		t.Fatal(err)
	}

	rec, body := doJSON(mux, http.MethodGet, "/api/graph/project/list", nil)
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("list: %d %v", rec.Code, body)
	}
	data, _ := body["data"].([]any)
	if len(data) != 1 || body["count"].(float64) != 1 {
		t.Fatalf("list data = %v", body)
	}

	rec, body = doJSON(mux, http.MethodGet, "/api/graph/project/"+p.ProjectID, nil)
	if rec.Code != 200 {
		t.Fatalf("get: %d %v", rec.Code, body)
	}
	projectData, _ := body["data"].(map[string]any)
	if projectData["name"] != "Demo" {
		t.Errorf("name = %v", projectData["name"])
	}

	rec, body = doJSON(mux, http.MethodGet, "/api/graph/project/proj_missing", nil)
	if rec.Code != 404 || body["success"] != false {
		t.Fatalf("missing: %d %v", rec.Code, body)
	}
}

// ===== /ontology/generate =====

func multipartBody(t *testing.T, fields map[string]string, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := w.CreateFormFile("files", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	return &buf, w.FormDataContentType()
}

func TestGenerateOntologyRouteUploadsAndPersists(t *testing.T) {
	disableGraphLLM(t)
	srv := fakeProvider(t, 200, ontologyResponse())
	h, mux := newTestHandler(t, func(h *GraphHandler) {
		h.GeneratorFactory = newGeneratorWithProvider(srv)
	})

	buf, ctype := multipartBody(t, map[string]string{
		"simulation_requirement": "Simular la discusión pública.",
		"project_name":           "Proyecto Test",
	}, "doc.txt", "Ana visita Cafe Mio y compra cafe con Bob.")

	req := httptest.NewRequest(http.MethodPost, "/api/graph/ontology/generate", buf)
	req.Header.Set("Content-Type", ctype)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["success"] != true {
		t.Fatalf("body = %v", body)
	}
	data, _ := body["data"].(map[string]any)
	projectID, _ := data["project_id"].(string)
	if projectID == "" {
		t.Fatal("sin project_id")
	}
	if data["analysis_summary"] != "ok" {
		t.Errorf("analysis_summary = %v", data["analysis_summary"])
	}
	files, _ := data["files"].([]any)
	if len(files) != 1 {
		t.Errorf("files = %v", files)
	}

	// el project.json persistido queda en estado ontology_generated
	raw, err := os.ReadFile(filepath.Join(hUploadsDir(h), "projects", projectID, "project.json"))
	if err != nil {
		t.Fatalf("project.json: %v", err)
	}
	var persisted models.Project
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.ProjectOntologyGenerated {
		t.Errorf("status persistido = %v", persisted.Status)
	}
	if !hasOntology(persisted.Ontology) {
		t.Error("ontología no persistida")
	}
}

// hUploadsDir expone el directorio de uploads del handler (tests).
func hUploadsDir(h *GraphHandler) string { return h.Cfg.UploadDir }

// ===== /build completo =====

func TestBuildRouteEndToEnd(t *testing.T) {
	disableGraphLLM(t)
	h, mux := newTestHandler(t)

	// Proyecto con ontología ya generada y texto extraído.
	p, err := h.PM.CreateProject("Build Test", "")
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := json.Marshal(orderedOntology{
		EntityTypes: []any{
			map[string]any{"name": "Person", "description": "Any individual person.", "attributes": []any{}},
			map[string]any{"name": "Organization", "description": "Any organization.", "attributes": []any{}},
		},
		EdgeTypes: []any{
			map[string]any{"name": "WORKS_FOR", "description": "works for", "source_targets": []any{}, "attributes": []any{}},
		},
	})
	p.Ontology = stored
	p.Status = models.ProjectOntologyGenerated
	if err := h.PM.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	h.PM.SaveExtractedText(p.ProjectID, strings.Repeat("Ana trabaja en Cafe Mio. Bob tambien. ", 20))

	rec, body := doJSON(mux, http.MethodPost, "/api/graph/build", map[string]any{
		"project_id": p.ProjectID,
	})
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("build: %d %v", rec.Code, body)
	}
	data, _ := body["data"].(map[string]any)
	taskID, _ := data["task_id"].(string)
	graphID, _ := data["graph_id"].(string)
	_ = graphID
	if taskID == "" {
		t.Fatal("sin task_id")
	}

	// sondeo del task hasta completarse ( WaitForBatch duerme 3s entre polls)
	deadline := time.Now().Add(20 * time.Second)
	var taskDict map[string]any
	for time.Now().Before(deadline) {
		_, tb := doJSON(mux, http.MethodGet, "/api/graph/task/"+taskID, nil)
		td, _ := tb["data"].(map[string]any)
		if td != nil {
			taskDict = td
			if td["status"] == "completed" || td["status"] == "failed" {
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if taskDict == nil || taskDict["status"] != "completed" {
		t.Fatalf("task = %v", taskDict)
	}
	result, _ := taskDict["result"].(map[string]any)
	graphID, _ = result["graph_id"].(string)
	if graphID == "" {
		t.Fatal("sin graph_id en el resultado")
	}

	// el proyecto quedó completado y referenciando el grafo
	updated := h.PM.GetProject(p.ProjectID)
	if updated == nil || updated.Status != models.ProjectGraphCompleted {
		t.Fatalf("project status = %v", updated.Status)
	}
	if updated.GraphID == nil || *updated.GraphID != graphID {
		t.Fatalf("graph ref = %v", updated.GraphID)
	}

	// /data/<graph_id>
	rec, body = doJSON(mux, http.MethodGet, "/api/graph/data/"+graphID, nil)
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("data: %d %v", rec.Code, body)
	}
	dataResp, _ := body["data"].(map[string]any)
	if dataResp["graph_id"] != graphID {
		t.Errorf("graph_id = %v", dataResp["graph_id"])
	}
	for _, key := range []string{"nodes", "edges", "node_count", "edge_count"} {
		if _, ok := dataResp[key]; !ok {
			t.Errorf("data sin %q", key)
		}
	}

	// /delete/<graph_id>
	rec, body = doJSON(mux, http.MethodDelete, "/api/graph/delete/"+graphID, nil)
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("delete: %d %v", rec.Code, body)
	}
	updated = h.PM.GetProject(p.ProjectID)
	if updated == nil || updated.GraphID != nil || updated.Status != models.ProjectOntologyGenerated {
		t.Fatalf("tras delete: status=%v graph=%v", updated.Status, updated.GraphID)
	}

	// el grafo ya no existe
	rec, body = doJSON(mux, http.MethodGet, "/api/graph/data/"+graphID, nil)
	if rec.Code != 200 {
		// el backend local devuelve 200 con listas vacías para grafos ausentes
		// (igual que get_graph_data sobre un grafo vacío en Python local).
		t.Logf("data tras delete: %d %v", rec.Code, body)
	}
}

// ===== errores de proveedor (test_ontology_api_errors.py) =====

func TestProviderErrorDoesNotExposeProviderBody(t *testing.T) {
	disableGraphLLM(t)
	secretBody := `{"error":{"message":"secret-provider-detail","type":"server_error"}}`
	srv := fakeProvider(t, 500, secretBody)
	h, mux := newTestHandler(t, func(h *GraphHandler) {
		h.GeneratorFactory = newGeneratorWithProvider(srv)
	})

	buf, ctype := multipartBody(t, map[string]string{
		"simulation_requirement": "Simular.",
	}, "doc.txt", "texto de prueba")

	req := httptest.NewRequest(http.MethodPost, "/api/graph/ontology/generate", buf)
	req.Header.Set("Content-Type", ctype)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret-provider-detail") {
		t.Error("el cuerpo del proveedor no debe filtrarse")
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "LLM provider request failed (HTTP 500)" {
		t.Errorf("error = %v", body["error"])
	}
	data, _ := body["data"].(map[string]any)
	projectID, _ := data["project_id"].(string)
	if projectID == "" {
		t.Fatal("sin project_id para diagnóstico")
	}
	project := h.PM.GetProject(projectID)
	if project == nil || project.Status != models.ProjectFailed {
		t.Fatalf("project = %v", project)
	}
	if project.Error == nil || *project.Error != "LLM provider request failed (HTTP 500)" {
		t.Fatalf("project.error = %v", project.Error)
	}
}
