// Port de backend/app/api/graph.py — blueprint /api/graph (10 rutas).
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mirofish/internal/config"
	"mirofish/internal/graph"
	"mirofish/internal/graphbuilder"
	"mirofish/internal/llm"
	"mirofish/internal/locale"
	"mirofish/internal/models"
	"mirofish/internal/oasis"
	"mirofish/internal/ontology"
	tex "mirofish/internal/text"
	"mirofish/internal/report"
	"mirofish/internal/simx"
	"mirofish/internal/users"
)

// GraphHandler concentra el estado del blueprint de grafo.
type GraphHandler struct {
	Cfg    *config.Config
	PM     *models.ProjectManager
	Client *graph.Client
	Logger *slog.Logger

	// UsersStore — créditos/"estudios" por usuario (nil en tests o sin BD).
	UsersStore *users.Store

	// Dependencias para el borrado total (nil en tests).
	Sim        *simx.Manager
	OasisStore *oasis.Store
	Reports    *report.Manager

	// GeneratorFactory crea el generador de ontología; inyectable en tests.
	GeneratorFactory func() *ontology.Generator
}

// ===== aislamiento por usuario =====

// ownsProject — admin/master/anónimo ve todo; un RoleUser solo lo suyo.
// Proyectos legacy sin user_id son solo de administradores.
func (h *GraphHandler) ownsProject(r *http.Request, project *models.Project) bool {
	uid := scopeID(r)
	if uid == "" {
		return true
	}
	return project.UserID != nil && *project.UserID == uid
}

// denyUnlessOwner responde 404 (como si no existiera: no filtra IDs) y
// devuelve false si al principal no le toca el proyecto.
func (h *GraphHandler) denyUnlessOwner(r *http.Request, w http.ResponseWriter, project *models.Project) bool {
	if h.ownsProject(r, project) {
		return true
	}
	JSON(w, http.StatusNotFound, map[string]any{
		"success": false,
		"error":   "not found",
	})
	return false
}

// ownerOfGraph — el grafo es visible si algún proyecto del principal lo usa.
func (h *GraphHandler) ownerOfGraph(r *http.Request, graphID string) bool {
	uid := scopeID(r)
	if uid == "" {
		return true
	}
	for _, p := range h.PM.FindProjectsByGraphId(graphID) {
		if p.UserID != nil && *p.UserID == uid {
			return true
		}
	}
	return false
}

// denyUnlessGraphOwner — 404 si el grafo no es del principal.
func (h *GraphHandler) denyUnlessGraphOwner(r *http.Request, w http.ResponseWriter, graphID string) bool {
	if h.ownerOfGraph(r, graphID) {
		return true
	}
	JSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "not found"})
	return false
}

// ownsTask — una tarea del TaskManager es del usuario si su project_id
// (o la simulación que referencia) le pertenece.
func (h *GraphHandler) ownsTask(r *http.Request, t *models.TaskDict) bool {
	uid := scopeID(r)
	if uid == "" || t == nil {
		return uid == ""
	}
	if projectID, ok := t.Metadata["project_id"].(string); ok && projectID != "" {
		p := h.PM.GetProject(projectID)
		return p != nil && p.UserID != nil && *p.UserID == uid
	}
	return false
}

// consumeEstudio — descuenta 1 crédito del RoleUser tras un estudio exitoso.
// Silencioso para admin/master/anónimo.
func (h *GraphHandler) consumeEstudio(r *http.Request, projectID string) {
	uid := scopeID(r)
	if uid == "" || h.UsersStore == nil {
		return
	}
	if err := h.UsersStore.ConsumeCredit(uid); err != nil {
		h.Logger.Error("crédito no consumido", "user", uid, "project", projectID, "error", err)
	}
}

// prechargeGuard — rechaza 402 antes del trabajo pesado si RoleUser no tiene
// créditos. Admin/master/anónimo siempre pasa.
func (h *GraphHandler) prechargeGuard(r *http.Request, w http.ResponseWriter, loc string) bool {
	uid := scopeID(r)
	if uid == "" || h.UsersStore == nil {
		return true
	}
	u, err := h.UsersStore.Get(uid)
	if err != nil {
		return true // el middleware ya validó identidad; BD inestable no bloquea
	}
	if u.Credits <= 0 {
		JSON(w, http.StatusPaymentRequired, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.noCredits", nil),
		})
		return false
	}
	return true
}

// GraphHandlerOption configura el handler (tests).
type GraphHandlerOption func(*GraphHandler)

// WithGraphClient fija el cliente de grafo local (tests con BD temporal).
func WithGraphClient(c *graph.Client) GraphHandlerOption {
	return func(h *GraphHandler) { h.Client = c }
}

