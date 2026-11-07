package oasis

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"mirofish/internal/fsx"
	"mirofish/internal/llm"
	"mirofish/internal/timex"
)

// maxConcurrentLLM limita las peticiones simultáneas, como el semáforo de
// OasisEnv (30 en Python). Ocho mantiene el SaaS dentro de un rate limit
// razonable sin serializar la simulación.
const maxConcurrentLLM = 8

// Runner ejecuta una simulación Twitter completa.
type Runner struct {
	cfg     *SimulationConfig
	store   *Store
	twitter *Twitter
	llm     *llm.Client
	logger  *slog.Logger

	agents map[int]*Agent
	rng    *rand.Rand

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once

	mu           sync.Mutex
	state        RunState
	actionsCount int
}

// NewRunner prepara el runner (no arranca nada).
func NewRunner(cfg *SimulationConfig, store *Store, client *llm.Client, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	clock := NewClock(1)
	return &Runner{
		cfg:     cfg,
		store:   store,
		twitter: NewTwitter(cfg.SimulationID, store, clock),
		llm:     client,
		logger:  logger.With("simulation_id", cfg.SimulationID),
		agents:  map[int]*Agent{},
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// IsRunning indica si el goroutine sigue vivo.
func (r *Runner) IsRunning() bool {
	select {
	case <-r.doneCh:
		return false
	default:
		return true
	}
}

// Done se cierra al terminar.
func (r *Runner) Done() <-chan struct{} { return r.doneCh }

// State devuelve una copia del estado actual.
func (r *Runner) State() RunState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// initState fija el estado inicial de forma síncrona para que POST /start
// pueda devolverlo sin esperar a que arranque el goroutine.
func (r *Runner) initState(maxRounds int) RunState {
	r.mu.Lock()
	r.state = RunState{
		SimulationID:         r.cfg.SimulationID,
		RunnerStatus:         "running",
		CurrentRound:         0,
		TotalRounds:          r.cfg.TotalRounds(maxRounds),
		TotalSimulationHours: r.cfg.TimeConfig.TotalSimulationHours,
		MinutesPerRound:      r.cfg.TimeConfig.MinutesPerRound,
		TwitterRunning:       true,
		StartedAt:            timex.Now(),
		UpdatedAt:            timex.Now(),
	}
	state := r.state
	r.mu.Unlock()
	r.persistState(state)
	return state
}

// Stop pide terminar al final de la ronda en curso. Es idempotente.
func (r *Runner) Stop() {
	r.stopOnce.Do(func() {
		r.mu.Lock()
		if r.state.RunnerStatus == "running" {
			r.state.RunnerStatus = "stopping"
			r.state.TwitterRunning = false
			r.state.UpdatedAt = timex.Now()
			_ = r.store.SaveRunState(r.cfg.SimulationID, r.state)
		}
		r.mu.Unlock()
		close(r.stopCh)
	})
}

func (r *Runner) stopped() bool {
	select {
	case <-r.stopCh:
		return true
	default:
		return false
	}
}

// Run ejecuta la simulación. profiles ya viene validado por LoadTwitterProfiles.
func (r *Runner) Run(profiles []Profile, maxRounds int) {
	defer close(r.doneCh)
	defer func() {
		if recovered := recover(); recovered != nil {
			r.logger.Error("runner OASIS abortado por pánico", "error", recovered)
			r.finish("failed", fmt.Sprintf("panic: %v", recovered))
		}
	}()

	if err := r.store.Reset(r.cfg.SimulationID); err != nil {
		r.finish("failed", err.Error())
		return
	}
	if err := r.registerAgents(profiles); err != nil {
		r.finish("failed", err.Error())
		return
	}

	r.mu.Lock()
	uninitialized := r.state.RunnerStatus == ""
	r.mu.Unlock()
	if uninitialized {
		// Uso directo del runner (tests): el Engine ya lo inicializa antes de
		// lanzar el goroutine para poder responder a /start sin carrera.
		r.initState(maxRounds)
	}
	r.mu.Lock()
	totalRounds := r.state.TotalRounds
	r.mu.Unlock()
	r.persist()

	r.logger.Info("simulación OASIS iniciada", "agents", len(r.agents), "rounds", totalRounds)

	r.runInitialPosts()
	if err := r.twitter.UpdateRecTable(); err != nil {
		r.finish("failed", "no se pudo construir la tabla de recomendaciones: "+err.Error())
		return
	}

	for round := 0; round < totalRounds; round++ {
		if r.stopped() {
			break
		}
		active := r.activeAgents(round)
		r.runRound(round, active)

		r.twitter.Clock.Advance()
		r.mu.Lock()
		r.state.CurrentRound = round + 1
		r.state.TwitterCurrentRound = round + 1
		r.state.TwitterActionsCount = r.actionsCount
		r.state.UpdatedAt = timex.Now()
		r.mu.Unlock()
		r.persist()

		// La tabla se refresca con lo publicado en la ronda, igual que
		// env.step → platform.update_rec_table.
		if err := r.twitter.UpdateRecTable(); err != nil {
			r.logger.Warn("no se pudo actualizar la tabla de recomendaciones", "error", err)
		}
	}

	if r.stopped() {
		r.finish("stopped", "")
		return
	}
	r.finish("completed", "")
}

// registerAgents da de alta a todos los perfiles y construye los agentes.
func (r *Runner) registerAgents(profiles []Profile) error {
	for _, profile := range profiles {
		user, err := r.twitter.SignUp(profile.UserID, profile)
		if err != nil {
			return fmt.Errorf("signup del agente %d: %w", profile.UserID, err)
		}
		r.agents[profile.UserID] = &Agent{
			ID:       profile.UserID,
			Profile:  profile,
			Platform: r.twitter,
			LLM:      r.llm,
		}
		_ = user
	}
	return nil
}

// runInitialPosts replica el bloque de initial_posts del script Python.
func (r *Runner) runInitialPosts() {
	for _, post := range r.cfg.EventConfig.InitialPosts {
		if r.stopped() {
			return
		}
		agent, ok := r.agents[post.PosterAgentID]
		if !ok {
			r.logger.Warn("post inicial con poster_agent_id inexistente",
				"agent_id", post.PosterAgentID)
			continue
		}
		if post.Content == "" {
			continue
		}
		result := agent.PerformAction(Action{
			Type: ActionCreatePost,
			Args: map[string]any{"content": post.Content},
		})
		r.record(0, agent, result)
	}
}

// runRound ejecuta las acciones de los agentes activos en paralelo.
func (r *Runner) runRound(round int, agents []*Agent) {
	if len(agents) == 0 {
		return
	}
	sem := make(chan struct{}, maxConcurrentLLM)
	var wg sync.WaitGroup
	for _, agent := range agents {
		agent := agent
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result := agent.PerformActionByLLM()
			r.record(round+1, agent, result)
		}()
	}
	wg.Wait()
}

// record guarda la acción en MySQL y actualiza el contador en memoria.
func (r *Runner) record(round int, agent *Agent, result ActionResult) {
	args := result.Args
	if args == nil {
		args = map[string]any{}
	}
	record := ActionRecord{
		Round:      round,
		Timestamp:  timex.Now(),
		AgentID:    agent.ID,
		AgentName:  agent.Profile.Name,
		ActionType: string(result.Action),
		ActionArgs: args,
		Success:    result.Success,
	}
	if result.Data != nil {
		record.Result = result.Data
	} else if result.Error != "" {
		record.Result = map[string]any{"error": result.Error}
	}
	if result.Action == "" {
		record.ActionType = "error"
	}
	if err := r.store.AppendAction(r.cfg.SimulationID, record); err != nil {
		r.logger.Error("no se pudo guardar la acción", "error", err)
	}
	r.mu.Lock()
	r.actionsCount++
	r.mu.Unlock()
}

// activeAgents traduce _get_active_agents_for_round: hora simulada, multiplicador
// de franja y probabilidad por activity_level.
func (r *Runner) activeAgents(round int) []*Agent {
	minutes := round * r.cfg.TimeConfig.MinutesPerRound
	hour := (minutes / 60) % 24
	multiplier := 1.0
	if containsInt(r.cfg.TimeConfig.PeakHours, hour) {
		multiplier = r.cfg.TimeConfig.PeakActivityMultiplier
	} else if containsInt(r.cfg.TimeConfig.OffPeakHours, hour) {
		multiplier = r.cfg.TimeConfig.OffPeakActivityMultiplier
	}
	if multiplier <= 0 {
		multiplier = 1
	}

	baseMin := r.cfg.TimeConfig.AgentsPerHourMin
	baseMax := r.cfg.TimeConfig.AgentsPerHourMax
	// Igual que Python: la franja valle puede dar 0 agentes activos; no se
	// fuerza una acción porque eso falsearía el ritmo de la simulación.
	target := int((float64(baseMin) + r.rng.Float64()*float64(baseMax-baseMin)) * multiplier)
	if target < 0 {
		target = 0
	}

	configs := r.cfg.AgentConfigs
	if len(configs) == 0 {
		// Configuración sin agent_configs: activar a todos evita una
		// simulación vacía que parecería un fallo.
		out := make([]*Agent, 0, len(r.agents))
		for _, agent := range r.agents {
			out = append(out, agent)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		if len(out) > target {
			out = out[:target]
		}
		return out
	}

	candidates := []int{}
	for _, cfg := range configs {
		if _, ok := r.agents[cfg.AgentID]; !ok {
			continue
		}
		if len(cfg.ActiveHours) > 0 && !containsInt(cfg.ActiveHours, hour) {
			continue
		}
		activity := cfg.ActivityLevel
		if activity <= 0 {
			activity = 0.5
		}
		if r.rng.Float64() < activity {
			candidates = append(candidates, cfg.AgentID)
		}
	}
	r.rng.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	if len(candidates) > target {
		candidates = candidates[:target]
	}
	out := make([]*Agent, 0, len(candidates))
	for _, id := range candidates {
		out = append(out, r.agents[id])
	}
	return out
}

// persist publica el estado actual en MySQL y en run_state.json.
func (r *Runner) persist() {
	r.mu.Lock()
	state := r.state
	r.mu.Unlock()
	r.persistState(state)
}

func (r *Runner) persistState(state RunState) {
	if err := r.store.SaveRunState(r.cfg.SimulationID, state); err != nil {
		r.logger.Error("no se pudo guardar run_state en MySQL", "error", err)
	}
	if r.cfg.SimDir == "" {
		return
	}
	raw, err := json.MarshalIndent(state.ToMap(), "", "  ")
	if err != nil {
		r.logger.Error("no se pudo serializar run_state.json", "error", err)
		return
	}
	if err := fsx.WriteFileAtomic(filepath.Join(r.cfg.SimDir, "run_state.json"), raw, 0o644); err != nil {
		r.logger.Error("no se pudo escribir run_state.json", "error", err)
	}
}

// finish fija el estado terminal y lo persiste.
func (r *Runner) finish(status, errMsg string) {
	r.mu.Lock()
	r.state.RunnerStatus = status
	r.state.TwitterRunning = false
	r.state.TwitterActionsCount = r.actionsCount
	r.state.UpdatedAt = timex.Now()
	if errMsg != "" {
		r.state.Error = &errMsg
	}
	r.mu.Unlock()
	r.persist()
	if errMsg != "" {
		r.logger.Error("simulación OASIS terminó con error", "error", errMsg)
	} else {
		r.logger.Info("simulación OASIS terminada", "status", status,
			"actions", r.actionsCount)
	}
}

func containsInt(list []int, value int) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
