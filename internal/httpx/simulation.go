// Port de backend/app/api/simulation.py — blueprint /api/simulation.
//
// Rutas implementadas sobre el núcleo de internal/simx (lectura de entidades,
// creación, preparación asíncrona con perfiles y configuración, perfiles y
// configuración en caliente, descargas e historial).
//
// start/stop/run-status/run-status/detail, actions, timeline, agent-stats,
// posts y comments usan el runner OASIS nativo (Twitter) sobre MySQL. Reddit,
// entrevistas y close-env siguen respondiendo 501 con un mensaje explícito:
// devolver datos falsos o un 500 genérico rompería la UI de forma silenciosa.
//
// El despacho es manual sobre "GET|POST /api/simulation/" porque las formas de
// las rutas se solapan (p.ej. /entities/{graph}/{uuid} y /{id}/profiles/
// realtime): registrarlas como patrones separados haría que ServeMux entre en
// pánico por patrones en conflicto al arrancar el servidor.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mirofish/internal/config"
	"mirofish/internal/graph"
	"mirofish/internal/llm"
	"mirofish/internal/locale"
	"mirofish/internal/models"
	"mirofish/internal/oasis"
	"mirofish/internal/simx"
)

// SimulationHandler concentra el estado del blueprint de simulación.
type SimulationHandler struct {
	Cfg     *config.Config
	PM      *models.ProjectManager
	Client  *graph.Client
	Manager *simx.Manager
	Logger  *slog.Logger

	// ProfileFactory es inyectable en tests.
	ProfileFactory func(client *graph.Client, graphID string) *simx.ProfileGenerator

	// Oasis ejecuta las simulaciones Twitter contra MySQL.
	Oasis *oasis.Engine
	// OasisLLMFactory permite inyectar un LLM falso en tests; nil usa el .env.
	OasisLLMFactory func() (*llm.Client, error)
}

// SimulationHandlerOption configura el handler (tests).
type SimulationHandlerOption func(*SimulationHandler)

// WithSimulationGraphClient fija el cliente de grafo local.
func WithSimulationGraphClient(c *graph.Client) SimulationHandlerOption {
	return func(h *SimulationHandler) { h.Client = c }
}

// WithSimulationManager fija el gestor de simulaciones (directorio temporal,
// generadores falsos...).
func WithSimulationManager(m *simx.Manager) SimulationHandlerOption {
	return func(h *SimulationHandler) { h.Manager = m }
}

// WithOasisEngine inyecta un motor ya construido (tests de integración).
func WithOasisEngine(e *oasis.Engine) SimulationHandlerOption {
	return func(h *SimulationHandler) { h.Oasis = e }
}

// WithOasisLLMFactory inyecta el constructor del LLM de los agentes.
func WithOasisLLMFactory(factory func() (*llm.Client, error)) SimulationHandlerOption {
	return func(h *SimulationHandler) { h.OasisLLMFactory = factory }
}

// NewSimulationHandler crea el handler con gestor de proyectos y simulaciones.
func NewSimulationHandler(cfg *config.Config, logger *slog.Logger, opts ...SimulationHandlerOption) (*SimulationHandler, error) {
	simDataDir := cfg.SimDataDir
	if simDataDir == "" {
		// Sin SimDataDir explícito se sigue el layout de Python:
		// <uploads>/simulations (nunca el directorio de trabajo actual).
		simDataDir = filepath.Join(cfg.UploadDir, "simulations")
	}
	h := &SimulationHandler{
		Cfg:     cfg,
		PM:      models.NewProjectManager(cfg.UploadDir),
		Manager: simx.NewManager(simDataDir),
		Logger:  logger,
	}
	for _, opt := range opts {
		opt(h)
	}
	if h.Client == nil {
		client, err := graph.NewClient("")
		if err != nil {
			return nil, err
		}
		h.Client = client
	}
	if h.Oasis == nil {
		store, err := oasis.NewStore(h.Client.DB.SQL())
		if err != nil {
			return nil, err
		}
		engineOpts := []oasis.EngineOption{oasis.WithEngineLogger(logger)}
		if h.OasisLLMFactory != nil {
			engineOpts = append(engineOpts, oasis.WithEngineLLMFactory(h.OasisLLMFactory))
		}
		h.Oasis = oasis.NewEngine(store, engineOpts...)
	}
	return h, nil
}

// Register monta el blueprint en el mux.
func (h *SimulationHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/simulation/", h.handleGET)
	mux.HandleFunc("POST /api/simulation/", h.handlePOST)
}

// ===== utilidades =====