// NewGraphHandler crea el handler con su gestor de proyectos y cliente local.
func NewGraphHandler(cfg *config.Config, logger *slog.Logger, opts ...GraphHandlerOption) (*GraphHandler, error) {
	h := &GraphHandler{
		Cfg:    cfg,
		PM:     models.NewProjectManager(cfg.UploadDir),
		Logger: logger,
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
	return h, nil
}

// generator resuelve el generador de ontología por petición. Si el usuario
// trae config LLM delegada, el generador la usa; si no, la global. El locale
// de la petición define el idioma de los prompts del agente.
func (h *GraphHandler) generator(r *http.Request) *ontology.Generator {
	var g *ontology.Generator
	if h.GeneratorFactory != nil {
		g = h.GeneratorFactory()
	} else {
		g = ontology.NewGenerator()
	}
	if g.Locale == "" {
		g.Locale = "es"
	}
	if r != nil {
		g.Locale = locale.Normalize(r.Header.Get("Accept-Language"))
	}
	if f := llmFactory(r); f != nil {
		g.LLM = f
	}
	return g
}

// Register monta las 10 rutas del blueprint en el mux.
func (h *GraphHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/graph/project/list", h.listProjects)
	mux.HandleFunc("GET /api/graph/project/{project_id}", h.getProject)
	mux.HandleFunc("DELETE /api/graph/project/{project_id}", h.deleteProject)
	mux.HandleFunc("DELETE /api/graph/project/{project_id}/purge", h.purgeProject)
	mux.HandleFunc("POST /api/graph/purge-all", h.purgeAll)
	mux.HandleFunc("POST /api/graph/project/{project_id}/reset", h.resetProject)
	mux.HandleFunc("POST /api/graph/ontology/generate", h.generateOntology)
	mux.HandleFunc("POST /api/graph/build", h.buildGraph)
	mux.HandleFunc("GET /api/graph/task/{task_id}", h.getTask)
	mux.HandleFunc("GET /api/graph/tasks", h.listTasks)
	mux.HandleFunc("GET /api/graph/data/{graph_id}", h.getGraphData)
	mux.HandleFunc("DELETE /api/graph/delete/{graph_id}", h.deleteGraph)
}

// ===== locks por grafo/proyecto (equivalente a zep_lifecycle + _build_locks) =====

var (
	graphLocksRegistry = struct {
		sync.Mutex
		m map[string]*sync.RWMutex
	}{m: map[string]*sync.RWMutex{}}

	buildLocksRegistry = struct {
		sync.Mutex
		m map[string]*sync.Mutex
	}{m: map[string]*sync.Mutex{}}
)

func graphLock(graphID string) *sync.RWMutex {
	graphLocksRegistry.Lock()
	defer graphLocksRegistry.Unlock()
	l, ok := graphLocksRegistry.m[graphID]
	if !ok {
		l = &sync.RWMutex{}
		graphLocksRegistry.m[graphID] = l
	}
	return l
}

func buildLock(projectID string) *sync.Mutex {
	buildLocksRegistry.Lock()
	defer buildLocksRegistry.Unlock()
	l, ok := buildLocksRegistry.m[projectID]
	if !ok {
		l = &sync.Mutex{}
		buildLocksRegistry.m[projectID] = l
	}
	return l
}

// delBuildLock libera la entrada de un proyecto borrado (evita crecimiento
// ilimitado del registro global).
func delBuildLock(projectID string) {
	buildLocksRegistry.Lock()
	defer buildLocksRegistry.Unlock()
	delete(buildLocksRegistry.m, projectID)
}

// delGraphLock libera la entrada de un grafo borrado.
func delGraphLock(graphID string) {
	graphLocksRegistry.Lock()
	defer graphLocksRegistry.Unlock()
	delete(graphLocksRegistry.m, graphID)
}

// GraphInUseError replica la excepción del api para grafos con consumidores.
type GraphInUseError struct{ Msg string }

func (e *GraphInUseError) Error() string { return e.Msg }

// activeGraphConsumers replica _active_graph_consumers. Hasta el hito 5 no
// existen simulaciones corriendo, por lo que no hay consumidores activos.
func (h *GraphHandler) activeGraphConsumers(graphID string) []string {
	return []string{}
}

// deleteGraphIfPresent replica _delete_cloud_graph_if_present: el llamador
// DEBE mantener graphLock(graphID) (en Python el RLock es reentrante).
func (h *GraphHandler) deleteGraphIfPresent(graphID string) error {
	if graphID == "" {
		return nil
	}
	if active := h.activeGraphConsumers(graphID); len(active) > 0 {
		return &GraphInUseError{Msg: fmt.Sprintf(
			"Graph %s is in use by active consumer(s): %s",
			graphID, strings.Join(active, ", "))}
	}
	builder := graphbuilder.New(h.Client)
	if err := builder.DeleteGraph(graphID); err != nil {
		var notFound *graph.NotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return err
	}
	return nil
}

// projectHasActiveBuild replica _project_has_active_build.
func (h *GraphHandler) projectHasActiveBuild(project *models.Project) bool {
	if project.Status != models.ProjectGraphBuilding {
		return false
	}
	if project.GraphBuildTask == nil {
		return false
	}
	task, ok := models.GetTaskManager().GetTask(*project.GraphBuildTask)
	return ok && (task.Status == models.TaskPending || task.Status == models.TaskProcessing)
}

// allowedFile replica allowed_file del blueprint.
func (h *GraphHandler) allowedFile(filename string) bool {
	if filename == "" || !strings.Contains(filename, ".") {
		return false
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	for _, allowed := range h.Cfg.AllowedExtensions {
		if strings.ToLower(strings.TrimPrefix(allowed, ".")) == ext {
			return true
		}
	}
	return false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ===== Gestión de proyectos =====

func (h *GraphHandler) getProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("project_id")
	loc := locale.Normalize(r.Header.Get("Accept-Language"))

	project := h.PM.GetProject(projectID)
	if project == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.projectNotFound", map[string]string{"id": projectID}),
		})
		return
	}
	if !h.denyUnlessOwner(r, w, project) {
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    project.ToDict(),
	})
}

