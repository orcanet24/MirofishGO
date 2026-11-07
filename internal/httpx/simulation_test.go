// Tests de integración del blueprint /api/simulation sobre el núcleo simx.
// Cubren el ciclo crear → preparar (con LLM falsa) → perfiles/config en disco,
// las rutas de entidades, las descargas, la validación de entrada y el 501
// explícito de las rutas que dependen del runner.
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
	"testing"
	"time"

	"mirofish/internal/config"
	"mirofish/internal/graph"
	"mirofish/internal/llm"
	"mirofish/internal/models"
	"mirofish/internal/simx"
	"mirofish/internal/testdb"
)

func newSimulationTestHandler(t *testing.T, opts ...SimulationHandlerOption) (*SimulationHandler, *http.ServeMux) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		BaseDir:             root,
		UploadDir:           filepath.Join(root, "uploads"),
		SimDataDir:          filepath.Join(root, "uploads", "simulations"),
		AllowedExtensions:   []string{".pdf", ".md", ".txt", ".markdown"},
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

	allOpts := append([]SimulationHandlerOption{WithSimulationGraphClient(client)}, opts...)
	h, err := NewSimulationHandler(cfg, slog.Default(), allOpts...)
	if err != nil {
		t.Fatalf("NewSimulationHandler: %v", err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return h, mux
}

// fakeAgentLLM responde siempre create_post para ejercitar el runner sin
// llamar a un proveedor real.
func fakeAgentLLM(t *testing.T) *httptest.Server {
	t.Helper()
	inner := `{"action":"create_post","args":{"content":"Hola desde el agente"}}`
	quoted, _ := json.Marshal(inner)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"model":"fake","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":%s}}]}`,
			quoted)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeConfigLLM devuelve una configuración de simulación válida (mismo cuerpo
// que usa el test del paquete simx) para no llamar nunca a un proveedor real.
func fakeConfigLLM(t *testing.T) *httptest.Server {
	t.Helper()
	inner := `{
  "total_simulation_hours": 48, "minutes_per_round": 60,
  "agents_per_hour_min": 2, "agents_per_hour_max": 6,
  "peak_hours": [19,20,21,22], "off_peak_hours": [0,1,2,3,4,5],
  "morning_hours": [6,7,8], "work_hours": [9,10,11,12,13,14,15,16,17,18],
  "reasoning": "ok",
  "hot_topics": ["precios"], "narrative_direction": "descubrimiento",
  "initial_posts": [{"content": "Que opinan del nuevo ERP?", "poster_type": "Person"}],
  "agent_configs": []
}`
	quoted, _ := json.Marshal(inner)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"model":"fake","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":%s}}]}`,
			quoted)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// seedSimulationProject crea proyecto + grafo con dos entidades tipadas.
func seedSimulationProject(t *testing.T, h *SimulationHandler) *models.Project {
	t.Helper()
	project, err := h.PM.CreateProject("Proyecto Sim", "")
	if err != nil {
		t.Fatal(err)
	}
	requirement := "Simular la discusión sobre el ERP."
	project.SimulationRequirement = &requirement

	graphID := "mirofish_test_sim"
	if _, err := h.Client.CreateGraph(graphID, "Test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Client.DB.AddNode(graphID, "Ana", []string{"Entity", "Person"}, "Contadora", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Client.DB.AddNode(graphID, "Cafe Mio", []string{"Entity", "Organization"}, "Cafetería", nil); err != nil {
		t.Fatal(err)
	}
	project.GraphID = &graphID
	if err := h.PM.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	h.PM.SaveExtractedText(project.ProjectID, "Ana trabaja en Cafe Mio.")
	return project
}