func pathSegments(path, prefix string) []string {
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(rest, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (h *SimulationHandler) localeOf(r *http.Request) string {
	return locale.Normalize(r.Header.Get("Accept-Language"))
}

// decodeBody replica `request.get_json() or {}`: cuerpo vacío es un objeto
// vacío, cuerpo malformado es 400 (no un 500 genérico).
func decodeBody(r *http.Request) (map[string]any, error) {
	body := map[string]any{}
	if r.Body == nil {
		return body, nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, nil
}

func boolField(body map[string]any, key string, def bool) bool {
	if v, ok := body[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

func stringField(body map[string]any, key string) string {
	if v, ok := body[key].(string); ok {
		return v
	}
	return ""
}

func stringSliceField(body map[string]any, key string) []string {
	raw, ok := body[key].([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, item := range raw {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// notPorted responde 501 con el nombre de la ruta que falta.
func (h *SimulationHandler) notPorted(w http.ResponseWriter, feature string) {
	JSON(w, http.StatusNotImplemented, map[string]any{
		"success": false,
		"error": "not implemented yet: " + feature +
			" (esta parte de OASIS todavía no está portada al binario Go)",
		"feature": feature,
	})
}

// ===== despacho GET =====

func (h *SimulationHandler) handleGET(w http.ResponseWriter, r *http.Request) {
	segs := pathSegments(r.URL.Path, "/api/simulation/")
	loc := h.localeOf(r)
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	if len(segs) == 0 {
		HTTPError(w, http.StatusNotFound, "not found")
		return
	}

	switch {
	case len(segs) == 1 && segs[0] == "list":
		h.listSimulations(w, r)
	case len(segs) == 1 && segs[0] == "history":
		h.history(w, r)
	case len(segs) == 1:
		h.getSimulation(w, r, segs[0])

	case len(segs) == 2 && segs[0] == "entities":
		h.graphEntities(w, r, segs[1])
	case len(segs) == 3 && segs[0] == "entities":
		h.entityDetail(w, r, segs[1], segs[2])
	case len(segs) == 4 && segs[0] == "entities" && segs[2] == "by-type":
		h.graphEntitiesByType(w, r, segs[1], segs[3])

	case len(segs) == 2 && segs[1] == "profiles":
		h.simulationProfiles(w, r, segs[0])
	case len(segs) == 3 && segs[1] == "profiles" && segs[2] == "realtime":
		h.simulationProfilesRealtime(w, r, segs[0])
	case len(segs) == 2 && segs[1] == "config":
		h.simulationConfig(w, r, segs[0])
	case len(segs) == 3 && segs[1] == "config" && segs[2] == "realtime":
		h.simulationConfigRealtime(w, r, segs[0])
	case len(segs) == 3 && segs[1] == "config" && segs[2] == "download":
		h.downloadConfig(w, r, segs[0])
	case len(segs) == 3 && segs[0] == "script" && segs[2] == "download":
		h.downloadScript(w, r, segs[1])

	case len(segs) == 2 && segs[1] == "run-status":
		h.runStatus(w, r, segs[0])
	case len(segs) == 3 && segs[1] == "run-status" && segs[2] == "detail":
		h.runStatusDetail(w, r, segs[0])
	case len(segs) == 2 && segs[1] == "actions":
		h.simulationActions(w, r, segs[0])
	case len(segs) == 2 && segs[1] == "timeline":
		h.simulationTimeline(w, r, segs[0])
	case len(segs) == 2 && segs[1] == "agent-stats":
		h.simulationAgentStats(w, r, segs[0])
	case len(segs) == 2 && segs[1] == "posts":
		h.simulationPosts(w, r, segs[0])
	case len(segs) == 2 && segs[1] == "comments":
		h.simulationComments(w, r, segs[0])
	default:
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   t("api.simulationNotFound", map[string]string{"id": segs[0]}),
		})
	}
}

// ===== despacho POST =====

func (h *SimulationHandler) handlePOST(w http.ResponseWriter, r *http.Request) {
	segs := pathSegments(r.URL.Path, "/api/simulation/")
	switch {
	case len(segs) == 1 && segs[0] == "create":
		h.createSimulation(w, r)
	case len(segs) == 1 && segs[0] == "prepare":
		h.prepareSimulation(w, r)
	case len(segs) == 2 && segs[0] == "prepare" && segs[1] == "status":
		h.prepareStatus(w, r)
	case len(segs) == 1 && segs[0] == "generate-profiles":
		h.generateProfiles(w, r)

	case len(segs) == 1 && segs[0] == "start":
		h.startSimulation(w, r)
	case len(segs) == 1 && segs[0] == "stop":
		h.stopSimulation(w, r)
	case len(segs) == 1 && segs[0] == "env-status":
		h.envStatus(w, r)
	case len(segs) == 1 && (segs[0] == "interview" || segs[0] == "close-env"):
		h.notPorted(w, "POST /api/simulation/"+segs[0])
	case len(segs) == 2 && segs[0] == "interview":
		h.notPorted(w, "POST /api/simulation/interview/"+segs[1])
	default:
		HTTPError(w, http.StatusNotFound, "not found")
	}
}

// ===== entidades =====

func (h *SimulationHandler) graphEntities(w http.ResponseWriter, r *http.Request, graphID string) {
	if !h.Cfg.UseLocalGraph() && h.Cfg.ZEPAPIKey == "" {
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false, "error": locale.T(h.localeOf(r), "api.zepApiKeyMissing", nil),
		})
		return
	}
	entityTypes := []string{}
	if raw := strings.TrimSpace(r.URL.Query().Get("entity_types")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				entityTypes = append(entityTypes, p)
			}
		}
	}
	enrich := strings.ToLower(r.URL.Query().Get("enrich")) != "false"

	reader := simx.NewEntityReader(h.Client)
	filtered, err := reader.FilterDefinedEntities(graphID, entityTypes, enrich)
	if err != nil {
		h.Logger.Error("获取图谱实体失败", "graph_id", graphID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false, "error": err.Error(),
		})
		return
	}
	// entity_types se ordena para que la respuesta sea estable entre llamadas
	// (el Python devolvía list(set), con orden arbitrario).
	types := append([]string(nil), filtered.EntityTypes...)
	sort.Strings(types)
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"entities":       filtered.Entities,
			"entity_types":   types,
			"total_count":    filtered.TotalCount,
			"filtered_count": filtered.FilteredCount,
		},
	})
}

func (h *SimulationHandler) entityDetail(w http.ResponseWriter, r *http.Request, graphID, entityUUID string) {
	loc := h.localeOf(r)
	reader := simx.NewEntityReader(h.Client)
	entity, err := reader.GetEntityWithContext(graphID, entityUUID)
	if err != nil {
		h.Logger.Error("获取实体详情失败", "entity_uuid", entityUUID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false, "error": err.Error(),
		})
		return
	}
	if entity == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.entityNotFound", map[string]string{"id": entityUUID}),
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": entity})
}

