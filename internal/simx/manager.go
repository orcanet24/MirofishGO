// Port de backend/app/services/simulation_manager.py.
package simx

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mirofish/internal/config"
	"mirofish/internal/fsx"
	"mirofish/internal/graph"
	"mirofish/internal/llm"
	"mirofish/internal/locale"
	"mirofish/internal/timex"
)

// SimulationStatus replica el enum de estados.
type SimulationStatus string

const (
	SimCreated   SimulationStatus = "created"
	SimPreparing SimulationStatus = "preparing"
	SimReady     SimulationStatus = "ready"
	SimRunning   SimulationStatus = "running"
	SimStopping  SimulationStatus = "stopping"
	SimPaused    SimulationStatus = "paused"
	SimStopped   SimulationStatus = "stopped"
	SimCompleted SimulationStatus = "completed"
	SimFailed    SimulationStatus = "failed"
)

// PlatformType replica el enum de plataformas.
type PlatformType string

const (
	PlatformTwitter PlatformType = "twitter"
	PlatformReddit  PlatformType = "reddit"
)

// SimulationState replica el dataclass SimulationState.
type SimulationState struct {
	SimulationID string
	ProjectID    string
	GraphID      string

	EnableTwitter bool
	EnableReddit  bool

	Status SimulationStatus

	EntitiesCount int
	ProfilesCount int
	EntityTypes   []string

	ProfilesGenerated bool
	ConfigGenerated   bool
	ConfigReasoning   string

	CurrentRound  int
	TwitterStatus string
	RedditStatus  string

	CreatedAt string
	UpdatedAt string

	Error *string

	UserID *string

	// Population — composición demográfica configurada para esta simulación.
	Population *PopulationSpec `json:",omitempty"`

	// InteractionDepth — profundidad de las entrevistas (1-5) elegida por el
	// usuario; la usa el frontend como valor por defecto.
	InteractionDepth int `json:"interaction_depth,omitempty"`
}

// ToDict replica to_dict completo.
func (s *SimulationState) ToDict() map[string]any {
	return map[string]any{
		"simulation_id":      s.SimulationID,
		"project_id":         s.ProjectID,
		"graph_id":           s.GraphID,
		"enable_twitter":     s.EnableTwitter,
		"enable_reddit":      s.EnableReddit,
		"status":             string(s.Status),
		"entities_count":     s.EntitiesCount,
		"profiles_count":     s.ProfilesCount,
		"entity_types":       s.EntityTypes,
		"profiles_generated": s.ProfilesGenerated,
		"config_generated":   s.ConfigGenerated,
		"config_reasoning":   s.ConfigReasoning,
		"current_round":      s.CurrentRound,
		"twitter_status":     s.TwitterStatus,
		"reddit_status":      s.RedditStatus,
		"created_at":         s.CreatedAt,
		"updated_at":         s.UpdatedAt,
		"error":              s.Error,
		"user_id":            s.UserID,
		"population":         s.Population,
		"interaction_depth":  s.InteractionDepth,
	}
}

// GetDefaultPlatform replica get_default_platform.
func (s *SimulationState) GetDefaultPlatform() string {
	if s.EnableTwitter && s.EnableReddit {
		return "reddit"
	}
	if s.EnableTwitter {
		return "twitter"
	}
	return "reddit"
}

// ToSimpleDict replica to_simple_dict.
func (s *SimulationState) ToSimpleDict() map[string]any {
	return map[string]any{
		"simulation_id":      s.SimulationID,
		"project_id":         s.ProjectID,
		"graph_id":           s.GraphID,
		"status":             string(s.Status),
		"entities_count":     s.EntitiesCount,
		"profiles_count":     s.ProfilesCount,
		"entity_types":       s.EntityTypes,
		"profiles_generated": s.ProfilesGenerated,
		"config_generated":   s.ConfigGenerated,
		"error":              s.Error,
	}
}

// Manager replica SimulationManager.
type Manager struct {
	DataDir string

	mu          sync.Mutex
	simulations map[string]*SimulationState

	// Fábricas inyectables (tests).
	ProfileGeneratorFactory func(client *graph.Client, graphID string) *ProfileGenerator
	ConfigGeneratorFactory  func() *ConfigGenerator
}