func (h *GraphHandler) listProjects(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}

	projects := h.PM.ListProjects(limit, scopeID(r))
	data := []map[string]any{}
	for _, p := range projects {
		data = append(data, p.ToDict())
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    data,
		"count":   len(data),
	})
}

func (h *GraphHandler) deleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("project_id")
	loc := locale.Normalize(r.Header.Get("Accept-Language"))

	lock := buildLock(projectID)
	lock.Lock()
	defer lock.Unlock()

	project := h.PM.GetProject(projectID)
	if project == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.projectNotFound", map[string]string{"id": projectID}),
		})
		return
	}
	if !h.denyUnlessOwner(r, w, project) {
		return
	}
	if h.projectHasActiveBuild(project) {
		JSON(w, http.StatusConflict, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.graphBuilding", nil),
		})
		return
	}

	graphID := deref(project.GraphID)
	if graphID != "" {
		gl := graphLock(graphID)
		gl.Lock()
		defer gl.Unlock()
		if err := h.deleteGraphIfPresent(graphID); err != nil {
			var inUse *GraphInUseError
			if errors.As(err, &inUse) {
				JSON(w, http.StatusConflict, map[string]any{"success": false, "error": err.Error()})
				return
			}
			JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
	}

	if !h.PM.DeleteProject(projectID) {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.projectDeleteFailed", map[string]string{"id": projectID}),
		})
		return
	}
	delBuildLock(projectID)
	if graphID != "" {
		delGraphLock(graphID)
	}

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": locale.T(loc, "api.projectDeleted", map[string]string{"id": projectID}),
	})
}

func (h *GraphHandler) resetProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("project_id")
	loc := locale.Normalize(r.Header.Get("Accept-Language"))

	lock := buildLock(projectID)
	lock.Lock()
	defer lock.Unlock()

	project := h.PM.GetProject(projectID)
	if project == nil {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.projectNotFound", map[string]string{"id": projectID}),
		})
		return
	}
	if !h.denyUnlessOwner(r, w, project) {
		return
	}
	if h.projectHasActiveBuild(project) {
		JSON(w, http.StatusConflict, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.graphBuilding", nil),
		})
		return
	}

	graphID := deref(project.GraphID)
	if graphID != "" {
		gl := graphLock(graphID)
		gl.Lock()
		defer gl.Unlock()
		if err := h.deleteGraphIfPresent(graphID); err != nil {
			var inUse *GraphInUseError
			if errors.As(err, &inUse) {
				JSON(w, http.StatusConflict, map[string]any{"success": false, "error": err.Error()})
				return
			}
			JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
	}

	if hasOntology(project.Ontology) {
		project.Status = models.ProjectOntologyGenerated
	} else {
		project.Status = models.ProjectCreated
	}
	clearProjectGraphReference(project)
	if err := h.PM.SaveProject(project); err != nil {
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": locale.T(loc, "api.projectReset", map[string]string{"id": projectID}),
		"data":    project.ToDict(),
	})
}