func (h *SimulationHandler) graphEntitiesByType(w http.ResponseWriter, r *http.Request, graphID, entityType string) {
	enrich := strings.ToLower(r.URL.Query().Get("enrich")) != "false"
	reader := simx.NewEntityReader(h.Client)
	entities, err := reader.GetEntitiesByType(graphID, entityType, enrich)
	if err != nil {
		h.Logger.Error("获取实体失败", "graph_id", graphID, "type", entityType, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false, "error": err.Error(),
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"entity_type": entityType,
			"count":       len(entities),
			"entities":    entities,
		},
	})
}

// ===== simulación: crear / consultar =====

func (h *SimulationHandler) createSimulation(w http.ResponseWriter, r *http.Request) {
	loc := h.localeOf(r)
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	body, err := decodeBody(r)
	if err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	projectID := strings.TrimSpace(stringField(body, "project_id"))
	if projectID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.requireProjectId", nil),
		})
		return
	}
	project := h.PM.GetProject(projectID)
	if project == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   t("api.projectNotFound", map[string]string{"id": projectID}),
		})
		return
	}
	graphID := strings.TrimSpace(stringField(body, "graph_id"))
	if graphID == "" && project.GraphID != nil {
		graphID = *project.GraphID
	}
	if graphID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.graphNotBuilt", nil),
		})
		return
	}

	state := h.Manager.CreateSimulation(projectID, graphID,
		boolField(body, "enable_twitter", true),
		boolField(body, "enable_reddit", true),
		nil,
	)
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": state.ToDict()})
}

func (h *SimulationHandler) getSimulation(w http.ResponseWriter, r *http.Request, simulationID string) {
	loc := h.localeOf(r)
	state := h.Manager.GetSimulation(simulationID)
	if state != nil {
		// Reflejar completion/stop antes de serializar; el runner escribe en
		// MySQL y state.json se sincroniza al consultar.
		if run, err := h.Oasis.State(simulationID); err == nil && run != nil {
			h.syncSimulationStatus(simulationID, run)
			state = h.Manager.GetSimulation(simulationID)
		}
	}
	if state == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.simulationNotFound", map[string]string{"id": simulationID}),
		})
		return
	}
	result := state.ToDict()
	if state.Status == simx.SimReady {
		result["run_instructions"] = h.Manager.GetRunInstructions(simulationID)
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": result})
}

func (h *SimulationHandler) listSimulations(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	states := h.Manager.ListSimulations(projectID)
	sortSimulations(states)
	data := make([]map[string]any, 0, len(states))
	for _, s := range states {
		if run, err := h.Oasis.State(s.SimulationID); err == nil && run != nil {
			h.syncSimulationStatus(s.SimulationID, run)
		}
		data = append(data, s.ToDict())
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    data,
		"count":   len(data),
	})
}

// sortSimulations ordena por created_at desc (más reciente primero) para que
// /list y /history sean deterministas; os.ReadDir devuelve orden alfabético.
func sortSimulations(states []*simx.SimulationState) {
	sort.SliceStable(states, func(i, j int) bool {
		return states[i].CreatedAt > states[j].CreatedAt
	})
}

func (h *SimulationHandler) history(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	states := h.Manager.ListSimulations("")
	sortSimulations(states)
	if len(states) > limit {
		states = states[:limit]
	}

	data := make([]map[string]any, 0, len(states))
	for _, state := range states {
		if run, err := h.Oasis.State(state.SimulationID); err == nil && run != nil {
			h.syncSimulationStatus(state.SimulationID, run)
		}
		sim := state.ToDict()

		recommendedRounds := 0
		if cfg := h.Manager.GetSimulationConfig(state.SimulationID); cfg != nil {
			sim["simulation_requirement"] = cfg["simulation_requirement"]
			timeConfig, _ := cfg["time_config"].(map[string]any)
			hours := floatFromAny(timeConfig["total_simulation_hours"])
			sim["total_simulation_hours"] = jsonNumber(hours)
			minutes := floatFromAny(timeConfig["minutes_per_round"])
			if minutes < 1 {
				minutes = 1
			}
			recommendedRounds = int(hours * 60 / minutes)
		} else {
			sim["simulation_requirement"] = ""
			sim["total_simulation_hours"] = 0
		}

		runState := h.runStateFor(state.SimulationID)
		if runState != nil {
			sim["current_round"] = runState["current_round"]
			sim["runner_status"] = runState["runner_status"]
			total := intFromAny(runState["total_rounds"])
			if total <= 0 {
				total = recommendedRounds
			}
			sim["total_rounds"] = total
		} else {
			sim["current_round"] = 0
			sim["runner_status"] = "idle"
			sim["total_rounds"] = recommendedRounds
		}

		files := []map[string]any{}
		if project := h.PM.GetProject(state.ProjectID); project != nil {
			for i, f := range project.Files {
				if i >= 3 {
					break
				}
				name := f.Filename
				if name == "" {
					name = "未知文件"
				}
				files = append(files, map[string]any{"filename": name})
			}
		}
		sim["files"] = files
		sim["report_id"] = h.findReportIDForSimulation(state.SimulationID)
		sim["version"] = "v1.0.2"
		if len(state.CreatedAt) >= 10 {
			sim["created_date"] = state.CreatedAt[:10]
		} else {
			sim["created_date"] = ""
		}
		data = append(data, sim)
	}

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    data,
		"count":   len(data),
	})
}

// findReportIDForSimulation replica _get_report_id_for_simulation: el report
// más reciente (created_at) cuyo meta.json referencia la simulación.
func (h *SimulationHandler) findReportIDForSimulation(simulationID string) any {
	reportsDir := filepath.Join(h.Cfg.UploadDir, "reports")
	entries, err := os.ReadDir(reportsDir)
	if err != nil {
		return nil
	}
	var bestID string
	var bestCreated string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(reportsDir, e.Name(), "meta.json"))
		if err != nil {
			continue
		}
		var meta map[string]any
		if json.Unmarshal(raw, &meta) != nil {
			continue
		}
		if stringField(meta, "simulation_id") != simulationID {
			continue
		}
		id := stringField(meta, "report_id")
		created := stringField(meta, "created_at")
		if id != "" && created >= bestCreated {
			bestID, bestCreated = id, created
		}
	}
	if bestID == "" {
		return nil
	}
	return bestID
}