// NewManager crea el gestor sobre el directorio de simulaciones.
func NewManager(dataDir string) *Manager {
	_ = os.MkdirAll(dataDir, 0o755)
	return &Manager{DataDir: dataDir, simulations: map[string]*SimulationState{}}
}

// newProfileGenerator — con factory LLM opcional: config delegada del usuario.
// localeCode fija el idioma de los prompts del generador (normalizado a es/en).
func (m *Manager) newProfileGenerator(client *graph.Client, graphID, localeCode string, statePopulation *PopulationSpec, studyTypeID string, llmF ...func() (*llm.Client, error)) *ProfileGenerator {
	var g *ProfileGenerator
	if m.ProfileGeneratorFactory != nil {
		g = m.ProfileGeneratorFactory(client, graphID)
	} else {
		g = NewProfileGenerator(client, graphID)
	}
	g.Locale = locale.Normalize(localeCode)
	g.Population = statePopulation
	g.StudyType = studyTypeID
	if len(llmF) > 0 && llmF[0] != nil {
		g.LLMFactory = llmF[0]
	}
	return g
}

// newConfigGenerator — localeCode fija el idioma de los prompts del generador.
func (m *Manager) newConfigGenerator(localeCode string, llmF ...func() (*llm.Client, error)) *ConfigGenerator {
	var g *ConfigGenerator
	if m.ConfigGeneratorFactory != nil {
		g = m.ConfigGeneratorFactory()
	} else {
		g = NewConfigGenerator()
	}
	g.Locale = locale.Normalize(localeCode)
	if len(llmF) > 0 && llmF[0] != nil {
		g.LLMFactory = llmF[0]
	}
	return g
}

// SimulationDir devuelve la ruta del directorio de una simulación SIN crearlo.
// Es importante: si leer un id inexistente creara el directorio, cada
// /api/simulation/<id> desconocido ensuciaría uploads/simulations y
// ListSimulations acabaría recorriendo carpetas vacías.
func (m *Manager) SimulationDir(simulationID string) string {
	return filepath.Join(m.DataDir, simulationID)
}

