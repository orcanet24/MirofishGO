// Port de backend/app/api/report.py — blueprint /api/report (18 rutas).
//
// Generación asíncrona con task_id + progreso en progress.json, streaming de
// logs por polling (from_line/total_lines), capítulos servidos en cuanto se
// guardan y chat con el agente sobre el informe.
//
// Despacho manual sobre "GET|POST|DELETE /api/report/" por el mismo motivo que
// /api/simulation: formas como /{report_id}/download y /by-simulation/{id}
// colisionan si se registran como patrones separados de ServeMux.
package httpx

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mirofish/internal/config"
	"mirofish/internal/graph"
	"mirofish/internal/locale"
	"mirofish/internal/models"
	"mirofish/internal/report"
	"mirofish/internal/simx"
)

// ReportAgentFactory permite inyectar el agente en tests.
type ReportAgentFactory func(graphID, simulationID, requirement, userID, localeCode string) *report.Agent

// ReportHandler concentra el estado del blueprint de reportes.
type ReportHandler struct {
	Cfg      *config.Config
	PM       *models.ProjectManager
	Sim      *simx.Manager
	Client   *graph.Client
	Reports  *report.Manager
	Tools    *report.Tools
	Logger   *slog.Logger
	NewAgent ReportAgentFactory
}

// ReportHandlerOption configura el handler (tests).
type ReportHandlerOption func(*ReportHandler)

// WithReportGraphClient fija el cliente de grafo local.
func WithReportGraphClient(c *graph.Client) ReportHandlerOption {
	return func(h *ReportHandler) {
		h.Client = c
		h.Tools = report.NewTools(c)
	}
}

// WithReportSimulationManager fija el gestor de simulaciones.
func WithReportSimulationManager(m *simx.Manager) ReportHandlerOption {
	return func(h *ReportHandler) { h.Sim = m }
}

// WithAgentFactory inyecta la fábrica del agente.
func WithAgentFactory(f ReportAgentFactory) ReportHandlerOption {
	return func(h *ReportHandler) { h.NewAgent = f }
}

// NewReportHandler crea el handler con sus dependencias.
func NewReportHandler(cfg *config.Config, logger *slog.Logger, opts ...ReportHandlerOption) (*ReportHandler, error) {
	simDir := cfg.SimDataDir
	if simDir == "" {
		simDir = filepath.Join(cfg.UploadDir, "simulations")
	}
	h := &ReportHandler{
		Cfg:     cfg,
		PM:      models.NewProjectManager(cfg.UploadDir),
		Sim:     simx.NewManager(simDir),
		Reports: report.NewManager(cfg.UploadDir),
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
	if h.Tools == nil {
		h.Tools = report.NewTools(h.Client)
	}
	if h.NewAgent == nil {
		h.NewAgent = func(graphID, simulationID, requirement, userID, localeCode string) *report.Agent {
			return report.NewAgent(h.Reports, h.Tools, graphID, simulationID, requirement, userID, localeCode)
		}
	}
	return h, nil
}

// Register monta el blueprint en el mux.
func (h *ReportHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/report/", h.handleGET)
	mux.HandleFunc("POST /api/report/", h.handlePOST)
	mux.HandleFunc("DELETE /api/report/", h.handleDELETE)
}

func (h *ReportHandler) t(r *http.Request, key string, args map[string]string) string {
	return locale.T(locale.Normalize(r.Header.Get("Accept-Language")), key, args)
}

// ===== despacho =====

func (h *ReportHandler) handleGET(w http.ResponseWriter, r *http.Request) {
	segs := pathSegments(r.URL.Path, "/api/report/")
	if len(segs) == 0 {
		HTTPError(w, http.StatusNotFound, "not found")
		return
	}

	switch {
	case len(segs) == 1 && segs[0] == "list":
		h.listReports(w, r)
	case len(segs) == 2 && segs[0] == "by-simulation":
		h.reportBySimulation(w, r, segs[1])
	case len(segs) == 2 && segs[0] == "check":
		h.checkReport(w, r, segs[1])
	case len(segs) == 1:
		h.getReport(w, r, segs[0])

	case len(segs) == 2 && segs[1] == "download":
		h.downloadReport(w, r, segs[0])
	case len(segs) == 2 && segs[1] == "progress":
		h.reportProgress(w, r, segs[0])
	case len(segs) == 2 && segs[1] == "sections":
		h.reportSections(w, r, segs[0])
	case len(segs) == 3 && segs[1] == "section":
		index, err := strconv.Atoi(segs[2])
		if err != nil {
			HTTPError(w, http.StatusNotFound, "not found")
			return
		}
		h.singleSection(w, r, segs[0], index)
	case len(segs) == 3 && segs[1] == "agent-log" && segs[2] == "stream":
		h.agentLogStream(w, r, segs[0])
	case len(segs) == 2 && segs[1] == "agent-log":
		h.agentLog(w, r, segs[0])
	case len(segs) == 3 && segs[1] == "console-log" && segs[2] == "stream":
		h.consoleLogStream(w, r, segs[0])
	case len(segs) == 2 && segs[1] == "console-log":
		h.consoleLog(w, r, segs[0])
	default:
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   h.t(r, "api.reportNotFound", map[string]string{"id": segs[0]}),
		})
	}
}