// hasOntology replica el truthiness de project.ontology del Python.
func hasOntology(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

// clearProjectGraphReference replica _clear_project_graph_reference.
func clearProjectGraphReference(p *models.Project) {
	p.GraphID = nil
	p.GraphBuildTask = nil
	p.ZepBatchID = nil
	p.ZepBatchOpID = nil
	p.Error = nil
}

// ===== Endpoint 1: subir archivo y generar ontología =====

func (h *GraphHandler) generateOntology(w http.ResponseWriter, r *http.Request) {
	loc := locale.Normalize(r.Header.Get("Accept-Language"))
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	// Un "estudio" arranca aquí (subida + LLM). Sin créditos → 402 antes de
	// gastar tokens del proveedor LLM.
	if !h.prechargeGuard(r, w, loc) {
		return
	}

	var project *models.Project
	maxSize := h.Cfg.MaxContentLength
	if maxSize <= 0 {
		maxSize = 16 << 20
	}
	// MaxBytesReader limita también peticiones con Transfer-Encoding: chunked
	// (ContentLength = -1), que antes pasaban el chequeo y se cargaban enteras
	// en memoria en ParseMultipartForm.
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			JSON(w, http.StatusRequestEntityTooLarge, map[string]any{
				"success": false, "error": "upload too large (max " + strconv.FormatInt(maxSize, 10) + " bytes)",
			})
			return
		}
		JSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}

	simulationRequirement := r.FormValue("simulation_requirement")
	projectName := r.FormValue("project_name")
	if projectName == "" {
		projectName = "Unnamed Project"
	}
	additionalContext := r.FormValue("additional_context")

	h.Logger.Info("=== Iniciando generación de la ontología ===")
	h.Logger.Debug("Nombre del proyecto: " + projectName)

	if simulationRequirement == "" {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.requireSimulationRequirement", nil),
		})
		return
	}

	files := []*multipart.FileHeader{}
	if r.MultipartForm != nil {
		for _, fh := range r.MultipartForm.File["files"] {
			if fh.Filename != "" {
				files = append(files, fh)
			}
		}
	}
	if len(files) == 0 {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.requireFileUpload", nil),
		})
		return
	}

	project, err := h.PM.CreateProject(projectName, scopeID(r))
	if err != nil {
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	project.SimulationRequirement = &simulationRequirement
	h.Logger.Info("Proyecto creado: " + project.ProjectID)

	documentTexts := []string{}
	allText := ""

	for _, fh := range files {
		if !h.allowedFile(fh.Filename) {
			continue
		}
		f, err := fh.Open()
		if err != nil {
			h.failOntologyProject(project, w, err, loc)
			return
		}
		fileInfo, err := h.PM.SaveFileToProject(project.ProjectID, fh.Filename, f)
		_ = f.Close()
		if err != nil {
			h.failOntologyProject(project, w, err, loc)
			return
		}

		originalFilename, _ := fileInfo["original_filename"].(string)
		size, _ := fileInfo["size"].(int64)
		project.Files = append(project.Files, models.SaveFileEntry(originalFilename, size))

		text, err := tex.ExtractText(strOr(fileInfo["path"]))
		if err != nil {
			h.failOntologyProject(project, w, err, loc)
			return
		}
		text = tex.PreprocessText(text)
		documentTexts = append(documentTexts, text)
		allText += fmt.Sprintf("\n\n=== %s ===\n%s", originalFilename, text)
	}

	if len(documentTexts) == 0 {
		h.PM.DeleteProject(project.ProjectID)
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.noDocProcessed", nil),
		})
		return
	}

	project.TotalTextLength = len([]rune(allText))
	h.PM.SaveExtractedText(project.ProjectID, allText)
	h.Logger.Info(fmt.Sprintf("Extracción de texto completada, %d caracteres", len([]rune(allText))))

	generator := h.generator(r)
	result, err := generator.Generate(documentTexts, simulationRequirement, additionalContext)
	if err != nil {
		h.failOntologyProject(project, w, err, loc)
		return
	}

	entityTypes, _ := result["entity_types"].([]any)
	edgeTypes, _ := result["edge_types"].([]any)
	h.Logger.Info(fmt.Sprintf("Ontología generada: %d tipos de entidad, %d tipos de relación", len(entityTypes), len(edgeTypes)))

	stored, _ := json.Marshal(orderedOntology{EntityTypes: entityTypes, EdgeTypes: edgeTypes})
	project.Ontology = stored
	summary, _ := result["analysis_summary"].(string)
	project.AnalysisSummary = &summary
	project.Status = models.ProjectOntologyGenerated
	if err := h.PM.SaveProject(project); err != nil {
		h.failOntologyProject(project, w, err, loc)
		return
	}
	h.Logger.Info("=== Ontología generada === ID del proyecto: " + project.ProjectID)

	// El estudio terminó: consume el crédito del usuario (RoleUser). Si no
	// tiene, ConsumeCredit falla pero el estudio YA está hecho: se registra
	// y el admin bloquea cuentas vencidas vía is_active/ads creditos.
	h.consumeEstudio(r, project.ProjectID)

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"project_id":        project.ProjectID,
			"project_name":      project.Name,
			"ontology":          orderedOntology{EntityTypes: entityTypes, EdgeTypes: edgeTypes},
			"analysis_summary":  project.AnalysisSummary,
			"files":             project.Files,
			"total_text_length": project.TotalTextLength,
		},
	})
}

// orderedOntology conserva el orden de claves del dict Python.
type orderedOntology struct {
	EntityTypes []any `json:"entity_types"`
	EdgeTypes   []any `json:"edge_types"`
}