// runStateFor devuelve el estado de ejecución desde MySQL y, si no existe,
// cae al run_state.json de una corrida hecha con el backend Python.
func (h *SimulationHandler) runStateFor(simulationID string) map[string]any {
	if h.Oasis != nil {
		if run, err := h.Oasis.State(simulationID); err == nil && run != nil {
			return run.ToMap()
		}
	}
	return readRunState(h.Manager.SimulationDir(simulationID))
}

// readRunState lee run_state.json si existe (compatibilidad con corridas
// hechas por el runner Python antes de portar OASIS).
func readRunState(simDir string) map[string]any {
	raw, err := os.ReadFile(filepath.Join(simDir, "run_state.json"))
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func jsonNumber(f float64) any {
	if f == float64(int64(f)) {
		return int64(f)
	}
	return f
}

func floatFromAny(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	return 0
}

func intFromAny(v any) int { return int(floatFromAny(v)) }

// ===== preparación =====

func (h *SimulationHandler) prepareSimulation(w http.ResponseWriter, r *http.Request) {
	loc := h.localeOf(r)
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	body, err := decodeBody(r)
	if err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	simulationID := strings.TrimSpace(stringField(body, "simulation_id"))
	if simulationID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.requireSimulationId", nil),
		})
		return
	}
	state := h.Manager.GetSimulation(simulationID)
	if state == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   t("api.simulationNotFound", map[string]string{"id": simulationID}),
		})
		return
	}

	forceRegenerate := boolField(body, "force_regenerate", false)
	if !forceRegenerate {
		if prepared, info := h.Manager.CheckPrepared(simulationID); prepared {
			JSON(w, http.StatusOK, map[string]any{
				"success": true,
				"data": map[string]any{
					"simulation_id":    simulationID,
					"status":           "ready",
					"message":          t("api.alreadyPrepared", nil),
					"already_prepared": true,
					"prepare_info":     info,
				},
			})
			return
		}
		// Preparación EN CURSO: re-adjuntar a la tarea activa en vez de crear
		// otra que regenere todos los perfiles (duplicaría el gasto de LLM).
		if state.Status == simx.SimPreparing {
			if task := activePrepareTask(simulationID); task != nil {
				JSON(w, http.StatusOK, map[string]any{
					"success": true,
					"data": map[string]any{
						"simulation_id":    simulationID,
						"status":           "preparing",
						"already_prepared": false,
						"resume":           true,
						"task_id":          task.TaskID,
						"message":          t("api.prepareInProgress", nil),
					},
				})
				return
			}
		}
	}

	project := h.PM.GetProject(state.ProjectID)
	if project == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   t("api.projectNotFound", map[string]string{"id": state.ProjectID}),
		})
		return
	}
	requirement := ""
	if project.SimulationRequirement != nil {
		requirement = strings.TrimSpace(*project.SimulationRequirement)
	}
	if requirement == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.projectMissingRequirement", nil),
		})
		return
	}
	documentText := ""
	if text := h.PM.GetExtractedText(state.ProjectID); text != nil {
		documentText = *text
	}

	entityTypes := stringSliceField(body, "entity_types")
	useLLMForProfiles := boolField(body, "use_llm_for_profiles", true)
	parallelProfileCount := 5
	if n := intFromAny(body["parallel_profile_count"]); n > 0 {
		parallelProfileCount = n
	}

	// Conteo síncrono de entidades: la UI muestra el total esperado apenas
	// arranca la preparación. Un fallo aquí no aborta (se reintenta en la tarea).
	if filtered, err := simx.NewEntityReader(h.Client).FilterDefinedEntities(
		state.GraphID, entityTypes, false); err == nil {
		state.EntitiesCount = filtered.FilteredCount
		types := append([]string(nil), filtered.EntityTypes...)
		sort.Strings(types)
		state.EntityTypes = types
	} else {
		h.Logger.Warn("no se pudo contar entidades antes de preparar", "error", err)
	}

	taskID := models.GetTaskManager().CreateTask("simulation_prepare", map[string]any{
		"simulation_id": simulationID,
		"project_id":    state.ProjectID,
	})

	state.Status = simx.SimPreparing
	h.Manager.SaveSimulationState(state)

	go h.runPrepare(taskID, simulationID, loc, requirement, documentText,
		entityTypes, useLLMForProfiles, parallelProfileCount)

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"simulation_id":           simulationID,
			"task_id":                 taskID,
			"status":                  "preparing",
			"message":                 t("api.prepareStarted", nil),
			"already_prepared":        false,
			"expected_entities_count": state.EntitiesCount,
			"entity_types":            state.EntityTypes,
		},
	})
}

// activePrepareTask busca una tarea de preparación viva para la simulación.
func activePrepareTask(simulationID string) *models.TaskDict {
	tasks := models.GetTaskManager().ListTasks("simulation_prepare")
	for i := range tasks {
		if stringField(tasks[i].Metadata, "simulation_id") != simulationID {
			continue
		}
		if tasks[i].Status == string(models.TaskPending) || tasks[i].Status == string(models.TaskProcessing) {
			return &tasks[i]
		}
	}
	return nil
}

