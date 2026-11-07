package oasis

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"mirofish/internal/llm"
	"mirofish/internal/logx"
)

// EngineOption configura el motor (tests: LLM falsa, logger).
type EngineOption func(*Engine)

// WithEngineLLMFactory inyecta el constructor de cliente LLM.
func WithEngineLLMFactory(factory func() (*llm.Client, error)) EngineOption {
	return func(e *Engine) { e.llmFactory = factory }
}

// WithEngineLogger inyecta el logger.
func WithEngineLogger(logger *slog.Logger) EngineOption {
	return func(e *Engine) { e.logger = logger }
}

// Engine coordina las ejecuciones activas por simulation_id.
type Engine struct {
	store      *Store
	logger     *slog.Logger
	llmFactory func() (*llm.Client, error)

	mu      sync.Mutex
	runners map[string]*Runner
}

// NewEngine crea el motor sobre el almacén social.
func NewEngine(store *Store, opts ...EngineOption) *Engine {
	e := &Engine{
		store:      store,
		logger:     logx.Get("mirofish.oasis"),
		llmFactory: llm.NewDefaultClient,
		runners:    map[string]*Runner{},
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Start lanza una simulación. force detiene una corrida previa (activa o
// terminada) antes de empezar de cero.
func (e *Engine) Start(cfg *SimulationConfig, profiles []Profile, maxRounds int, force bool) (*RunState, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuración de simulación vacía")
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("no hay perfiles de agentes para ejecutar")
	}

	e.mu.Lock()
	previous := e.runners[cfg.SimulationID]
	if previous != nil && previous.IsRunning() {
		if !force {
			e.mu.Unlock()
			return nil, fmt.Errorf("la simulación ya está en ejecución; usa force=true para reiniciarla")
		}
		// No retener el lock global mientras se espera: otro simulation_id
		// debe poder arrancar en paralelo.
		e.mu.Unlock()
		previous.Stop()
		select {
		case <-previous.Done():
		case <-time.After(30 * time.Second):
			return nil, fmt.Errorf("la ejecución anterior no terminó a tiempo")
		}
		e.mu.Lock()
	}

	// Una corrida anterior sin runner vivo (proceso reiniciado) también exige
	// force: reiniciar sin querer borraría el histórico social.
	previousRun, err := e.store.LoadRunState(cfg.SimulationID)
	if err != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("no se pudo leer run_state: %w", err)
	}
	if previousRun != nil {
		switch previousRun.RunnerStatus {
		case "running", "stopping", "paused":
			// Sin runner en memoria puede ser un proceso reiniciado. force
			// permite recuperarlo; en un despliegue multi-instancia esto
			// exige coordinación externa (ver PROGRESO.md).
			if !force {
				e.mu.Unlock()
				return nil, fmt.Errorf("hay una ejecución previa marcada como %s; usa force=true", previousRun.RunnerStatus)
			}
		case "completed", "stopped", "failed":
			if !force {
				e.mu.Unlock()
				return nil, fmt.Errorf("la simulación ya se ejecutó (%s); usa force=true para repetirla", previousRun.RunnerStatus)
			}
		}
	}

	client, err := e.llmFactory()
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	runner := NewRunner(cfg, e.store, client, e.logger)
	initial := runner.initState(maxRounds)
	e.runners[cfg.SimulationID] = runner
	e.mu.Unlock()

	go runner.Run(profiles, maxRounds)
	return &initial, nil
}

// Stop detiene la simulación y espera a que el runner cierre la ronda actual.
func (e *Engine) Stop(simulationID string) (*RunState, error) {
	e.mu.Lock()
	runner := e.runners[simulationID]
	e.mu.Unlock()
	if runner == nil {
		return nil, fmt.Errorf("no hay una ejecución activa para %s", simulationID)
	}
	if !runner.IsRunning() {
		state := runner.State()
		return &state, nil
	}
	runner.Stop()
	select {
	case <-runner.Done():
	case <-time.After(30 * time.Second):
		state := runner.State()
		return &state, fmt.Errorf("la simulación sigue cerrando la ronda actual; vuelve a consultar run-status")
	}
	state := runner.State()
	return &state, nil
}

// State devuelve el run_state persistido (nil si nunca se ejecutó).
func (e *Engine) State(simulationID string) (*RunState, error) {
	return e.store.LoadRunState(simulationID)
}

// IsRunning consulta el runner en memoria.
func (e *Engine) IsRunning(simulationID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	runner := e.runners[simulationID]
	return runner != nil && runner.IsRunning()
}

// Actions devuelve las acciones registradas con los filtros del blueprint.
func (e *Engine) Actions(simulationID string, limit, offset int, platform string, agentID, roundNum *int) ([]ActionRecord, error) {
	return e.store.ListActions(simulationID, limit, offset, platform, agentID, roundNum)
}

// Posts devuelve los posts de la simulación, del más nuevo al más viejo.
func (e *Engine) Posts(simulationID string, limit, offset int) ([]Post, error) {
	return e.store.ListPosts(simulationID, limit, offset)
}

// CountPosts cuenta los posts de la simulación.
func (e *Engine) CountPosts(simulationID string) (int, error) {
	return e.store.CountPosts(simulationID)
}

// Comments devuelve los comentarios, opcionalmente filtrados por post.
func (e *Engine) Comments(simulationID string, postID *int64, limit, offset int) ([]Comment, error) {
	return e.store.ListComments(simulationID, postID, limit, offset)
}