// failOntologyProject replica el manejo de excepciones de generate_ontology:
// LLMResponseError → 502 str(error); provider con status → 502 sanitizado;
// resto → 500 genérico. El proyecto queda en estado failed.
func (h *GraphHandler) failOntologyProject(project *models.Project, w http.ResponseWriter, err error, loc string) {
	var respErr *llm.ResponseError
	var provErr *llm.ProviderError

	publicError := ""
	status := http.StatusOK
	switch {
	case errors.As(err, &respErr):
		publicError = err.Error()
		status = http.StatusBadGateway
		h.Logger.Error("LLM returned an unusable ontology response", "error", err)
	case errors.As(err, &provErr):
		publicError = fmt.Sprintf("LLM provider request failed (HTTP %d)", provErr.Status)
		if provErr.Raw != nil && provErr.Raw.ID != "" {
			safeRequestID := sanitizeRequestID(provErr.Raw.ID)
			if safeRequestID != "" {
				publicError += " (request_id: " + safeRequestID + ")"
			}
		}
		status = http.StatusBadGateway
		h.Logger.Error("Ontology provider request failed",
			"type", fmt.Sprintf("%T", err), "status", provErr.Status, "request_id", provErr.Raw != nil)
	default:
		publicError = "Ontology generation failed; check the server logs"
		status = http.StatusInternalServerError
		h.Logger.Error("Unexpected ontology generation failure", "error", err)
	}

	project.Status = models.ProjectFailed
	project.Error = &publicError
	if saveErr := h.PM.SaveProject(project); saveErr != nil {
		h.Logger.Error("Failed to persist ontology failure", "project_id", project.ProjectID, "error", saveErr)
	}

	JSON(w, status, map[string]any{
		"success": false,
		"error":   publicError,
		"data":    map[string]any{"project_id": project.ProjectID},
	})
}

func sanitizeRequestID(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == ':' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() > 128 {
		return b.String()[:128]
	}
	return b.String()
}

func strOr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ===== Endpoint 2: construir grafo =====

type buildRequestBody struct {
	ProjectID       string
	GraphName       string
	ChunkSize       float64
	ChunkOverlap    float64
	Force           bool
	HasForce        bool
	ForceInvalid    bool
	HasChunkSize    bool
	HasChunkOverlap bool
}

func parseBuildBody(body map[string]any) buildRequestBody {
	out := buildRequestBody{}
	if v, ok := body["project_id"].(string); ok {
		out.ProjectID = v
	}
	if v, ok := body["graph_name"].(string); ok {
		out.GraphName = v
	}
	if v, ok := body["chunk_size"]; ok {
		if f, ok := v.(float64); ok {
			out.ChunkSize = f
			out.HasChunkSize = true
		}
	}
	if v, ok := body["chunk_overlap"]; ok {
		if f, ok := v.(float64); ok {
			out.ChunkOverlap = f
			out.HasChunkOverlap = true
		}
	}
	if v, ok := body["force"]; ok {
		if b, isBool := v.(bool); isBool {
			out.Force = b
			out.HasForce = true
		} else {
			out.ForceInvalid = true
		}
	}
	return out
}

func (h *GraphHandler) buildGraph(w http.ResponseWriter, r *http.Request) {
	loc := locale.Normalize(r.Header.Get("Accept-Language"))
	body := map[string]any{}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			// Antes el error de decodificación se ignoraba y se seguía con
			// un cuerpo vacío, devolviendo "falta project_id" como si el
			// JSON fuera válido.
			HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	if body == nil {
		body = map[string]any{}
	}
	parsed := parseBuildBody(body)

	if parsed.ProjectID == "" {
		h.buildGraphImpl(w, r, loc, body, parsed, "")
		return
	}
	lock := buildLock(parsed.ProjectID)
	lock.Lock()
	defer lock.Unlock()
	h.buildGraphImpl(w, r, loc, body, parsed, parsed.ProjectID)
}