// runPrepare replica run_prepare del Python: ejecuta la preparación con
// callback de progreso ponderado por etapas y publica el resultado en la tarea.
func (h *SimulationHandler) runPrepare(taskID, simulationID, loc, requirement, documentText string,
	entityTypes []string, useLLM bool, parallelProfileCount int) {

	taskManager := models.GetTaskManager()
	logger := h.Logger.With("task_id", taskID, "simulation_id", simulationID)

	status := models.TaskProcessing
	startMsg := locale.T(loc, "progress.startPreparingEnv", nil)
	taskManager.UpdateTask(taskID, models.TaskUpdate{Status: &status, Progress: intPtr(0), Message: &startMsg})

	stageWeights := map[string][2]int{
		"reading":             {0, 20},
		"generating_profiles": {20, 70},
		"generating_config":   {70, 90},
		"copying_scripts":     {90, 100},
	}
	stageNameKeys := map[string]string{
		"reading":             "progress.readingGraphEntities",
		"generating_profiles": "progress.generatingProfiles",
		"generating_config":   "progress.generatingSimConfig",
		"copying_scripts":     "progress.preparingScripts",
	}
	order := []string{"reading", "generating_profiles", "generating_config", "copying_scripts"}
	stageDetails := map[string]map[string]any{}
	// La generación de perfiles en paralelo invoca este callback desde varias
	// goroutines: stageDetails y la lectura de los mapas necesitan un candado.
	var progressMu sync.Mutex

	progressCallback := func(stage string, progress int, message string, extra map[string]any) {
		progressMu.Lock()
		defer progressMu.Unlock()
		weights, ok := stageWeights[stage]
		if !ok {
			weights = [2]int{0, 100}
		}
		currentProgress := weights[0] + (weights[1]-weights[0])*progress/100

		stageName := locale.T(loc, stageNameKeys[stage], nil)
		current := intFromAny(extra["current"])
		total := intFromAny(extra["total"])
		itemName := stringField(extra, "item_name")
		stageDetails[stage] = map[string]any{
			"stage_name":     stageName,
			"stage_progress": progress,
			"current":        current,
			"total":          total,
			"item_name":      itemName,
		}

		stageIndex := 1
		for i, name := range order {
			if name == stage {
				stageIndex = i + 1
				break
			}
		}
		detail := map[string]any{
			"current_stage":      stage,
			"current_stage_name": stageName,
			"stage_index":        stageIndex,
			"total_stages":       len(order),
			"stage_progress":     progress,
			"current_item":       current,
			"total_items":        total,
			"item_description":   message,
		}
		var detailed string
		if total > 0 {
			detailed = fmt.Sprintf("[%d/%d] %s: %d/%d - %s",
				stageIndex, len(order), stageName, current, total, message)
		} else {
			detailed = fmt.Sprintf("[%d/%d] %s: %s", stageIndex, len(order), stageName, message)
		}

		taskManager.UpdateTask(taskID, models.TaskUpdate{
			Progress:       &currentProgress,
			Message:        &detailed,
			ProgressDetail: detail,
		})
	}

	prepared, err := h.Manager.PrepareSimulation(
		h.Client, simulationID, requirement, documentText,
		entityTypes, useLLM, progressCallback, parallelProfileCount, loc,
	)
	if err != nil {
		logger.Error("准备模拟失败", "error", err)
		// PrepareSimulation ya deja el estado FAILED en sus rutas de error;
		// se re-guarda para cubrir errores externos al manager.
		if state := h.Manager.GetSimulation(simulationID); state != nil && state.Status != simx.SimFailed {
			msg := err.Error()
			state.Status = simx.SimFailed
			state.Error = &msg
			h.Manager.SaveSimulationState(state)
		}
		failMsg := locale.T(loc, "progress.taskFailed", nil)
		errMsg := err.Error()
		taskManager.UpdateTask(taskID, models.TaskUpdate{
			Status:  statusPtr(models.TaskFailed),
			Message: &failMsg,
			Error:   &errMsg,
		})
		return
	}

	taskManager.CompleteTask(taskID, prepared.ToSimpleDict())
	logger.Info("准备模拟完成", "profiles", prepared.ProfilesCount)
}

func statusPtr(s models.TaskStatus) *models.TaskStatus { return &s }

func (h *SimulationHandler) prepareStatus(w http.ResponseWriter, r *http.Request) {
	loc := h.localeOf(r)
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	body, err := decodeBody(r)
	if err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	taskID := strings.TrimSpace(stringField(body, "task_id"))
	simulationID := strings.TrimSpace(stringField(body, "simulation_id"))

	if simulationID != "" {
		if prepared, info := h.Manager.CheckPrepared(simulationID); prepared {
			JSON(w, http.StatusOK, map[string]any{
				"success": true,
				"data": map[string]any{
					"simulation_id":    simulationID,
					"status":           "ready",
					"progress":         100,
					"message":          t("api.alreadyPrepared", nil),
					"already_prepared": true,
					"prepare_info":     info,
				},
			})
			return
		}
	}

	if taskID == "" {
		if simulationID != "" {
			JSON(w, http.StatusOK, map[string]any{
				"success": true,
				"data": map[string]any{
					"simulation_id":    simulationID,
					"status":           "not_started",
					"progress":         0,
					"message":          t("api.notStartedPrepare", nil),
					"already_prepared": false,
				},
			})
			return
		}
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.requireTaskOrSimId", nil),
		})
		return
	}

	task, ok := models.GetTaskManager().GetTask(taskID)
	if !ok {
		if simulationID != "" {
			if prepared, info := h.Manager.CheckPrepared(simulationID); prepared {
				JSON(w, http.StatusOK, map[string]any{
					"success": true,
					"data": map[string]any{
						"simulation_id":    simulationID,
						"task_id":          taskID,
						"status":           "ready",
						"progress":         100,
						"message":          t("api.taskCompletedPrepared", nil),
						"already_prepared": true,
						"prepare_info":     info,
					},
				})
				return
			}
		}
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   t("api.taskNotFound", map[string]string{"id": taskID}),
		})
		return
	}

	payload := task.ToDict()
	data := map[string]any{
		"task_id":          payload.TaskID,
		"task_type":        payload.TaskType,
		"status":           payload.Status,
		"created_at":       payload.CreatedAt,
		"updated_at":       payload.UpdatedAt,
		"progress":         payload.Progress,
		"message":          payload.Message,
		"progress_detail":  payload.ProgressDetail,
		"result":           payload.Result,
		"error":            payload.Error,
		"metadata":         payload.Metadata,
		"already_prepared": false,
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// ===== perfiles / configuración =====

func (h *SimulationHandler) simulationProfiles(w http.ResponseWriter, r *http.Request, simulationID string) {
	loc := h.localeOf(r)
	state := h.Manager.GetSimulation(simulationID)
	if state == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.simulationNotFound", map[string]string{"id": simulationID}),
		})
		return
	}
	platform := r.URL.Query().Get("platform")
	if platform == "" {
		platform = state.GetDefaultPlatform()
	}
	if platform != "twitter" && platform != "reddit" {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false, "error": "不支持的平台: " + platform,
		})
		return
	}
	profiles, err := h.Manager.GetProfiles(simulationID, platform)
	if err != nil {
		h.Logger.Error("获取Profile失败", "simulation_id", simulationID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false, "error": err.Error(),
		})
		return
	}
	if profiles == nil {
		profiles = []map[string]any{}
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"platform": platform,
			"count":    len(profiles),
			"profiles": profiles,
		},
	})
}