// simulationDir asegura el directorio y devuelve su ruta (solo para escritura).
func (m *Manager) simulationDir(simulationID string) string {
	dir := m.SimulationDir(simulationID)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// SaveSimulationState replica _save_simulation_state.
//
// Serializa y escribe sobre una copia fuera del lock (el I/O de disco no debe
// bloquear /list ni /history) y publica la copia en la caché. La caché nunca
// comparte punteros con quien está mutando el estado, así que un GET concurrente
// no puede leer un struct a medio actualizar (antes Load devolvía el mismo
// puntero que el hilo de preparación estaba modificando: data race real).
func (m *Manager) SaveSimulationState(state *SimulationState) {
	state.UpdatedAt = timex.Now()
	cached := *state
	cached.EntityTypes = append([]string(nil), state.EntityTypes...)

	b, err := json.MarshalIndent(state.ToDict(), "", "  ")
	if err != nil {
		logger.Error("no se pudo serializar el estado de la simulación: " + err.Error())
		return
	}
	dir := m.simulationDir(state.SimulationID)
	if err := fsx.WriteFileAtomic(filepath.Join(dir, "state.json"), b, 0o644); err != nil {
		logger.Error("no se pudo escribir state.json: " + err.Error())
		return
	}

	m.mu.Lock()
	m.simulations[state.SimulationID] = &cached
	m.mu.Unlock()
}

// LoadSimulationState replica _load_simulation_state devolviendo siempre una
// copia (el llamador puede mutarla y guardarla sin tocar la caché).
func (m *Manager) LoadSimulationState(simulationID string) *SimulationState {
	m.mu.Lock()
	if state, ok := m.simulations[simulationID]; ok {
		cp := *state
		cp.EntityTypes = append([]string(nil), state.EntityTypes...)
		m.mu.Unlock()
		return &cp
	}
	m.mu.Unlock()

	raw, err := os.ReadFile(filepath.Join(m.SimulationDir(simulationID), "state.json"))
	if err != nil {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil
	}

	state := &SimulationState{
		SimulationID:      simulationID,
		ProjectID:         strOr(data["project_id"]),
		GraphID:           strOr(data["graph_id"]),
		EnableTwitter:     boolOr(data["enable_twitter"], true),
		EnableReddit:      boolOr(data["enable_reddit"], true),
		Status:            SimulationStatus(defaultStr(strOr(data["status"]), "created")),
		EntitiesCount:     intFromAny(data["entities_count"], 0),
		ProfilesCount:     intFromAny(data["profiles_count"], 0),
		EntityTypes:       stringList(data["entity_types"]),
		ProfilesGenerated: boolOr(data["profiles_generated"], false),
		ConfigGenerated:   boolOr(data["config_generated"], false),
		ConfigReasoning:   strOr(data["config_reasoning"]),
		CurrentRound:      intFromAny(data["current_round"], 0),
		TwitterStatus:     defaultStr(strOr(data["twitter_status"]), "not_started"),
		RedditStatus:      defaultStr(strOr(data["reddit_status"]), "not_started"),
		CreatedAt:         strOr(data["created_at"]),
		UpdatedAt:         strOr(data["updated_at"]),
	}
	if s, ok := data["error"].(string); ok {
		state.Error = &s
	}
	if s, ok := data["user_id"].(string); ok {
		state.UserID = &s
	}
	if raw, ok := data["population"].(map[string]any); ok {
		state.Population = parsePopulation(raw)
	}
	if d := intFromAny(data["interaction_depth"], 0); d > 0 && d <= 100 {
		state.InteractionDepth = d
	}
	if state.CreatedAt == "" {
		state.CreatedAt = timex.Now()
	}
	if state.UpdatedAt == "" {
		state.UpdatedAt = state.CreatedAt
	}

	cached := *state
	cached.EntityTypes = append([]string(nil), state.EntityTypes...)
	m.mu.Lock()
	m.simulations[simulationID] = &cached
	m.mu.Unlock()
	return state
}

// CreateSimulation replica create_simulation.
func (m *Manager) CreateSimulation(projectID, graphID string, enableTwitter, enableReddit bool, userID *string) *SimulationState {
	now := timex.Now()
	state := &SimulationState{
		SimulationID:  newSimulationID(),
		ProjectID:     projectID,
		GraphID:       graphID,
		EnableTwitter: enableTwitter,
		EnableReddit:  enableReddit,
		Status:        SimCreated,
		TwitterStatus: "not_started",
		RedditStatus:  "not_started",
		CreatedAt:     now,
		UpdatedAt:     now,
		UserID:        userID,
	}
	m.SaveSimulationState(state)
	logger.Info(fmt.Sprintf("Simulación creada: %s, project=%s, graph=%s", state.SimulationID, projectID, graphID))
	return state
}

// PrepareSimulation replica prepare_simulation (fase completa de preparación).
func (m *Manager) PrepareSimulation(
	client *graph.Client,
	simulationID string,
	simulationRequirement, documentText string,
	definedEntityTypes []string,
	useLLMForProfiles bool,
	progressCallback func(stage string, progress int, message string, extra map[string]any),
	parallelProfileCount int,
	localeCode string,
	studyType string,
	llmF ...func() (*llm.Client, error),
) (*SimulationState, error) {
	// El locale de la petición se pasa explícitamente: el manager es un
	// singleton compartido y no puede guardarlo en un campo sin carreras
	// entre peticiones concurrentes en idiomas distintos.
	loc := localeCode
	state := m.LoadSimulationState(simulationID)
	if state == nil {
		return nil, fmt.Errorf("Simulación no existe: %s", simulationID)
	}

	state.Status = SimPreparing
	state.Error = nil
	state.ProfilesGenerated = false
	state.ConfigGenerated = false
	state.ConfigReasoning = ""
	m.SaveSimulationState(state)

	simDir := m.simulationDir(simulationID)

	// Etapa 1: leer y filtrar entidades (con reintentos con backoff).
	reader := NewEntityReader(client)
	var filtered *FilteredEntities
	retryCount := 0
	maxRetries := 3
	for {
		var err error
		filtered, err = reader.FilterDefinedEntities(state.GraphID, definedEntityTypes, true)
		if err == nil {
			break
		}
		retryCount++
		if retryCount >= maxRetries {
			state.Status = SimFailed
			msg := err.Error()
			state.Error = &msg
			m.SaveSimulationState(state)
			return nil, err
		}
		waitTime := 1 << retryCount
		if waitTime > 30 {
			waitTime = 30
		}
		logger.Warn(fmt.Sprintf("Intento %d/%d fallido leyendo grafo Zep, reintentando en %ds: %v", retryCount, maxRetries, waitTime, err))
		time.Sleep(time.Duration(waitTime) * time.Second)
	}

	state.EntitiesCount = filtered.FilteredCount
	state.EntityTypes = filtered.EntityTypes

	if progressCallback != nil {
		progressCallback("reading", 100, locale.T(loc, "progress.readingComplete", map[string]string{"count": fmt.Sprintf("%d", filtered.FilteredCount)}),
			map[string]any{"current": filtered.FilteredCount, "total": filtered.FilteredCount})
	}

	if filtered.FilteredCount == 0 {
		state.Status = SimFailed
		msg := "No se encontraron entidades que cumplan los criterios (0 entidades usables): el grafo no contiene entidades para esta simulación. Reconstruye el grafo o revisa la ontología del proyecto antes de preparar."
		state.Error = &msg
		m.SaveSimulationState(state)
		return nil, fmt.Errorf("%s", msg)
	}

	// Etapa 2: perfiles.
	totalEntities := len(filtered.Entities)
	if progressCallback != nil {
		progressCallback("generating_profiles", 0, locale.T(loc, "progress.startGenerating", nil),
			map[string]any{"current": 0, "total": totalEntities})
	}

	generator := m.newProfileGenerator(client, state.GraphID, loc, state.Population, studyType, llmF...)
	realtimeOutputPath := ""
	realtimePlatform := "reddit"
	if state.EnableReddit {
		realtimeOutputPath = filepath.Join(simDir, "reddit_profiles.json")
		realtimePlatform = "reddit"
	} else if state.EnableTwitter {
		realtimeOutputPath = filepath.Join(simDir, "twitter_profiles.csv")
		realtimePlatform = "twitter"
	}

	profiles := generator.GenerateProfilesFromEntities(
		filtered.Entities,
		useLLMForProfiles,
		func(current, total int, msg string) {
			if progressCallback != nil {
				progressCallback("generating_profiles", int(float64(current)/float64(total)*100), msg,
					map[string]any{"current": current, "total": total, "item_name": msg})
			}
		},
		state.GraphID,
		parallelProfileCount,
		realtimeOutputPath,
		realtimePlatform,
	)

	state.ProfilesCount = len(profiles)
	state.ProfilesGenerated = len(profiles) > 0
	m.SaveSimulationState(state)

	if progressCallback != nil {
		progressCallback("generating_profiles", 95, locale.T(loc, "progress.savingProfiles", nil),
			map[string]any{"current": totalEntities, "total": totalEntities})
	}

	if state.EnableReddit {
		_ = generator.SaveProfiles(profiles, filepath.Join(simDir, "reddit_profiles.json"), "reddit")
	}
	if state.EnableTwitter {
		_ = generator.SaveProfiles(profiles, filepath.Join(simDir, "twitter_profiles.csv"), "twitter")
	}

	if progressCallback != nil {
		progressCallback("generating_profiles", 100, locale.T(loc, "progress.profilesComplete", map[string]string{"count": fmt.Sprintf("%d", len(profiles))}),
			map[string]any{"current": len(profiles), "total": len(profiles)})
	}

	// Etapa 3: configuración con LLM.
	if progressCallback != nil {
		progressCallback("generating_config", 0, locale.T(loc, "progress.analyzingRequirements", nil), map[string]any{"current": 0, "total": 3})
	}
	if progressCallback != nil {
		progressCallback("generating_config", 30, locale.T(loc, "progress.callingLLMConfig", nil), map[string]any{"current": 1, "total": 3})
	}

	configGenerator := m.newConfigGenerator(loc, llmF...)
	simParams, err := configGenerator.GenerateConfig(
		simulationID, state.ProjectID, state.GraphID,
		simulationRequirement, documentText, filtered.Entities,
		state.EnableTwitter, state.EnableReddit, nil,
	)
	if err != nil {
		state.Status = SimFailed
		msg := err.Error()
		state.Error = &msg
		m.SaveSimulationState(state)
		return nil, err
	}

	if progressCallback != nil {
		progressCallback("generating_config", 70, locale.T(loc, "progress.savingConfigFiles", nil), map[string]any{"current": 2, "total": 3})
	}

	configPath := filepath.Join(simDir, "simulation_config.json")
	_ = os.WriteFile(configPath, []byte(simParams.ToJSON()), 0o644)

	state.ConfigGenerated = true
	state.ConfigReasoning = simParams.GenerationReasoning

	if progressCallback != nil {
		progressCallback("generating_config", 100, locale.T(loc, "progress.configComplete", nil), map[string]any{"current": 3, "total": 3})
	}

	state.Status = SimReady
	m.SaveSimulationState(state)
	logger.Info(fmt.Sprintf("Simulación preparada: %s, entities=%d, profiles=%d", simulationID, state.EntitiesCount, state.ProfilesCount))
	return state, nil
}

// GetSimulation replica get_simulation.
func (m *Manager) GetSimulation(simulationID string) *SimulationState {
	return m.LoadSimulationState(simulationID)
}

// ListSimulations replica list_simulations.
func (m *Manager) ListSimulations(projectID string) []*SimulationState {
	out := []*SimulationState{}
	entries, err := os.ReadDir(m.DataDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !e.IsDir() {
			continue
		}
		state := m.LoadSimulationState(name)
		if state == nil {
			continue
		}
		if projectID == "" || state.ProjectID == projectID {
			out = append(out, state)
		}
	}
	return out
}

// GetProfiles replica get_profiles (CSV twitter / JSON reddit).
func (m *Manager) GetProfiles(simulationID string, platform string) ([]map[string]any, error) {
	state := m.LoadSimulationState(simulationID)
	if state == nil {
		return nil, fmt.Errorf("Simulación no existe: %s", simulationID)
	}
	if platform == "" {
		platform = state.GetDefaultPlatform()
	}
	if platform != "twitter" && platform != "reddit" {
		return nil, fmt.Errorf("Plataforma no soportada: %s", platform)
	}

	simDir := m.SimulationDir(simulationID)
	profilePath := filepath.Join(simDir, "twitter_profiles.csv")
	if platform == "reddit" {
		profilePath = filepath.Join(simDir, "reddit_profiles.json")
	}
	if _, err := os.Stat(profilePath); err != nil {
		return []map[string]any{}, nil
	}

	if platform == "twitter" {
		raw, err := os.ReadFile(profilePath)
		if err != nil {
			return nil, err
		}
		return readTwitterCSV(raw)
	}

	raw, err := os.ReadFile(profilePath)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSimulationConfig replica get_simulation_config.
func (m *Manager) GetSimulationConfig(simulationID string) map[string]any {
	simDir := m.SimulationDir(simulationID)
	raw, err := os.ReadFile(filepath.Join(simDir, "simulation_config.json"))
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// GetRunInstructions replica la forma de get_run_instructions. El motor OASIS
// corre en proceso (oasis.Engine): no hay scripts externos que ejecutar, así
// que los comandos van vacíos y la nota lo explica.
func (m *Manager) GetRunInstructions(simulationID string) map[string]any {
	simDir := m.SimulationDir(simulationID)
	configPath := filepath.Join(simDir, "simulation_config.json")
	cfg := config.Load()
	scriptsDir := filepath.Join(cfg.BaseDir, "backend", "scripts")
	return map[string]any{
		"simulation_dir": simDir,
		"scripts_dir":    scriptsDir,
		"config_file":    configPath,
		"commands":       map[string]string{},
		"instructions": "La simulación se ejecuta en proceso con el motor OASIS: " +
			"usa los endpoints /api/simulation/start y /api/simulation/stop. " +
			"El archivo de configuración está en config_file.",
	}
}

// Exists indica si el directorio de la simulación existe.
func (m *Manager) Exists(simulationID string) bool {
	if simulationID == "" || strings.ContainsAny(simulationID, `/\`) || simulationID == "." || simulationID == ".." {
		return false
	}
	st, err := os.Stat(m.SimulationDir(simulationID))
	return err == nil && st.IsDir()
}

// DeleteSimulation elimina el directorio de la simulación (borrado total).
func (m *Manager) DeleteSimulation(simulationID string) bool {
	if simulationID == "" || strings.ContainsAny(simulationID, `/\`) || simulationID == "." || simulationID == ".." {
		return false
	}
	dir := m.SimulationDir(simulationID)
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return os.RemoveAll(dir) == nil
	}
	return false
}

// preparedStatuses replica la lista de estados que cuentan como preparados.
var preparedStatuses = map[SimulationStatus]bool{
	SimReady:     true,
	SimPreparing: true,
	SimRunning:   true,
	SimCompleted: true,
	SimStopped:   true,
	SimFailed:    true,
}

// CheckPrepared replica _check_simulation_prepared: comprueba archivos
// requeridos + state.json y auto-cura el estado preparing→ready cuando los
// artefactos ya están completos.
//
// Diferencia deliberada con Python: solo exige los perfiles de las plataformas
// habilitadas. El original exigía siempre reddit_profiles.json Y
// twitter_profiles.csv, así que una simulación con enable_twitter=false nunca
// podía considerarse preparada (reintentaba preparar y gastaba LLM cada vez).
func (m *Manager) CheckPrepared(simulationID string) (bool, map[string]any) {
	if !m.Exists(simulationID) {
		return false, map[string]any{"reason": "La carpeta de simulación no existe"}
	}
	state := m.LoadSimulationState(simulationID)
	if state == nil {
		return false, map[string]any{"reason": "La carpeta de simulación no existe"}
	}

	dir := m.SimulationDir(simulationID)
	required := []string{"state.json", "simulation_config.json"}
	if state.EnableReddit {
		required = append(required, "reddit_profiles.json")
	}
	if state.EnableTwitter {
		required = append(required, "twitter_profiles.csv")
	}

	existing := []string{}
	missing := []string{}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			existing = append(existing, name)
		} else {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return false, map[string]any{
			"reason":         "Faltan archivos necesarios",
			"missing_files":  missing,
			"existing_files": existing,
		}
	}

	if !preparedStatuses[state.Status] || !state.ConfigGenerated {
		return false, map[string]any{
			"reason": fmt.Sprintf("El estado no está en la lista de preparados o config_generated es false: status=%s, config_generated=%v",
				state.Status, state.ConfigGenerated),
			"status":           string(state.Status),
			"config_generated": state.ConfigGenerated,
		}
	}

	if state.Status == SimPreparing {
		state.Status = SimReady
		m.SaveSimulationState(state)
		logger.Info(fmt.Sprintf("Estado de simulación actualizado automáticamente: %s preparing -> ready", simulationID))
	}

	profilesCount := state.ProfilesCount
	if data, err := os.ReadFile(filepath.Join(dir, "reddit_profiles.json")); err == nil {
		var list []map[string]any
		if json.Unmarshal(data, &list) == nil {
			profilesCount = len(list)
		}
	}

	return true, map[string]any{
		"status":           string(state.Status),
		"entities_count":   state.EntitiesCount,
		"profiles_count":   profilesCount,
		"entity_types":     state.EntityTypes,
		"config_generated": state.ConfigGenerated,
		"created_at":       state.CreatedAt,
		"updated_at":       state.UpdatedAt,
		"existing_files":   existing,
	}
}

// GetProfilesRealtime replica GET /profiles/realtime: lee el archivo directo
// del disco (sirve mientras se está generando) y añade metadatos de estado.
// No crea directorios ni falla si el JSON está a medio escribir.
func (m *Manager) GetProfilesRealtime(simulationID, platform string) (map[string]any, bool) {
	dir := m.SimulationDir(simulationID)
	if !m.Exists(simulationID) {
		return nil, false
	}
	if platform == "" {
		platform = "reddit"
		if state := m.LoadSimulationState(simulationID); state != nil {
			platform = state.GetDefaultPlatform()
		}
	}
	if platform != "twitter" && platform != "reddit" {
		return nil, false
	}

	profilesFile := filepath.Join(dir, "twitter_profiles.csv")
	if platform == "reddit" {
		profilesFile = filepath.Join(dir, "reddit_profiles.json")
	}

	profiles := []map[string]any{}
	var modifiedAt any
	if st, err := os.Stat(profilesFile); err == nil {
		modifiedAt = timex.ISO(st.ModTime())
		if raw, err := os.ReadFile(profilesFile); err == nil {
			if platform == "reddit" {
				// Un JSON incompleto significa "todavía escribiendo": se
				// devuelve lista vacía en vez de un 500 durante el polling.
				_ = json.Unmarshal(raw, &profiles)
			} else {
				csvProfiles, err := readTwitterCSV(raw)
				if err == nil {
					profiles = csvProfiles
				}
			}
		}
	}
	if profiles == nil {
		profiles = []map[string]any{}
	}

	result := map[string]any{
		"simulation_id":    simulationID,
		"platform":         platform,
		"count":            len(profiles),
		"total_expected":   nil,
		"is_generating":    false,
		"status":           nil,
		"error":            nil,
		"file_exists":      false,
		"file_modified_at": modifiedAt,
		"profiles":         profiles,
	}
	if _, err := os.Stat(profilesFile); err == nil {
		result["file_exists"] = true
	}
	if state := m.LoadSimulationState(simulationID); state != nil {
		result["status"] = string(state.Status)
		result["is_generating"] = state.Status == SimPreparing
		result["total_expected"] = state.EntitiesCount
		if state.Error != nil {
			result["error"] = *state.Error
		}
	}
	return result, true
}

// GetConfigRealtime replica GET /config/realtime.
func (m *Manager) GetConfigRealtime(simulationID string) (map[string]any, bool) {
	dir := m.SimulationDir(simulationID)
	if !m.Exists(simulationID) {
		return nil, false
	}

	configFile := filepath.Join(dir, "simulation_config.json")
	var config any
	var modifiedAt any
	fileExists := false
	if st, err := os.Stat(configFile); err == nil {
		fileExists = true
		modifiedAt = timex.ISO(st.ModTime())
		if raw, err := os.ReadFile(configFile); err == nil {
			var parsed map[string]any
			if json.Unmarshal(raw, &parsed) == nil {
				config = parsed
			}
		}
	}

	status := ""
	var genStage any
	var errMsg any
	isGenerating := false
	profilesGenerated := false
	configGenerated := false
	if state := m.LoadSimulationState(simulationID); state != nil {
		status = string(state.Status)
		isGenerating = state.Status == SimPreparing
		profilesGenerated = state.ProfilesGenerated
		configGenerated = state.ConfigGenerated
		if state.Error != nil {
			errMsg = *state.Error
		}
		switch {
		case isGenerating && profilesGenerated:
			genStage = "generating_config"
		case isGenerating:
			genStage = "generating_profiles"
		case state.Status == SimReady:
			genStage = "completed"
		case state.Status == SimFailed:
			genStage = "failed"
		}
	}

	result := map[string]any{
		"simulation_id":      simulationID,
		"file_exists":        fileExists,
		"file_modified_at":   modifiedAt,
		"is_generating":      isGenerating,
		"status":             status,
		"error":              errMsg,
		"generation_stage":   genStage,
		"profiles_generated": profilesGenerated,
		"config_generated":   configGenerated,
		"config":             config,
	}
	if cfg, ok := config.(map[string]any); ok && cfg != nil {
		agentConfigs, _ := cfg["agent_configs"].([]any)
		timeConfig, _ := cfg["time_config"].(map[string]any)
		eventConfig, _ := cfg["event_config"].(map[string]any)
		initialPosts, _ := eventConfig["initial_posts"].([]any)
		hotTopics, _ := eventConfig["hot_topics"].([]any)
		_, hasTwitter := cfg["twitter_config"]
		_, hasReddit := cfg["reddit_config"]
		result["summary"] = map[string]any{
			"total_agents":        len(agentConfigs),
			"simulation_hours":    timeConfig["total_simulation_hours"],
			"initial_posts_count": len(initialPosts),
			"hot_topics_count":    len(hotTopics),
			"has_twitter_config":  hasTwitter,
			"has_reddit_config":   hasReddit,
			"generated_at":        cfg["generated_at"],
			"llm_model":           cfg["llm_model"],
		}
	}
	return result, true
}

// readTwitterCSV parsea los perfiles CSV de OASIS con DictReader semántica:
// la primera fila es cabecera y las filas cortas no abortan el resto.
func readTwitterCSV(raw []byte) ([]map[string]any, error) {
	reader := csv.NewReader(strings.NewReader(string(raw)))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	if len(records) < 2 {
		return out, nil
	}
	header := records[0]
	for _, row := range records[1:] {
		item := map[string]any{}
		for i, col := range header {
			if i < len(row) {
				item[col] = row[i]
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// SimulationIDsForGraph devuelve los ids de simulaciones que referencian un grafo.
func (m *Manager) SimulationIDsForGraph(graphID string) []string {
	out := []string{}
	for _, state := range m.ListSimulations("") {
		if state.GraphID == graphID {
			out = append(out, state.SimulationID)
		}
	}
	sort.Strings(out)
	return out
}

func newSimulationID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "sim_" + hex.EncodeToString(b[:])
}

func boolOr(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}