func (h *GraphHandler) buildGraphImpl(w http.ResponseWriter, r *http.Request, loc string, _ map[string]any, parsed buildRequestBody, projectID string) {
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	// Comprobar configuración (local: MySQL no requiere ZEP_API_KEY; zep: sí)
	if !h.Cfg.UseLocalGraph() && h.Cfg.ZEPAPIKey == "" {
		h.Logger.Error("Error de configuración: " + t("api.zepApiKeyMissing", nil))
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   t("api.configError", map[string]string{"details": t("api.zepApiKeyMissing", nil)}),
		})
		return
	}

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

	if !h.denyUnlessOwner(r, w, project) {
		return
	}

	if parsed.ForceInvalid {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": "force must be a JSON boolean",
		})
		return
	}
	force := parsed.Force

	if project.Status == models.ProjectCreated {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.ontologyNotGenerated", nil),
		})
		return
	}

	resumeExistingBatch := false
	if project.Status == models.ProjectGraphBuilding {
		if h.projectHasActiveBuild(project) {
			JSON(w, http.StatusOK, map[string]any{
				"success": true,
				"data": map[string]any{
					"project_id": projectID,
					"task_id":    deref(project.GraphBuildTask),
					"graph_id":   deref(project.GraphID),
					"reused":     true,
					"message":    t("api.graphBuilding", nil),
				},
			})
			return
		}

		if !force && project.GraphID != nil && project.ZepBatchID != nil && project.ZepBatchOpID != nil {
			builder := graphbuilder.New(h.Client)
			builder.Locale = loc
			batchSummary, err := builder.GetBatchSummary(*project.ZepBatchID)
			if err != nil {
				var notFound *graph.NotFoundError
				if !errors.As(err, &notFound) {
					h.Logger.Error("no se pudo leer el batch persistido", "error", err)
					JSON(w, http.StatusInternalServerError, map[string]any{
						"success": false, "error": err.Error(),
					})
					return
				}
			} else if batchSummary != nil {
				switch batchSummary.Status {
				case "queued", "processing", "succeeded":
					resumeExistingBatch = true
				}
			}
		}

		if !resumeExistingBatch {
			project.Status = models.ProjectFailed
			msg := "Graph build task is no longer present; the persisted Zep batch cannot be resumed automatically"
			project.Error = &msg
			if err := h.PM.SaveProject(project); err != nil {
				JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
				return
			}
			if !force {
				JSON(w, http.StatusConflict, map[string]any{
					"success":     false,
					"error":       msg,
					"task_id":     deref(project.GraphBuildTask),
					"recoverable": true,
				})
				return
			}
		}
	}

	if project.Status == models.ProjectGraphCompleted && !force {
		JSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data": map[string]any{
				"project_id": projectID,
				"task_id":    deref(project.GraphBuildTask),
				"graph_id":   deref(project.GraphID),
				"reused":     true,
				"message":    t("progress.graphBuildComplete", nil),
			},
		})
		return
	}

	graphName := parsed.GraphName
	if graphName == "" {
		graphName = project.Name
	}
	if graphName == "" {
		graphName = "MiroFish Graph"
	}

	chunkSize := 0
	if parsed.HasChunkSize {
		chunkSize = int(parsed.ChunkSize)
		if parsed.ChunkSize != float64(chunkSize) || chunkSize <= 0 {
			JSON(w, http.StatusBadRequest, map[string]any{
				"success": false, "error": "chunk_size must be a positive integer",
			})
			return
		}
	} else if project.ChunkSize > 0 {
		chunkSize = project.ChunkSize
	} else {
		chunkSize = h.Cfg.DefaultChunkSize
	}

	chunkOverlap := 0
	if parsed.HasChunkOverlap {
		chunkOverlap = int(parsed.ChunkOverlap)
		if parsed.ChunkOverlap != float64(chunkOverlap) || chunkOverlap < 0 || chunkOverlap >= chunkSize {
			JSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"error":   "chunk_overlap must satisfy 0 <= chunk_overlap < chunk_size",
			})
			return
		}
	} else if project.ChunkOverlap > 0 {
		chunkOverlap = project.ChunkOverlap
	} else {
		chunkOverlap = h.Cfg.DefaultChunkOverlap
	}

	project.ChunkSize = chunkSize
	project.ChunkOverlap = chunkOverlap

	text := h.PM.GetExtractedText(projectID)
	if text == nil {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.textNotFound", nil),
		})
		return
	}

	if !hasOntology(project.Ontology) {
		JSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": t("api.ontologyNotFound", nil),
		})
		return
	}

	// Only mutate Cloud state after the complete rebuild request validates.
	if project.Status == models.ProjectFailed || (force && project.Status == models.ProjectGraphCompleted) {
		graphIDToDelete := deref(project.GraphID)
		if graphIDToDelete != "" {
			gl := graphLock(graphIDToDelete)
			gl.Lock()
			defer gl.Unlock()
			if err := h.deleteGraphIfPresent(graphIDToDelete); err != nil {
				var inUse *GraphInUseError
				if errors.As(err, &inUse) {
					JSON(w, http.StatusConflict, map[string]any{"success": false, "error": err.Error()})
					return
				}
				JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
				return
			}
		}
		project.Status = models.ProjectOntologyGenerated
		clearProjectGraphReference(project)
		if err := h.PM.SaveProject(project); err != nil {
			JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
	}

	taskManager := models.GetTaskManager()
	taskID := taskManager.CreateTask(fmt.Sprintf("Construcción de grafo: %s", graphName), map[string]any{
		"project_id": projectID,
	})
	h.Logger.Info(fmt.Sprintf("Creando tarea de construcción de grafo: task_id=%s, project_id=%s", taskID, projectID))

	project.Status = models.ProjectGraphBuilding
	project.GraphBuildTask = &taskID
	if err := h.PM.SaveProject(project); err != nil {
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	go h.buildTask(taskID, projectID, loc, graphName, chunkSize, chunkOverlap, resumeExistingBatch, llmFactory(r))

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"project_id": projectID,
			"task_id":    taskID,
			"resumed":    resumeExistingBatch,
			"message":    t("api.graphBuildStarted", map[string]string{"taskId": taskID}),
		},
	})
}