func (h *ReportHandler) handlePOST(w http.ResponseWriter, r *http.Request) {
	segs := pathSegments(r.URL.Path, "/api/report/")
	switch {
	case len(segs) == 1 && segs[0] == "generate":
		h.generateReport(w, r)
	case len(segs) == 2 && segs[0] == "generate" && segs[1] == "status":
		h.generateStatus(w, r)
	case len(segs) == 1 && segs[0] == "chat":
		h.chat(w, r)
	case len(segs) == 2 && segs[0] == "tools" && segs[1] == "search":
		h.toolsSearch(w, r)
	case len(segs) == 2 && segs[0] == "tools" && segs[1] == "statistics":
		h.toolsStatistics(w, r)
	default:
		HTTPError(w, http.StatusNotFound, "not found")
	}
}

func (h *ReportHandler) handleDELETE(w http.ResponseWriter, r *http.Request) {
	segs := pathSegments(r.URL.Path, "/api/report/")
	if len(segs) != 1 {
		HTTPError(w, http.StatusNotFound, "not found")
		return
	}
	if !h.Reports.DeleteReport(segs[0]) {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   h.t(r, "api.reportNotFound", map[string]string{"id": segs[0]}),
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": h.t(r, "api.reportDeleted", map[string]string{"id": segs[0]}),
	})
}

// ===== generación =====