func TestSimulationCreateValidations(t *testing.T) {
	h, mux := newSimulationTestHandler(t)

	rec, body := doJSON(mux, http.MethodPost, "/api/simulation/create", map[string]any{})
	if rec.Code != http.StatusBadRequest || body["success"] != false {
		t.Fatalf("sin project_id: %d %v", rec.Code, body)
	}

	rec, body = doJSON(mux, http.MethodPost, "/api/simulation/create", map[string]any{
		"project_id": "proj_inexistente",
	})
	if rec.Code != http.StatusNotFound || body["success"] != false {
		t.Fatalf("proyecto inexistente: %d %v", rec.Code, body)
	}

	project, err := h.PM.CreateProject("Sin grafo", "")
	if err != nil {
		t.Fatal(err)
	}
	rec, body = doJSON(mux, http.MethodPost, "/api/simulation/create", map[string]any{
		"project_id": project.ProjectID,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("proyecto sin grafo: %d %v", rec.Code, body)
	}

	seedSimulationProject(t, h)
}

func TestSimulationLifecycleEndToEnd(t *testing.T) {
	srv := fakeConfigLLM(t)
	root := t.TempDir()
	manager := simx.NewManager(filepath.Join(root, "sims"))
	manager.ConfigGeneratorFactory = func() *simx.ConfigGenerator {
		g := simx.NewConfigGenerator()
		g.LLMFactory = func() (*llm.Client, error) {
			return &llm.Client{APIKey: "k", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}, nil
		}
		return g
	}

	agentSrv := fakeAgentLLM(t)
	h, mux := newSimulationTestHandler(t,
		WithSimulationManager(manager),
		WithOasisLLMFactory(func() (*llm.Client, error) {
			return &llm.Client{APIKey: "k", BaseURL: agentSrv.URL, Model: "fake", HTTP: agentSrv.Client()}, nil
		}),
	)
	project := seedSimulationProject(t, h)

	// crear
	rec, body := doJSON(mux, http.MethodPost, "/api/simulation/create", map[string]any{
		"project_id":     project.ProjectID,
		"enable_twitter": true,
		"enable_reddit":  true,
	})
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("create: %d %v", rec.Code, body)
	}
	created, _ := body["data"].(map[string]any)
	simulationID, _ := created["simulation_id"].(string)
	if simulationID == "" || created["status"] != "created" {
		t.Fatalf("create data = %v", created)
	}
	if created["graph_id"] != *project.GraphID {
		t.Errorf("graph_id = %v", created["graph_id"])
	}

	// preparar (perfiles por reglas, sin LLM; configuración con LLM falsa)
	rec, body = doJSON(mux, http.MethodPost, "/api/simulation/prepare", map[string]any{
		"simulation_id":          simulationID,
		"use_llm_for_profiles":   false,
		"parallel_profile_count": 2,
	})
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("prepare: %d %v", rec.Code, body)
	}
	prep, _ := body["data"].(map[string]any)
	taskID, _ := prep["task_id"].(string)
	if taskID == "" || prep["status"] != "preparing" {
		t.Fatalf("prepare data = %v", prep)
	}
	if prep["expected_entities_count"].(float64) != 2 {
		t.Errorf("expected_entities_count = %v", prep["expected_entities_count"])
	}

	// segunda llamada mientras prepara: debe re-adjuntarse, no duplicar
	rec, body = doJSON(mux, http.MethodPost, "/api/simulation/prepare", map[string]any{
		"simulation_id": simulationID,
	})
	if rec.Code != 200 {
		t.Fatalf("prepare en curso: %d %v", rec.Code, body)
	}
	second, _ := body["data"].(map[string]any)
	if second["resume"] != true || second["task_id"] != taskID {
		t.Errorf("prepare en curso = %v", second)
	}

	// sondear la tarea
	deadline := time.Now().Add(20 * time.Second)
	var status map[string]any
	for time.Now().Before(deadline) {
		_, sb := doJSON(mux, http.MethodPost, "/api/simulation/prepare/status", map[string]any{
			"task_id":       taskID,
			"simulation_id": simulationID,
		})
		data, _ := sb["data"].(map[string]any)
		if data != nil {
			status = data
			// "ready" es terminal: la preparación pudo terminar antes del
			// primer sondeo y CheckPrepared responde con el estado final.
			switch data["status"] {
			case "completed", "failed", "ready":
				goto done
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
done:
	if status == nil || (status["status"] != "completed" && status["status"] != "ready") {
		t.Fatalf("prepare status = %v", status)
	}

	// estado final
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID, nil)
	if rec.Code != 200 {
		t.Fatalf("get: %d %v", rec.Code, body)
	}
	sim, _ := body["data"].(map[string]any)
	if sim["status"] != "ready" {
		t.Fatalf("status = %v (error=%v)", sim["status"], sim["error"])
	}
	if sim["profiles_count"].(float64) != 2 {
		t.Errorf("profiles_count = %v", sim["profiles_count"])
	}
	if sim["run_instructions"] == nil {
		t.Error("falta run_instructions en estado ready")
	}

	// perfiles
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID+"/profiles", nil)
	if rec.Code != 200 {
		t.Fatalf("profiles: %d %v", rec.Code, body)
	}
	profilesData, _ := body["data"].(map[string]any)
	if profilesData["count"].(float64) != 2 {
		t.Errorf("profiles = %v", profilesData)
	}

	// perfiles en caliente
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID+"/profiles/realtime", nil)
	if rec.Code != 200 {
		t.Fatalf("profiles/realtime: %d %v", rec.Code, body)
	}
	realtime, _ := body["data"].(map[string]any)
	if realtime["is_generating"] != false || realtime["file_exists"] != true {
		t.Errorf("profiles/realtime = %v", realtime)
	}

	// configuración + resumen en caliente
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID+"/config", nil)
	if rec.Code != 200 {
		t.Fatalf("config: %d %v", rec.Code, body)
	}
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID+"/config/realtime", nil)
	if rec.Code != 200 {
		t.Fatalf("config/realtime: %d %v", rec.Code, body)
	}
	cfgRT, _ := body["data"].(map[string]any)
	summary, _ := cfgRT["summary"].(map[string]any)
	if summary == nil || summary["simulation_hours"].(float64) != 48 {
		t.Errorf("summary = %v", summary)
	}

	// descarga de configuración
	rec, _ = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID+"/config/download", nil)
	if rec.Code != 200 {
		t.Fatalf("config/download: %d", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "simulation_config.json") {
		t.Errorf("Content-Disposition = %q", cd)
	}

	// repetir prepare sin force: ya está preparada, sin nueva tarea
	rec, body = doJSON(mux, http.MethodPost, "/api/simulation/prepare", map[string]any{
		"simulation_id": simulationID,
	})
	if rec.Code != 200 {
		t.Fatalf("prepare repetido: %d %v", rec.Code, body)
	}
	again, _ := body["data"].(map[string]any)
	if again["already_prepared"] != true || again["task_id"] != nil {
		t.Errorf("prepare repetido = %v", again)
	}

	// list e historial
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/list?project_id="+project.ProjectID, nil)
	if rec.Code != 200 || body["count"].(float64) != 1 {
		t.Fatalf("list: %d %v", rec.Code, body)
	}
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/history", nil)
	if rec.Code != 200 || body["count"].(float64) != 1 {
		t.Fatalf("history: %d %v", rec.Code, body)
	}
	hist, _ := body["data"].([]any)
	entry, _ := hist[0].(map[string]any)
	if entry["runner_status"] != "idle" || entry["total_rounds"].(float64) != 48 {
		t.Errorf("history = %v", entry)
	}

	// ejecutar una ronda con el runner OASIS nativo
	rec, body = doJSON(mux, http.MethodPost, "/api/simulation/start", map[string]any{
		"simulation_id": simulationID,
		"platform":      "twitter",
		"max_rounds":    1,
	})
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("start: %d %v", rec.Code, body)
	}
	started, _ := body["data"].(map[string]any)
	if started["runner_status"] != "running" || started["total_rounds"].(float64) != 1 {
		t.Fatalf("start data = %v", started)
	}

	deadline = time.Now().Add(20 * time.Second)
	var runState map[string]any
	for time.Now().Before(deadline) {
		rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID+"/run-status", nil)
		if rec.Code != 200 {
			t.Fatalf("run-status: %d %v", rec.Code, body)
		}
		runState, _ = body["data"].(map[string]any)
		if runState["runner_status"] == "completed" || runState["runner_status"] == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if runState["runner_status"] != "completed" {
		t.Fatalf("run-status final = %v", runState)
	}
	if runState["current_round"].(float64) != 1 || runState["total_rounds"].(float64) != 1 {
		t.Errorf("progreso = %v", runState)
	}
	if runState["total_actions_count"].(float64) < 1 {
		t.Errorf("acciones = %v", runState["total_actions_count"])
	}
	// el contrato de /api/report/generate exige run_state.json terminal
	runStateFile := filepath.Join(h.Manager.SimulationDir(simulationID), "run_state.json")
	rawRunState, err := os.ReadFile(runStateFile)
	if err != nil {
		t.Fatalf("run_state.json no publicado: %v", err)
	}
	var fileRunState map[string]any
	if err := json.Unmarshal(rawRunState, &fileRunState); err != nil {
		t.Fatalf("run_state.json inválido: %v", err)
	}
	if fileRunState["runner_status"] != "completed" {
		t.Errorf("run_state.json = %v", fileRunState)
	}

	// el estado de la simulación refleja la ejecución terminada
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID, nil)
	if rec.Code != 200 {
		t.Fatalf("get tras run: %d %v", rec.Code, body)
	}
	sim, _ = body["data"].(map[string]any)
	if sim["status"] != "completed" {
		t.Errorf("status tras run = %v (error=%v)", sim["status"], sim["error"])
	}

	// acciones persistidas y filtrables
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID+"/actions?limit=100&platform=twitter", nil)
	if rec.Code != 200 {
		t.Fatalf("actions: %d %v", rec.Code, body)
	}
	actionsData, _ := body["data"].(map[string]any)
	if actionsData["count"].(float64) < 1 {
		t.Fatalf("actions = %v", actionsData)
	}
	actionsList, _ := actionsData["actions"].([]any)
	first, _ := actionsList[0].(map[string]any)
	if first["action_type"] != "create_post" || first["agent_id"] == nil {
		t.Errorf("primera acción = %v", first)
	}

	// timeline: una ronda con al menos una acción Twitter y un agente activo
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID+"/timeline", nil)
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("timeline: %d %v", rec.Code, body)
	}
	timelineData, _ := body["data"].(map[string]any)
	if timelineData["rounds_count"].(float64) != 1 {
		t.Fatalf("timeline = %v", timelineData)
	}
	timeline, _ := timelineData["timeline"].([]any)
	round, _ := timeline[0].(map[string]any)
	actionRound := first["round"].(float64)
	if round["round_num"].(float64) != actionRound || round["twitter_actions"].(float64) < 1 ||
		round["active_agents_count"].(float64) < 1 || round["action_types"] == nil {
		t.Errorf("ronda = %v (acción round=%v)", round, actionRound)
	}

	// agent-stats: mismo agregado por agente
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID+"/agent-stats", nil)
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("agent-stats: %d %v", rec.Code, body)
	}
	statsData, _ := body["data"].(map[string]any)
	stats, _ := statsData["stats"].([]any)
	if statsData["agents_count"].(float64) < 1 || len(stats) == 0 {
		t.Fatalf("agent-stats = %v", statsData)
	}
	agentStat, _ := stats[0].(map[string]any)
	if agentStat["agent_id"] == nil || agentStat["total_actions"].(float64) < 1 {
		t.Errorf("stat = %v", agentStat)
	}

	// posts: filas crudas con num_reports y total
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID+"/posts?limit=50", nil)
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("posts: %d %v", rec.Code, body)
	}
	postsData, _ := body["data"].(map[string]any)
	postList, _ := postsData["posts"].([]any)
	if postsData["platform"] != "twitter" || postsData["total"].(float64) < 1 || len(postList) == 0 {
		t.Fatalf("posts = %v", postsData)
	}
	firstPost, _ := postList[0].(map[string]any)
	if firstPost["post_id"] == nil || firstPost["num_reports"] == nil || firstPost["content"] == nil {
		t.Errorf("post = %v", firstPost)
	}

	// comments: insertar una fila y comprobar listado y filtro por post_id
	postID := int64(firstPost["post_id"].(float64))
	var userID int64
	if err := h.Client.DB.SQL().QueryRow(
		"SELECT user_id FROM oasis_posts WHERE simulation_id = ? AND post_id = ?",
		simulationID, postID,
	).Scan(&userID); err != nil {
		t.Fatalf("user del post: %v", err)
	}
	if _, err := h.Client.DB.SQL().Exec(
		`INSERT INTO oasis_comments (simulation_id, post_id, user_id, content, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		simulationID, postID, userID, "Comentario de prueba", "2026-11-07T00:00:00",
	); err != nil {
		t.Fatalf("insert comment: %v", err)
	}

	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/"+simulationID+"/comments", nil)
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("comments: %d %v", rec.Code, body)
	}
	commentsData, _ := body["data"].(map[string]any)
	commentList, _ := commentsData["comments"].([]any)
	if commentsData["count"].(float64) != 1 || len(commentList) != 1 {
		t.Fatalf("comments = %v", commentsData)
	}
	comment, _ := commentList[0].(map[string]any)
	if comment["post_id"].(float64) != float64(postID) || comment["content"] != "Comentario de prueba" {
		t.Errorf("comment = %v", comment)
	}

	rec, body = doJSON(mux, http.MethodGet,
		fmt.Sprintf("/api/simulation/%s/comments?post_id=%d", simulationID, postID), nil)
	if rec.Code != 200 {
		t.Fatalf("comments por post: %d %v", rec.Code, body)
	}
	filtered, _ := body["data"].(map[string]any)
	if filtered["count"].(float64) != 1 {
		t.Errorf("comments filtrados = %v", filtered)
	}

	rec, body = doJSON(mux, http.MethodGet,
		"/api/simulation/"+simulationID+"/comments?post_id=abc", nil)
	if rec.Code != 200 {
		t.Fatalf("comments post_id inválido: %d %v", rec.Code, body)
	}
	invalid, _ := body["data"].(map[string]any)
	if invalid["count"].(float64) != 0 {
		t.Errorf("comments inválidos = %v", invalid)
	}
}

func TestSimulationEntitiesRoutes(t *testing.T) {
	h, mux := newSimulationTestHandler(t)
	project := seedSimulationProject(t, h)
	graphID := *project.GraphID

	rec, body := doJSON(mux, http.MethodGet, "/api/simulation/entities/"+graphID, nil)
	if rec.Code != 200 || body["success"] != true {
		t.Fatalf("entities: %d %v", rec.Code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["filtered_count"].(float64) != 2 || data["total_count"].(float64) != 2 {
		t.Errorf("entities data = %v", data)
	}

	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/entities/"+graphID+"/by-type/Person", nil)
	if rec.Code != 200 {
		t.Fatalf("by-type: %d %v", rec.Code, body)
	}
	byType, _ := body["data"].(map[string]any)
	if byType["count"].(float64) != 1 {
		t.Errorf("by-type = %v", byType)
	}

	nodes, err := h.Client.DB.GetAllNodes(graphID)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("nodes: %v", err)
	}
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/entities/"+graphID+"/"+nodes[0].UUID, nil)
	if rec.Code != 200 {
		t.Fatalf("entity detail: %d %v", rec.Code, body)
	}

	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/entities/"+graphID+"/node_inexistente", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("entity inexistente: %d %v", rec.Code, body)
	}
}

func TestSimulationDownloadsAndEnginePlaceholders(t *testing.T) {
	h, mux := newSimulationTestHandler(t)

	// script no permitido → 400 (allowlist, sin traversal)
	rec, body := doJSON(mux, http.MethodGet, "/api/simulation/script/evil.py/download", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("script no permitido: %d %v", rec.Code, body)
	}
	// permitido pero ausente → 404
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/script/action_logger.py/download", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("script ausente: %d %v", rec.Code, body)
	}
	// permitido y presente → 200
	scriptsDir := filepath.Join(h.Cfg.BaseDir, "backend", "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "action_logger.py"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, _ = doJSON(mux, http.MethodGet, "/api/simulation/script/action_logger.py/download", nil)
	if rec.Code != 200 {
		t.Fatalf("script permitido: %d", rec.Code)
	}

	// configuración de simulación inexistente → 404, no 500
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/sim_inexistente/config/download", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("config inexistente: %d %v", rec.Code, body)
	}

	// start/run-status de una simulación inexistente: 404, no 501
	rec, body = doJSON(mux, http.MethodPost, "/api/simulation/start", map[string]any{"simulation_id": "sim_x"})
	if rec.Code != http.StatusNotFound || body["success"] != false {
		t.Fatalf("start inexistente: %d %v", rec.Code, body)
	}
	// run-status de un id sin ejecución: idle 200 (contrato Python), no 404
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/sim_x/run-status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("run-status inexistente: %d %v", rec.Code, body)
	}
	if idle, _ := body["data"].(map[string]any); idle["runner_status"] != "idle" {
		t.Fatalf("run-status idle = %v", body)
	}

	// analíticas de una simulación nunca ejecutada: vacío 200, no 501
	for _, route := range []string{"timeline", "agent-stats", "posts", "comments"} {
		rec, body = doJSON(mux, http.MethodGet, "/api/simulation/sim_x/"+route, nil)
		if rec.Code != http.StatusOK || body["success"] != true {
			t.Fatalf("%s vacío: %d %v", route, rec.Code, body)
		}
	}
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/sim_x/posts", nil)
	posts, _ := body["data"].(map[string]any)
	if posts["count"].(float64) != 0 || posts["message"] == nil {
		t.Errorf("posts vacío = %v", posts)
	}
	rec, body = doJSON(mux, http.MethodGet, "/api/simulation/sim_x/posts?platform=reddit", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("posts reddit: %d %v", rec.Code, body)
	}
	reddit, _ := body["data"].(map[string]any)
	if reddit["platform"] != "reddit" || reddit["count"].(float64) != 0 {
		t.Errorf("posts reddit = %v", reddit)
	}
}
