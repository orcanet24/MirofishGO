// Tests de integración del blueprint /api/report: generación asíncrona con
// LLM guionizada, capítulos servidos por polling, logs incrementales, chat,
// descarga, borrado y validaciones de estado.
package httpx

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mirofish/internal/config"
	"mirofish/internal/graph"
	"mirofish/internal/llm"
	"mirofish/internal/models"
	"mirofish/internal/report"
	"mirofish/internal/simx"
	"mirofish/internal/testdb"
)

// scriptedLLM responde según el prompt: índice JSON, sub-preguntas, secciones
// con llamadas a quick_search (3 antes de la respuesta final) y chat directo.
type scriptedLLM struct {
	mu    sync.Mutex
	calls int
}

func (s *scriptedLLM) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		var system, last string
		for _, m := range payload.Messages {
			if m.Role == "system" && system == "" {
				system = m.Content
			}
			last = m.Content
		}
		_ = last

		content := ""
		switch {
		case strings.Contains(system, "informes de predicción de futuro"):
			content = `{"title":"Informe de prueba","summary":"Resumen de prueba","sections":[{"title":"Hallazgos","description":"d"},{"title":"Riesgos","description":"d"}]}`
		case strings.Contains(system, "subconsultas") || strings.Contains(system, "descompone"):
			content = `{"sub_queries":["Ana","Cafe Mio"]}`
		case strings.Contains(system, "asistente de predicción de simulación"):
			content = "Respuesta del agente basada en el informe."
		default:
			s.mu.Lock()
			s.calls++
			n := s.calls
			s.mu.Unlock()
			if n%3 == 0 {
				content = "Final Answer: Contenido del capítulo con datos de la simulación. " +
					"Ana visita Cafe Mio y la discusión crece en la red.\n\n> \"Ana: el ERP cambia la operación\"\n\n" +
					"Este párrafo añade contexto suficiente para superar el mínimo de contenido exigido."
			} else {
				content = `<tool_call>{"name":"quick_search","parameters":{"query":"Ana"}}</tool_call>`
			}
		}

		body := map[string]any{
			"model": "fake",
			"choices": []any{map[string]any{
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": content},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
}

func newReportTestHandler(t *testing.T, srv *httptest.Server) (*ReportHandler, *http.ServeMux, *models.Project, *simx.SimulationState) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		BaseDir:             root,
		UploadDir:           filepath.Join(root, "uploads"),
		SimDataDir:          filepath.Join(root, "uploads", "simulations"),
		AllowedExtensions:   []string{".txt"},
		DefaultChunkSize:    500,
		DefaultChunkOverlap: 50,
		MaxContentLength:    16 << 20,
		GraphBackend:        "local",
	}
	client, err := graph.NewClient(testdb.New(t))
	if err != nil {
		t.Fatalf("graph client: %v", err)
	}
	t.Cleanup(func() { _ = client.DB.Close() })

	simManager := simx.NewManager(cfg.SimDataDir)
	h, err := NewReportHandler(cfg, slog.Default(),
		WithReportGraphClient(client),
		WithReportSimulationManager(simManager),
	)
	if err != nil {
		t.Fatalf("NewReportHandler: %v", err)
	}
	h.NewAgent = func(graphID, simulationID, requirement, userID, localeCode string) *report.Agent {
		agent := report.NewAgent(h.Reports, h.Tools, graphID, simulationID, requirement, userID, localeCode)
		agent.LLMFactory = func() (*llm.Client, error) {
			return &llm.Client{APIKey: "k", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}, nil
		}
		return agent
	}

	mux := http.NewServeMux()
	h.Register(mux)

	project, err := h.PM.CreateProject("Proyecto Report", "")
	if err != nil {
		t.Fatal(err)
	}
	requirement := "Simular el impacto del nuevo ERP."
	project.SimulationRequirement = &requirement
	graphID := "mirofish_report_test"
	if _, err := h.Client.CreateGraph(graphID, "Test", ""); err != nil {
		t.Fatal(err)
	}
	ana, err := h.Client.DB.AddNode(graphID, "Ana", []string{"Entity", "Person"}, "Contadora", nil)
	if err != nil {
		t.Fatal(err)
	}
	cafe, err := h.Client.DB.AddNode(graphID, "Cafe Mio", []string{"Entity", "Organization"}, "Cafetería", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Client.DB.AddEdge(graphID, ana, cafe, "WORKS_FOR", "Ana trabaja en Cafe Mio", nil); err != nil {
		t.Fatal(err)
	}
	project.GraphID = &graphID
	project.Status = models.ProjectGraphCompleted
	if err := h.PM.SaveProject(project); err != nil {
		t.Fatal(err)
	}

	state := simManager.CreateSimulation(project.ProjectID, graphID, true, true, nil)
	runState := map[string]any{"runner_status": "completed", "current_round": 10, "total_rounds": 10}
	raw, _ := json.MarshalIndent(runState, "", "  ")
	if err := os.WriteFile(filepath.Join(simManager.SimulationDir(state.SimulationID), "run_state.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return h, mux, project, state
}

func waitReportTask(t *testing.T, mux *http.ServeMux, taskID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		rec, body := doJSON(mux, http.MethodPost, "/api/report/generate/status", map[string]any{"task_id": taskID})
		if rec.Code != 200 {
			t.Fatalf("generate/status: %d %v", rec.Code, body)
		}
		data, _ := body["data"].(map[string]any)
		last = data
		if data["status"] == "completed" || data["status"] == "failed" {
			return data
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("la tarea de informe no terminó: %v", last)
	return nil
}

func TestReportGenerateValidations(t *testing.T) {
	srv := httptest.NewServer((&scriptedLLM{}).handler())
	t.Cleanup(srv.Close)
	h, mux, project, _ := newReportTestHandler(t, srv)

	rec, body := doJSON(mux, http.MethodPost, "/api/report/generate", map[string]any{})
	if rec.Code != http.StatusBadRequest || body["success"] != false {
		t.Fatalf("sin simulation_id: %d %v", rec.Code, body)
	}
	rec, body = doJSON(mux, http.MethodPost, "/api/report/generate", map[string]any{
		"simulation_id": "sim_inexistente",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("simulación inexistente: %d %v", rec.Code, body)
	}
	rec, body = doJSON(mux, http.MethodPost, "/api/report/generate", map[string]any{
		"simulation_id":    "sim_x",
		"force_regenerate": "yes",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("force_regenerate no booleano: %d %v", rec.Code, body)
	}

	// Simulación sin run_state.json → 409 (no se puede informar de algo no ejecutado).
	state := h.Sim.CreateSimulation(project.ProjectID, *project.GraphID, true, true, nil)
	rec, body = doJSON(mux, http.MethodPost, "/api/report/generate", map[string]any{
		"simulation_id": state.SimulationID,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("sin run_state: %d %v", rec.Code, body)
	}

	// Proyecto sin grafo completo → 409.
	project.Status = models.ProjectOntologyGenerated
	if err := h.PM.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	rec, body = doJSON(mux, http.MethodPost, "/api/report/generate", map[string]any{
		"simulation_id": state.SimulationID,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("proyecto sin grafo: %d %v", rec.Code, body)
	}
}

func TestReportLifecycleEndToEnd(t *testing.T) {
	srv := httptest.NewServer((&scriptedLLM{}).handler())
	t.Cleanup(srv.Close)
	_, mux, _, state := newReportTestHandler(t, srv)

	rec, body := doJSON(mux, http.MethodPost, "/api/report/generate", map[string]any{
		"simulation_id": state.SimulationID,
	})
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("generate: %d %v", rec.Code, body)
	}
	data, _ := body["data"].(map[string]any)
	reportID, _ := data["report_id"].(string)
	taskID, _ := data["task_id"].(string)
	if reportID == "" || taskID == "" || data["status"] != "generating" {
		t.Fatalf("generate data = %v", data)
	}

	task := waitReportTask(t, mux, taskID)
	if task["status"] != "completed" {
		t.Fatalf("task = %v", task)
	}
	result, _ := task["result"].(map[string]any)
	if result["report_id"] != reportID {
		t.Errorf("result = %v", result)
	}

	// Detalle del informe.
	rec, body = doJSON(mux, http.MethodGet, "/api/report/"+reportID, nil)
	if rec.Code != 200 {
		t.Fatalf("get report: %d %v", rec.Code, body)
	}
	rep, _ := body["data"].(map[string]any)
	if rep["status"] != "completed" {
		t.Fatalf("report status = %v (error=%v)", rep["status"], rep["error"])
	}
	outline, _ := rep["outline"].(map[string]any)
	sections, _ := outline["sections"].([]any)
	if len(sections) != 2 {
		t.Errorf("outline sections = %v", outline)
	}
	if md, _ := rep["markdown_content"].(string); !strings.Contains(md, "Informe de prueba") {
		t.Errorf("markdown inesperado: %.120s", md)
	}

	// Progreso y capítulos por polling.
	rec, body = doJSON(mux, http.MethodGet, "/api/report/"+reportID+"/progress", nil)
	if rec.Code != 200 {
		t.Fatalf("progress: %d %v", rec.Code, body)
	}
	progress, _ := body["data"].(map[string]any)
	if progress["status"] != "completed" || progress["progress"].(float64) != 100 {
		t.Errorf("progress = %v", progress)
	}
	completedSections, _ := progress["completed_sections"].([]any)
	if len(completedSections) != 2 {
		t.Errorf("completed_sections = %v", progress["completed_sections"])
	}

	rec, body = doJSON(mux, http.MethodGet, "/api/report/"+reportID+"/sections", nil)
	if rec.Code != 200 {
		t.Fatalf("sections: %d %v", rec.Code, body)
	}
	sectionsData, _ := body["data"].(map[string]any)
	if sectionsData["total_sections"].(float64) != 2 || sectionsData["is_complete"] != true {
		t.Errorf("sections = %v", sectionsData)
	}

	rec, body = doJSON(mux, http.MethodGet, "/api/report/"+reportID+"/section/1", nil)
	if rec.Code != 200 {
		t.Fatalf("section/1: %d %v", rec.Code, body)
	}
	sectionData, _ := body["data"].(map[string]any)
	if sectionData["filename"] != "section_01.md" || !strings.Contains(strOf(sectionData["content"]), "## Hallazgos") {
		t.Errorf("section = %v", sectionData)
	}
	rec, body = doJSON(mux, http.MethodGet, "/api/report/"+reportID+"/section/9", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("section/9: %d %v", rec.Code, body)
	}

	// Archivos por simulación, listado y check (desbloqueo de entrevistas).
	rec, body = doJSON(mux, http.MethodGet, "/api/report/by-simulation/"+state.SimulationID, nil)
	if rec.Code != 200 || body["has_report"] != true {
		t.Fatalf("by-simulation: %d %v", rec.Code, body)
	}
	rec, body = doJSON(mux, http.MethodGet, "/api/report/list?simulation_id="+state.SimulationID, nil)
	if rec.Code != 200 || body["count"].(float64) != 1 {
		t.Fatalf("list: %d %v", rec.Code, body)
	}
	rec, body = doJSON(mux, http.MethodGet, "/api/report/check/"+state.SimulationID, nil)
	if rec.Code != 200 {
		t.Fatalf("check: %d %v", rec.Code, body)
	}
	check, _ := body["data"].(map[string]any)
	if check["has_report"] != true || check["interview_unlocked"] != true {
		t.Errorf("check = %v", check)
	}

	// Descarga.
	rec, _ = doJSON(mux, http.MethodGet, "/api/report/"+reportID+"/download", nil)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), reportID+".md") {
		t.Fatalf("download: %d %q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
	if !strings.Contains(rec.Body.String(), "## Hallazgos") {
		t.Errorf("descarga sin capítulos")
	}

	// Logs estructurados con streaming incremental.
	rec, body = doJSON(mux, http.MethodGet, "/api/report/"+reportID+"/agent-log", nil)
	if rec.Code != 200 {
		t.Fatalf("agent-log: %d %v", rec.Code, body)
	}
	logData, _ := body["data"].(map[string]any)
	logs, _ := logData["logs"].([]any)
	total := int(logData["total_lines"].(float64))
	if len(logs) < 5 || total < len(logs) {
		t.Fatalf("agent-log = %v", logData)
	}
	actions := map[string]bool{}
	for _, raw := range logs {
		if entry, ok := raw.(map[string]any); ok {
			actions[strOf(entry["action"])] = true
		}
	}
	for _, want := range []string{"start", "planning_start", "tool_call", "section_complete", "report_complete"} {
		if !actions[want] {
			t.Errorf("falta acción de log %q (acciones=%v)", want, actions)
		}
	}

	rec, body = doJSON(mux, http.MethodGet,
		fmt.Sprintf("/api/report/%s/agent-log?from_line=%d", reportID, total), nil)
	if rec.Code != 200 {
		t.Fatalf("agent-log from_line: %d %v", rec.Code, body)
	}
	tail, _ := body["data"].(map[string]any)
	if len(tail["logs"].([]any)) != 0 || tail["from_line"].(float64) != float64(total) {
		t.Errorf("agent-log incremental = %v", tail)
	}
	rec, body = doJSON(mux, http.MethodGet, "/api/report/"+reportID+"/agent-log/stream", nil)
	if rec.Code != 200 || body["data"].(map[string]any)["count"].(float64) < 5 {
		t.Fatalf("agent-log/stream: %d %v", rec.Code, body)
	}

	// Log de consola.
	rec, body = doJSON(mux, http.MethodGet, "/api/report/"+reportID+"/console-log", nil)
	if rec.Code != 200 {
		t.Fatalf("console-log: %d %v", rec.Code, body)
	}
	consoleData, _ := body["data"].(map[string]any)
	consoleLogs, _ := consoleData["logs"].([]any)
	if len(consoleLogs) == 0 {
		t.Fatal("console-log vacío")
	}
	if !strings.Contains(strOf(consoleLogs[0]), "INFO:") {
		t.Errorf("línea de consola inesperada: %v", consoleLogs[0])
	}
	rec, body = doJSON(mux, http.MethodGet, "/api/report/"+reportID+"/console-log/stream", nil)
	if rec.Code != 200 || body["data"].(map[string]any)["count"].(float64) == 0 {
		t.Fatalf("console-log/stream: %d %v", rec.Code, body)
	}

	// Chat sobre el informe.
	rec, body = doJSON(mux, http.MethodPost, "/api/report/chat", map[string]any{
		"simulation_id": state.SimulationID,
		"message":       "¿Qué pasó con Ana?",
		"chat_history":  []any{map[string]any{"role": "user", "content": "hola"}},
	})
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("chat: %d %v", rec.Code, body)
	}
	chatData, _ := body["data"].(map[string]any)
	if !strings.Contains(strOf(chatData["response"]), "Respuesta del agente") {
		t.Errorf("chat response = %v", chatData["response"])
	}

	// Segunda generación sin force: reutiliza el informe completo.
	rec, body = doJSON(mux, http.MethodPost, "/api/report/generate", map[string]any{
		"simulation_id": state.SimulationID,
	})
	if rec.Code != 200 {
		t.Fatalf("generate repetido: %d %v", rec.Code, body)
	}
	reuse, _ := body["data"].(map[string]any)
	if reuse["already_generated"] != true || reuse["report_id"] != reportID {
		t.Errorf("reuse = %v", reuse)
	}

	// Regeneración forzada: nuevo report_id y nuevo informe completo.
	rec, body = doJSON(mux, http.MethodPost, "/api/report/generate", map[string]any{
		"simulation_id":    state.SimulationID,
		"force_regenerate": true,
	})
	if rec.Code != 200 {
		t.Fatalf("force generate: %d %v", rec.Code, body)
	}
	forced, _ := body["data"].(map[string]any)
	newID, _ := forced["report_id"].(string)
	if newID == "" || newID == reportID {
		t.Fatalf("force report_id = %v", forced)
	}
	if task := waitReportTask(t, mux, strOf(forced["task_id"])); task["status"] != "completed" {
		t.Fatalf("force task = %v", task)
	}

	// Borrado.
	rec, body = doJSON(mux, http.MethodDelete, "/api/report/"+newID, nil)
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("delete: %d %v", rec.Code, body)
	}
	rec, body = doJSON(mux, http.MethodGet, "/api/report/"+newID, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get tras delete: %d %v", rec.Code, body)
	}
}

func TestReportToolsRoutes(t *testing.T) {
	srv := httptest.NewServer((&scriptedLLM{}).handler())
	t.Cleanup(srv.Close)
	h, mux, project, _ := newReportTestHandler(t, srv)
	graphID := *project.GraphID

	rec, body := doJSON(mux, http.MethodPost, "/api/report/tools/statistics", map[string]any{
		"graph_id": graphID,
	})
	if rec.Code != 200 {
		t.Fatalf("tools/statistics: %d %v", rec.Code, body)
	}
	stats, _ := body["data"].(map[string]any)
	if stats["total_nodes"].(float64) != 2 || stats["total_edges"].(float64) != 1 {
		t.Errorf("stats = %v", stats)
	}

	rec, body = doJSON(mux, http.MethodPost, "/api/report/tools/search", map[string]any{
		"graph_id": graphID,
		"query":    "Ana",
		"limit":    5,
	})
	if rec.Code != 200 {
		t.Fatalf("tools/search: %d %v", rec.Code, body)
	}
	search, _ := body["data"].(map[string]any)
	facts, _ := search["facts"].([]any)
	if len(facts) == 0 || !strings.Contains(strOf(facts[0]), "Ana") {
		t.Errorf("search = %v", search)
	}

	rec, body = doJSON(mux, http.MethodPost, "/api/report/tools/search", map[string]any{
		"graph_id": graphID,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tools/search sin query: %d %v", rec.Code, body)
	}
	_ = h
}

func TestReportNotFoundRoutes(t *testing.T) {
	srv := httptest.NewServer((&scriptedLLM{}).handler())
	t.Cleanup(srv.Close)
	_, mux, _, _ := newReportTestHandler(t, srv)

	for _, path := range []string{
		"/api/report/report_missing",
		"/api/report/report_missing/progress",
		"/api/report/report_missing/download",
		"/api/report/by-simulation/sim_missing",
	} {
		rec, body := doJSON(mux, http.MethodGet, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d %v", path, rec.Code, body)
		}
	}

	rec, _ := doJSON(mux, http.MethodDelete, "/api/report/report_missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("DELETE inexistente: %d", rec.Code)
	}
	// Sections de un id inexistente responde vacío (no 404), como Python.
	rec, body := doJSON(mux, http.MethodGet, "/api/report/report_missing/sections", nil)
	if rec.Code != 200 || body["data"].(map[string]any)["total_sections"].(float64) != 0 {
		t.Errorf("sections inexistente: %d %v", rec.Code, body)
	}
}

func strOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