// buildTask replica el hilo build_task del blueprint /build.
func (h *GraphHandler) buildTask(taskID, projectID, loc, graphName string, chunkSize, chunkOverlap int, resumeExistingBatch bool, llmF func() (*llm.Client, error)) {
	builder := graphbuilder.New(h.Client)
	builder.Locale = loc
	if llmF != nil {
		builder.LLMFactory = llmF
	}
	taskManager := models.GetTaskManager()
	logger := h.Logger.With("task_id", taskID)

	fail := func(err error) {
		logger.Error("Fallo en la construcción del grafo", "error", err.Error())
		msg := err.Error()
		lock := buildLock(projectID)
		lock.Lock()
		defer lock.Unlock()
		project := h.PM.GetProject(projectID)
		if project != nil {
			project.Status = models.ProjectFailed
			project.Error = &msg
			if saveErr := h.PM.SaveProject(project); saveErr != nil {
				logger.Error("no se pudo persistir el fallo", "error", saveErr)
			}
		}
		status := models.TaskFailed
		message := locale.T(loc, "progress.buildFailed", map[string]string{"error": msg})
		taskManager.UpdateTask(taskID, models.TaskUpdate{
			Status:  &status,
			Message: &message,
			Error:   &msg,
		})
	}

	update := func(progress int, key string, args map[string]string) {
		msg := locale.T(loc, key, args)
		taskManager.UpdateTask(taskID, models.TaskUpdate{
			Progress: &progress,
			Message:  &msg,
		})
	}

	update(0, "progress.initGraphService", nil)
	status := models.TaskProcessing
	initMsg := locale.T(loc, "progress.initGraphService", nil)
	taskManager.UpdateTask(taskID, models.TaskUpdate{Status: &status, Message: &initMsg})

	project := h.PM.GetProject(projectID)
	if project == nil {
		fail(fmt.Errorf("project %s not found", projectID))
		return
	}

	textPtr := h.PM.GetExtractedText(projectID)
	if textPtr == nil {
		fail(fmt.Errorf("extracted text not found for project %s", projectID))
		return
	}
	text := *textPtr

	var ontologyDef map[string]any
	if err := json.Unmarshal(project.Ontology, &ontologyDef); err != nil {
		fail(err)
		return
	}

	update(5, "progress.textChunking", nil)
	chunks := tex.SplitTextIntoChunks(text, chunkSize, chunkOverlap)
	if err := graphbuilder.ValidateBatchChunks(chunks, 350); err != nil {
		fail(err)
		return
	}
	totalChunks := len(chunks)

	var submission graphbuilder.BatchSubmission
	var graphID string

	if resumeExistingBatch {
		graphID = deref(project.GraphID)
		operationID := deref(project.ZepBatchOpID)
		if computed := graphbuilder.BuildOperationID(graphID, chunks); computed != operationID {
			fail(fmt.Errorf("Persisted Zep batch does not match the current graph input"))
			return
		}
		submission = graphbuilder.BatchSubmission{
			BatchID:     deref(project.ZepBatchID),
			OperationID: operationID,
			ItemCount:   totalChunks,
		}
		update(55, "progress.waitingZepProcess", nil)
	} else {
		update(10, "progress.creatingZepGraph", nil)

		rememberGraph := func(id string) {
			p := h.PM.GetProject(projectID)
			if p == nil {
				return
			}
			p.GraphID = &id
			_ = h.PM.SaveProject(p)
		}
		var err error
		graphID, err = builder.CreateGraph(graphName, "", rememberGraph)
		if err != nil {
			fail(err)
			return
		}

		update(15, "progress.settingOntology", nil)
		if err := builder.SetOntology(graphID, ontologyDef); err != nil {
			fail(err)
			return
		}

		update(15, "progress.addingChunks", map[string]string{"count": fmt.Sprintf("%d", totalChunks)})

		rememberBatch := func(batchID *string, operationID string) {
			p := h.PM.GetProject(projectID)
			if p == nil {
				return
			}
			if batchID != nil {
				id := *batchID
				p.ZepBatchID = &id
			}
			op := operationID
			p.ZepBatchOpID = &op
			_ = h.PM.SaveProject(p)
		}

		submission, err = builder.AddTextBatches(
			graphID, chunks, 350,
			func(msg string, ratio float64) {
				progress := 15 + int(ratio*40)
				m := msg
				taskManager.UpdateTask(taskID, models.TaskUpdate{Progress: &progress, Message: &m})
			},
			rememberBatch,
		)
		if err != nil {
			fail(err)
			return
		}
	}

	update(55, "progress.waitingZepProcess", nil)
	if _, err := builder.WaitForBatch(submission, func(msg string, ratio float64) {
		progress := 55 + int(ratio*35)
		m := msg
		taskManager.UpdateTask(taskID, models.TaskUpdate{Progress: &progress, Message: &m})
	}, 0); err != nil {
		fail(err)
		return
	}

	update(95, "progress.fetchingGraphData", nil)
	graphData, err := builder.GetGraphData(graphID)
	if err != nil {
		fail(err)
		return
	}

	nodeCount, _ := graphData["node_count"].(int)
	edgeCount, _ := graphData["edge_count"].(int)
	logger.Info(fmt.Sprintf("Construcción del grafo completada: graph_id=%s, nodos=%d, aristas=%d", graphID, nodeCount, edgeCount))

	lock := buildLock(projectID)
	lock.Lock()
	defer lock.Unlock()

	project = h.PM.GetProject(projectID)
	if project != nil {
		project.Status = models.ProjectGraphCompleted
		project.Error = nil
		if saveErr := h.PM.SaveProject(project); saveErr != nil {
			logger.Error("no se pudo persistir el estado completado", "error", saveErr)
		}
	}

	completed := models.TaskCompleted
	completeMsg := locale.T(loc, "progress.graphBuildComplete", nil)
	result := map[string]any{
		"project_id":   projectID,
		"graph_id":     graphID,
		"node_count":   nodeCount,
		"edge_count":   edgeCount,
		"chunk_count":  totalChunks,
		"zep_batch_id": submission.BatchID,
	}
	taskManager.UpdateTask(taskID, models.TaskUpdate{
		Status:   &completed,
		Message:  &completeMsg,
		Progress: intPtr(100),
		Result:   result,
	})
}