func (h *ReportHandler) generateReport(w http.ResponseWriter, r *http.Request) {
	loc := locale.Normalize(r.Header.Get("Accept-Language"))
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
	if raw, exists := body["force_regenerate"]; exists {
		if _, ok := raw.(bool); !ok {
			JSON(w, http.StatusBadRequest, map[string]any{
				"success": false, "error": "force_regenerate must be a JSON boolean",
			})
			return
		}
	}
	forceRegenerate := boolField(body, "force_regenerate", false)

	state := h.Sim.GetSimulation(simulationID)
	if state == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false, "error": t("api.simulationNotFound", map[string]string{"id": simulationID}),
		})
		return
	}

	// El informe exige una ejecución terminada correctamente: sin runner en Go,
	// run_state.json lo escribe el runner Python o el runner Go futuro.
	runState := readRunState(h.Sim.SimulationDir(simulationID))
	runnerStatus := ""
	if runState != nil {
		runnerStatus = stringField(runState, "runner_status")
	}
	switch runnerStatus {
	case "starting", "running", "paused", "stopping":
		JSON(w, http.StatusConflict, map[string]any{
			"success": false,
			"error": "Simulation or Zep graph ingestion is still active; " +
				"wait for a terminal run status before generating a report",
			"ingestion_pending": false,
		})
		return
	case "completed", "stopped":
		// ok
	default:
		JSON(w, http.StatusConflict, map[string]any{
			"success": false,
			"error": "A successfully completed or stopped simulation is required " +
				"before generating a report",
		})
		return
	}

	project := h.PM.GetProject(state.ProjectID)
	if project == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false, "error": t("api.projectNotFound", map[string]string{"id": state.ProjectID}),
		})
		return
	}
	if project.Status != models.ProjectGraphCompleted {
		JSON(w, http.StatusConflict, map[string]any{
			"success": false,
			"error":   "The project graph must be completely built before reporting",
		})
		return
	}
	graphID := deref(project.GraphID)
	if graphID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.missingGraphIdEnsure", nil),
		})
		return
	}
	if state.GraphID != "" && state.GraphID != graphID {
		JSON(w, http.StatusConflict, map[string]any{
			"success": false,
			"error": "The simulation references an older graph; prepare it again " +
				"before generating a report",
		})
		return
	}
	requirement := ""
	if project.SimulationRequirement != nil {
		requirement = strings.TrimSpace(*project.SimulationRequirement)
	}
	if requirement == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.missingSimRequirement", nil),
		})
		return
	}

	if !forceRegenerate {
		if existing := h.Reports.GetReportBySimulation(simulationID); existing != nil && existing.Status == report.StatusCompleted {
			JSON(w, http.StatusOK, map[string]any{
				"success": true,
				"data": map[string]any{
					"simulation_id":     simulationID,
					"report_id":         existing.ReportID,
					"status":            "completed",
					"message":           t("api.reportAlreadyExists", nil),
					"already_generated": true,
				},
			})
			return
		}
	}

	reportID := report.NewReportID()
	taskID := models.GetTaskManager().CreateTask("report_generate", map[string]any{
		"simulation_id": simulationID,
		"graph_id":      graphID,
		"report_id":     reportID,
	})

	go h.runGenerate(taskID, reportID, simulationID, graphID, requirement, loc)

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"simulation_id":     simulationID,
			"report_id":         reportID,
			"task_id":           taskID,
			"status":            "generating",
			"message":           t("api.reportGenerateStarted", nil),
			"already_generated": false,
		},
	})
}

func (h *ReportHandler) runGenerate(taskID, reportID, simulationID, graphID, requirement, loc string) {
	taskManager := models.GetTaskManager()
	logger := h.Logger.With("task_id", taskID, "report_id", reportID)

	status := models.TaskProcessing
	startMsg := locale.T(loc, "api.initReportAgent", nil)
	taskManager.UpdateTask(taskID, models.TaskUpdate{Status: &status, Progress: intPtr(0), Message: &startMsg})

	agent := h.NewAgent(graphID, simulationID, requirement, "", loc)
	if agent.Manager == nil {
		agent.Manager = h.Reports
	}
	if agent.Tools == nil {
		agent.Tools = h.Tools
	}

	reportResult := agent.GenerateReport(func(stage string, progress int, message string) {
		msg := "[" + stage + "] " + message
		taskManager.UpdateTask(taskID, models.TaskUpdate{Progress: &progress, Message: &msg})
	}, reportID)

	if err := h.Reports.SaveReport(reportResult); err != nil {
		logger.Error("no se pudo guardar el informe", "error", err)
	}
	if reportResult.Status == report.StatusCompleted {
		taskManager.CompleteTask(taskID, map[string]any{
			"report_id":     reportResult.ReportID,
			"simulation_id": simulationID,
			"status":        "completed",
		})
		logger.Info("informe completado")
		return
	}
	errMsg := locale.T(loc, "api.reportGenerateFailed", nil)
	if reportResult.Error != nil && *reportResult.Error != "" {
		errMsg = *reportResult.Error
	}
	taskManager.FailTask(taskID, errMsg)
	logger.Error("informe fallido", "error", errMsg)
}