func (h *SimulationHandler) simulationProfilesRealtime(w http.ResponseWriter, r *http.Request, simulationID string) {
	loc := h.localeOf(r)
	platform := r.URL.Query().Get("platform")
	data, ok := h.Manager.GetProfilesRealtime(simulationID, platform)
	if !ok {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.simulationNotFound", map[string]string{"id": simulationID}),
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

func (h *SimulationHandler) simulationConfig(w http.ResponseWriter, r *http.Request, simulationID string) {
	loc := h.localeOf(r)
	cfg := h.Manager.GetSimulationConfig(simulationID)
	if cfg == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false, "error": locale.T(loc, "api.configNotFound", nil),
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": cfg})
}

func (h *SimulationHandler) simulationConfigRealtime(w http.ResponseWriter, r *http.Request, simulationID string) {
	loc := h.localeOf(r)
	data, ok := h.Manager.GetConfigRealtime(simulationID)
	if !ok {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.simulationNotFound", map[string]string{"id": simulationID}),
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

func (h *SimulationHandler) downloadConfig(w http.ResponseWriter, r *http.Request, simulationID string) {
	loc := h.localeOf(r)
	configPath := filepath.Join(h.Manager.SimulationDir(simulationID), "simulation_config.json")
	// simulationID viene de la URL: se valida antes de tocar el disco para que
	// "../../etc/passwd" no se convierta en una ruta fuera de uploads.
	if !h.Manager.Exists(simulationID) || strings.ContainsAny(simulationID, `/\`) {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false, "error": locale.T(loc, "api.configFileNotFound", nil),
		})
		return
	}
	if _, err := os.Stat(configPath); err != nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false, "error": locale.T(loc, "api.configFileNotFound", nil),
		})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="simulation_config.json"`)
	http.ServeFile(w, r, configPath)
}

var allowedSimulationScripts = map[string]bool{
	"run_twitter_simulation.py":  true,
	"run_reddit_simulation.py":   true,
	"run_parallel_simulation.py": true,
	"action_logger.py":           true,
}

func (h *SimulationHandler) downloadScript(w http.ResponseWriter, r *http.Request, scriptName string) {
	loc := h.localeOf(r)
	if !allowedSimulationScripts[scriptName] {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error": locale.T(loc, "api.unknownScript", map[string]string{
				"name":    scriptName,
				"allowed": "run_twitter_simulation.py, run_reddit_simulation.py, run_parallel_simulation.py, action_logger.py",
			}),
		})
		return
	}
	scriptsDir := filepath.Join(h.Cfg.BaseDir, "backend", "scripts")
	scriptPath := filepath.Join(scriptsDir, scriptName)
	if _, err := os.Stat(scriptPath); err != nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.scriptFileNotFound", map[string]string{"name": scriptName}),
		})
		return
	}
	w.Header().Set("Content-Type", "text/x-python; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+scriptName+`"`)
	http.ServeFile(w, r, scriptPath)
}

// ===== ejecución OASIS (Twitter sobre MySQL) =====

// startSimulation replica POST /start. En esta rebanada solo existe la
// plataforma Twitter: con platform=parallel se ejecuta Twitter y se declara
// explícitamente que Reddit no corrió, en vez de fingir una corrida doble.
func (h *SimulationHandler) startSimulation(w http.ResponseWriter, r *http.Request) {
	loc := h.localeOf(r)
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	body, err := decodeBody(r)
	if err != nil {
		h.HTTPBadRequest(w, "invalid JSON body: "+err.Error())
		return
	}
	simulationID := strings.TrimSpace(stringField(body, "simulation_id"))
	if simulationID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.requireSimulationId", nil),
		})
		return
	}

	state := h.Manager.GetSimulation(simulationID)
	if state == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   t("api.simulationNotFound", map[string]string{"id": simulationID}),
		})
		return
	}

	platform := strings.TrimSpace(stringField(body, "platform"))
	if platform == "" {
		platform = "parallel"
	}
	if platform != "twitter" && platform != "reddit" && platform != "parallel" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   t("api.invalidPlatform", map[string]string{"platform": platform}),
		})
		return
	}
	if platform == "reddit" || !state.EnableTwitter {
		h.notPorted(w, "plataforma Reddit")
		return
	}

	maxRounds := 0
	if raw, ok := body["max_rounds"]; ok && raw != nil {
		n := intFromAny(raw)
		if n <= 0 {
			JSON(w, http.StatusBadRequest, map[string]any{
				"success": false, "error": t("api.maxRoundsPositive", nil),
			})
			return
		}
		maxRounds = n
	}

	if prepared, info := h.Manager.CheckPrepared(simulationID); !prepared {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success":      false,
			"error":        "la simulación no está preparada; ejecuta /api/simulation/prepare primero",
			"prepare_info": info,
		})
		return
	}

	simDir := h.Manager.SimulationDir(simulationID)
	cfg, err := oasis.LoadSimulationConfig(filepath.Join(simDir, "simulation_config.json"))
	if err != nil {
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false, "error": err.Error(),
		})
		return
	}
	if cfg.SimulationID != simulationID {
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   "simulation_config.json pertenece a otra simulación: " + cfg.SimulationID,
		})
		return
	}
	cfg.SimDir = simDir

	profiles, err := oasis.LoadTwitterProfiles(filepath.Join(simDir, "twitter_profiles.csv"))
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": err.Error(),
		})
		return
	}

	force := boolField(body, "force", false)
	run, err := h.Oasis.Start(cfg, profiles, maxRounds, force)
	if err != nil {
		code := http.StatusInternalServerError
		msg := err.Error()
		if strings.Contains(msg, "ya está en ejecución") || strings.Contains(msg, "ya se ejecutó") ||
			strings.Contains(msg, "ejecución previa") {
			code = http.StatusConflict
		} else if strings.Contains(msg, "perfiles") || strings.Contains(msg, "configuración") {
			code = http.StatusBadRequest
		}
		JSON(w, code, map[string]any{"success": false, "error": msg})
		return
	}

	state.Status = simx.SimRunning
	state.TwitterStatus = "running"
	state.RedditStatus = "not_started"
	state.CurrentRound = 0
	state.Error = nil
	h.Manager.SaveSimulationState(state)
	// El runner puede terminar antes de esta respuesta (max_rounds=1): no
	// sobrescribir su estado terminal con "running".
	if current, err := h.Oasis.State(simulationID); err == nil && current != nil {
		h.syncSimulationStatus(simulationID, current)
	}
	data := map[string]any{
		"simulation_id":               simulationID,
		"runner_status":               run.RunnerStatus,
		"process_pid":                 os.Getpid(),
		"twitter_running":             true,
		"reddit_running":              false,
		"platform_effective":          "twitter",
		"total_rounds":                run.TotalRounds,
		"started_at":                  run.StartedAt,
		"graph_memory_update_enabled": false,
		"force_restarted":             force,
	}
	if platform == "parallel" && state.EnableReddit {
		data["warning"] = "Reddit aún no está portado: esta corrida ejecuta solo Twitter. " +
			"Los totales de Reddit quedan en 0."
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// stopSimulation replica POST /stop: pide cierre y espera la ronda en curso.
func (h *SimulationHandler) stopSimulation(w http.ResponseWriter, r *http.Request) {
	body, err := decodeBody(r)
	if err != nil {
		h.HTTPBadRequest(w, "invalid JSON body: "+err.Error())
		return
	}
	simulationID := strings.TrimSpace(stringField(body, "simulation_id"))
	if simulationID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": "simulation_id es requerido",
		})
		return
	}

	run, err := h.Oasis.Stop(simulationID)
	if err != nil {
		state := h.Manager.GetSimulation(simulationID)
		if state == nil {
			JSON(w, http.StatusNotFound, map[string]any{
				"success": false, "error": "simulación no encontrada: " + simulationID,
			})
			return
		}
		// Timeout cerrando la ronda: el estado real ya es stopping.
		if strings.Contains(err.Error(), "sigue cerrando") {
			state.Status = simx.SimStopping
			state.TwitterStatus = "stopping"
			h.Manager.SaveSimulationState(state)
			JSON(w, http.StatusAccepted, map[string]any{
				"success": true, "pending": true,
				"data": map[string]any{
					"simulation_id": simulationID,
					"runner_status": "stopping",
				},
			})
			return
		}
		JSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}

	h.syncSimulationStatus(simulationID, run)
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"simulation_id":   simulationID,
			"runner_status":   run.RunnerStatus,
			"twitter_running": false,
			"reddit_running":  false,
			"stopped_at":      run.UpdatedAt,
		},
	})
}

// envStatus responde si el entorno puede recibir comandos (entrevistas) en
// esta rebanada: solo mientras el runner está vivo.
func (h *SimulationHandler) envStatus(w http.ResponseWriter, r *http.Request) {
	body, err := decodeBody(r)
	if err != nil {
		h.HTTPBadRequest(w, "invalid JSON body: "+err.Error())
		return
	}
	simulationID := strings.TrimSpace(stringField(body, "simulation_id"))
	if simulationID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": "simulation_id es requerido",
		})
		return
	}
	alive := h.Oasis.IsRunning(simulationID)
	message := "El entorno no está en ejecución."
	if alive {
		message = "El entorno está en ejecución."
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"simulation_id":     simulationID,
			"env_alive":         alive,
			"twitter_available": alive,
			"reddit_available":  false,
			"message":           message,
		},
	})
}

// runStatus replica GET /<id>/run-status con la forma idle del Python.
func (h *SimulationHandler) runStatus(w http.ResponseWriter, r *http.Request, simulationID string) {
	// Igual que el Python: un id sin ejecución responde idle, no 404. La UI
	// sondea este endpoint mientras arranca la simulación.
	run, err := h.Oasis.State(simulationID)
	if err != nil {
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if run == nil {
		JSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data": map[string]any{
				"simulation_id":         simulationID,
				"runner_status":         "idle",
				"current_round":         0,
				"total_rounds":          0,
				"progress_percent":      0,
				"twitter_running":       false,
				"reddit_running":        false,
				"twitter_actions_count": 0,
				"reddit_actions_count":  0,
				"total_actions_count":   0,
			},
		})
		return
	}
	h.syncSimulationStatus(simulationID, run)
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": run.ToMap()})
}

// runStatusDetail añade las acciones a run-status.
func (h *SimulationHandler) runStatusDetail(w http.ResponseWriter, r *http.Request, simulationID string) {
	run, err := h.Oasis.State(simulationID)
	if err != nil {
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	platform := r.URL.Query().Get("platform")
	if platform == "parallel" {
		platform = ""
	}
	if run == nil {
		JSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data": map[string]any{
				"simulation_id":   simulationID,
				"runner_status":   "idle",
				"all_actions":     []any{},
				"twitter_actions": []any{},
				"reddit_actions":  []any{},
			},
		})
		return
	}
	h.syncSimulationStatus(simulationID, run)

	all, err := h.Oasis.Actions(simulationID, 1000, 0, platform, nil, nil)
	if err != nil {
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	twitterActions := []oasis.ActionRecord{}
	redditActions := []oasis.ActionRecord{}
	if platform == "" || platform == "twitter" {
		twitterActions = all
	}
	recent := []oasis.ActionRecord{}
	if run.CurrentRound > 0 {
		recent, err = h.Oasis.Actions(simulationID, 1000, 0, platform, nil, &run.CurrentRound)
		if err != nil {
			JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
	}

	data := run.ToMap()
	data["all_actions"] = toActionMaps(all)
	data["twitter_actions"] = toActionMaps(twitterActions)
	data["reddit_actions"] = toActionMaps(redditActions)
	data["recent_actions"] = toActionMaps(recent)
	data["rounds_count"] = run.CurrentRound
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// simulationActions replica GET /<id>/actions con filtros.
func (h *SimulationHandler) simulationActions(w http.ResponseWriter, r *http.Request, simulationID string) {
	query := r.URL.Query()
	limit := 100
	if n, err := strconv.Atoi(query.Get("limit")); err == nil && n > 0 {
		limit = n
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := 0
	if n, err := strconv.Atoi(query.Get("offset")); err == nil && n > 0 {
		offset = n
	}
	var agentID *int
	if raw := query.Get("agent_id"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			agentID = &n
		}
	}
	var roundNum *int
	if raw := query.Get("round_num"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			roundNum = &n
		}
	}

	platform := query.Get("platform")
	if platform == "parallel" {
		platform = ""
	}
	actions, err := h.Oasis.Actions(simulationID, limit, offset, platform, agentID, roundNum)
	if err != nil {
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"count":   len(actions),
			"actions": toActionMaps(actions),
		},
	})
}

// syncSimulationStatus refleja el run_state en state.json una sola vez por
// cambio, para que /api/simulation/<id> y el historial cuenten la verdad.
func (h *SimulationHandler) syncSimulationStatus(simulationID string, run *oasis.RunState) {
	if run == nil {
		return
	}
	state := h.Manager.GetSimulation(simulationID)
	if state == nil {
		return
	}
	status := state.Status
	switch run.RunnerStatus {
	case "running":
		status = simx.SimRunning
	case "stopping":
		status = simx.SimStopping
	case "paused":
		status = simx.SimPaused
	case "completed":
		status = simx.SimCompleted
	case "stopped":
		status = simx.SimStopped
	case "failed":
		status = simx.SimFailed
	}
	if state.Status == status && state.CurrentRound == run.CurrentRound &&
		state.TwitterStatus == run.RunnerStatus {
		return
	}
	state.Status = status
	state.CurrentRound = run.CurrentRound
	state.TwitterStatus = run.RunnerStatus
	if run.Error != nil {
		state.Error = run.Error
	}
	h.Manager.SaveSimulationState(state)
}

func toActionMaps(records []oasis.ActionRecord) []map[string]any {
	out := make([]map[string]any, 0, len(records))
	for _, record := range records {
		out = append(out, record.ToMap())
	}
	return out
}

// HTTPBadRequest centraliza el error JSON de cuerpo inválido.
func (h *SimulationHandler) HTTPBadRequest(w http.ResponseWriter, message string) {
	JSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": message})
}

// ===== generación de perfiles sin crear simulación =====

func (h *SimulationHandler) generateProfiles(w http.ResponseWriter, r *http.Request) {
	loc := h.localeOf(r)
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	body, err := decodeBody(r)
	if err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	graphID := strings.TrimSpace(stringField(body, "graph_id"))
	if graphID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.requireGraphId", nil),
		})
		return
	}
	entityTypes := stringSliceField(body, "entity_types")
	useLLM := boolField(body, "use_llm", true)
	platform := stringField(body, "platform")
	if platform == "" {
		platform = "reddit"
	}

	reader := simx.NewEntityReader(h.Client)
	filtered, err := reader.FilterDefinedEntities(graphID, entityTypes, true)
	if err != nil {
		h.Logger.Error("生成Profile失败", "graph_id", graphID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false, "error": err.Error(),
		})
		return
	}
	if filtered.FilteredCount == 0 {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.noMatchingEntities", nil),
		})
		return
	}

	generator := simx.NewProfileGenerator(h.Client, graphID)
	if h.ProfileFactory != nil {
		generator = h.ProfileFactory(h.Client, graphID)
	}
	profiles := generator.GenerateProfilesFromEntities(filtered.Entities, useLLM, nil, graphID, 5, "", "")

	profilesData := make([]map[string]any, 0, len(profiles))
	for _, p := range profiles {
		switch platform {
		case "reddit":
			profilesData = append(profilesData, p.ToRedditFormat())
		case "twitter":
			profilesData = append(profilesData, p.ToTwitterFormat())
		default:
			profilesData = append(profilesData, p.ToDict())
		}
	}
	types := append([]string(nil), filtered.EntityTypes...)
	sort.Strings(types)

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"platform":     platform,
			"entity_types": types,
			"count":        len(profilesData),
			"profiles":     profilesData,
		},
	})
}