func intPtr(i int) *int { return &i }

// ===== Consulta de tareas =====

func (h *GraphHandler) getTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("task_id")
	loc := locale.Normalize(r.Header.Get("Accept-Language"))

	task, ok := models.GetTaskManager().GetTask(taskID)
	if !ok {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.taskNotFound", map[string]string{"id": taskID}),
		})
		return
	}
	dict := task.ToDict()
	if !h.ownsTask(r, &dict) {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.taskNotFound", map[string]string{"id": taskID}),
		})
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    dict,
	})
}

func (h *GraphHandler) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks := models.GetTaskManager().ListTasks("")
	uid := scopeID(r)
	if uid != "" {
		filtered := []models.TaskDict{}
		for i := range tasks {
			if h.ownsTask(r, &tasks[i]) {
				filtered = append(filtered, tasks[i])
			}
		}
		tasks = filtered
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    tasks,
		"count":   len(tasks),
	})
}

// ===== Datos del grafo =====

func (h *GraphHandler) getGraphData(w http.ResponseWriter, r *http.Request) {
	graphID := r.PathValue("graph_id")
	loc := locale.Normalize(r.Header.Get("Accept-Language"))

	if !h.Cfg.UseLocalGraph() && h.Cfg.ZEPAPIKey == "" {
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   locale.T(loc, "api.zepApiKeyMissing", nil),
		})
		return
	}

	builder := graphbuilder.New(h.Client)
	if !h.denyUnlessGraphOwner(r, w, graphID) {
		return
	}
	data, err := builder.GetGraphData(graphID)
	if err != nil {
		h.Logger.Error("no se pudieron leer los datos del grafo", "graph_id", graphID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false, "error": err.Error(),
		})
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    data,
	})
}

func (h *GraphHandler) deleteGraph(w http.ResponseWriter, r *http.Request) {
	graphID := r.PathValue("graph_id")
	loc := locale.Normalize(r.Header.Get("Accept-Language"))
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	if !h.Cfg.UseLocalGraph() && h.Cfg.ZEPAPIKey == "" {
		JSON(w, http.StatusInternalServerError, map[string]any{
			"success": false, "error": t("api.zepApiKeyMissing", nil),
		})
		return
	}

	projects := h.PM.FindProjectsByGraphId(graphID)
	if len(projects) == 0 || !h.denyUnlessGraphOwner(r, w, graphID) {
		JSON(w, http.StatusNotFound, map[string]any{
			"success": false, "error": "No local project references this graph",
		})
		return
	}

	projectIDs := []string{}
	for _, p := range projects {
		projectIDs = append(projectIDs, p.ProjectID)
	}
	sort.Strings(projectIDs)

	locks := []*sync.Mutex{}
	for _, id := range projectIDs {
		l := buildLock(id)
		l.Lock()
		locks = append(locks, l)
	}
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
	}()

	gl := graphLock(graphID)
	gl.Lock()
	defer gl.Unlock()

	// Re-read under all owning project locks.
	projects = h.PM.FindProjectsByGraphId(graphID)
	for _, p := range projects {
		if h.projectHasActiveBuild(p) {
			JSON(w, http.StatusConflict, map[string]any{
				"success": false, "error": t("api.graphBuilding", nil),
			})
			return
		}
	}

	if err := h.deleteGraphIfPresent(graphID); err != nil {
		var inUse *GraphInUseError
		if errors.As(err, &inUse) {
			JSON(w, http.StatusConflict, map[string]any{"success": false, "error": err.Error()})
			return
		}
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	for _, p := range projects {
		clearProjectGraphReference(p)
		if hasOntology(p.Ontology) {
			p.Status = models.ProjectOntologyGenerated
		} else {
			p.Status = models.ProjectCreated
		}
		if err := h.PM.SaveProject(p); err != nil {
			JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		delBuildLock(p.ProjectID)
	}
	delGraphLock(graphID)

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": t("api.graphDeleted", map[string]string{"id": graphID}),
	})
}