func (h *ReportHandler) generateStatus(w http.ResponseWriter, r *http.Request) {
	loc := locale.Normalize(r.Header.Get("Accept-Language"))
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	body, err := decodeBody(r)
	if err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	taskID := strings.TrimSpace(stringField(body, "task_id"))
	simulationID := strings.TrimSpace(stringField(body, "simulation_id"))

	if simulationID != "" {
		if existing := h.Reports.GetReportBySimulation(simulationID); existing != nil && existing.Status == report.StatusCompleted {
			JSON(w, http.StatusOK, map[string]any{
				"success": true,
				"data": map[string]any{
					"simulation_id":     simulationID,
					"report_id":         existing.ReportID,
					"status":            "completed",
					"progress":          100,
					"message":           t("api.reportGenerated", nil),
					"already_completed": true,
				},
			})
			return
		}
	}
	if taskID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.requireTaskOrSimId", nil),
		})
		return
	}
	task, ok := models.GetTaskManager().GetTask(taskID)
	if !ok {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false, "error": t("api.taskNotFound", map[string]string{"id": taskID}),
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": task.ToDict()})
}

// ===== consulta =====

func (h *ReportHandler) getReport(w http.ResponseWriter, r *http.Request, reportID string) {
	rep := h.Reports.GetReport(reportID)
	if rep == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   h.t(r, "api.reportNotFound", map[string]string{"id": reportID}),
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": rep.ToDict()})
}

func (h *ReportHandler) reportBySimulation(w http.ResponseWriter, r *http.Request, simulationID string) {
	rep := h.Reports.GetReportBySimulation(simulationID)
	if rep == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success":    false,
			"error":      h.t(r, "api.noReportForSim", map[string]string{"id": simulationID}),
			"has_report": false,
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"data":       rep.ToDict(),
		"has_report": true,
	})
}

func (h *ReportHandler) listReports(w http.ResponseWriter, r *http.Request) {
	simulationID := r.URL.Query().Get("simulation_id")
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			limit = n
		}
	}
	reports := h.Reports.ListReports(simulationID, limit)
	data := make([]map[string]any, 0, len(reports))
	for _, rep := range reports {
		data = append(data, rep.ToDict())
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    data,
		"count":   len(data),
	})
}

func (h *ReportHandler) downloadReport(w http.ResponseWriter, r *http.Request, reportID string) {
	rep := h.Reports.GetReport(reportID)
	if rep == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   h.t(r, "api.reportNotFound", map[string]string{"id": reportID}),
		})
		return
	}
	content := rep.MarkdownContent
	if raw, err := os.ReadFile(h.Reports.MarkdownPath(reportID)); err == nil {
		content = string(raw)
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+reportID+`.md"`)
	_, _ = w.Write([]byte(content))
}

func (h *ReportHandler) reportProgress(w http.ResponseWriter, r *http.Request, reportID string) {
	progress := h.Reports.GetProgress(reportID)
	if progress == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   h.t(r, "api.reportProgressNotAvail", map[string]string{"id": reportID}),
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": progress})
}

func (h *ReportHandler) reportSections(w http.ResponseWriter, r *http.Request, reportID string) {
	sections := h.Reports.GetGeneratedSections(reportID)
	rep := h.Reports.GetReport(reportID)
	isComplete := rep != nil && rep.Status == report.StatusCompleted
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"report_id":      reportID,
			"sections":       sections,
			"total_sections": len(sections),
			"is_complete":    isComplete,
		},
	})
}

func (h *ReportHandler) singleSection(w http.ResponseWriter, r *http.Request, reportID string, index int) {
	path := h.Reports.SectionPath(reportID, index)
	content, err := os.ReadFile(path)
	if err != nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   h.t(r, "api.sectionNotFound", map[string]string{"index": strconv.Itoa(index)}),
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"filename":      filepath.Base(path),
			"section_index": index,
			"content":       string(content),
		},
	})
}

func (h *ReportHandler) checkReport(w http.ResponseWriter, r *http.Request, simulationID string) {
	rep := h.Reports.GetReportBySimulation(simulationID)
	var status, id any
	if rep != nil {
		status = string(rep.Status)
		id = rep.ReportID
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"simulation_id":      simulationID,
			"has_report":         rep != nil,
			"report_status":      status,
			"report_id":          id,
			"interview_unlocked": rep != nil && rep.Status == report.StatusCompleted,
		},
	})
}

// ===== logs (streaming por polling) =====

func fromLineParam(r *http.Request) int {
	if raw := r.URL.Query().Get("from_line"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func (h *ReportHandler) agentLog(w http.ResponseWriter, r *http.Request, reportID string) {
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    h.Reports.GetAgentLog(reportID, fromLineParam(r)),
	})
}

func (h *ReportHandler) agentLogStream(w http.ResponseWriter, r *http.Request, reportID string) {
	logs := h.Reports.GetAgentLogStream(reportID)
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    map[string]any{"logs": logs, "count": len(logs)},
	})
}

func (h *ReportHandler) consoleLog(w http.ResponseWriter, r *http.Request, reportID string) {
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    h.Reports.GetConsoleLog(reportID, fromLineParam(r)),
	})
}

func (h *ReportHandler) consoleLogStream(w http.ResponseWriter, r *http.Request, reportID string) {
	logs := h.Reports.GetConsoleLogStream(reportID)
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    map[string]any{"logs": logs, "count": len(logs)},
	})
}

// ===== chat =====

func (h *ReportHandler) chat(w http.ResponseWriter, r *http.Request) {
	loc := locale.Normalize(r.Header.Get("Accept-Language"))
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	body, err := decodeBody(r)
	if err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	simulationID := strings.TrimSpace(stringField(body, "simulation_id"))
	message := strings.TrimSpace(stringField(body, "message"))
	if simulationID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.requireSimulationId", nil),
		})
		return
	}
	if message == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.requireMessage", nil),
		})
		return
	}

	state := h.Sim.GetSimulation(simulationID)
	if state == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false, "error": t("api.simulationNotFound", map[string]string{"id": simulationID}),
		})
		return
	}
	project := h.PM.GetProject(state.ProjectID)
	if project == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false, "error": t("api.projectNotFound", map[string]string{"id": state.ProjectID}),
		})
		return
	}
	graphID := state.GraphID
	if graphID == "" {
		graphID = deref(project.GraphID)
	}
	if graphID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.missingGraphId", nil),
		})
		return
	}
	requirement := ""
	if project.SimulationRequirement != nil {
		requirement = *project.SimulationRequirement
	}

	history := []map[string]any{}
	if raw, ok := body["chat_history"].([]any); ok {
		for _, item := range raw {
			if m, ok := item.(map[string]any); ok {
				history = append(history, m)
			}
		}
	}

	agent := h.NewAgent(graphID, simulationID, requirement, "", loc)
	if agent.Manager == nil {
		agent.Manager = h.Reports
	}
	if agent.Tools == nil {
		agent.Tools = h.Tools
	}
	result := agent.Chat(message, history)
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": result})
}

// ===== herramientas de depuración =====

func (h *ReportHandler) toolsSearch(w http.ResponseWriter, r *http.Request) {
	loc := locale.Normalize(r.Header.Get("Accept-Language"))
	body, err := decodeBody(r)
	if err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	graphID := strings.TrimSpace(stringField(body, "graph_id"))
	query := strings.TrimSpace(stringField(body, "query"))
	if graphID == "" || query == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": locale.T(loc, "api.requireGraphIdAndQuery", nil),
		})
		return
	}
	limit := 10
	if n := intFromAny(body["limit"]); n > 0 {
		limit = n
	}
	result := h.Tools.SearchGraph(graphID, query, limit, "both")
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": result.ToDict()})
}

func (h *ReportHandler) toolsStatistics(w http.ResponseWriter, r *http.Request) {
	loc := locale.Normalize(r.Header.Get("Accept-Language"))
	body, err := decodeBody(r)
	if err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	graphID := strings.TrimSpace(stringField(body, "graph_id"))
	if graphID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": locale.T(loc, "api.requireGraphId", nil),
		})
		return
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": h.Tools.GraphStatistics(graphID)})
}
