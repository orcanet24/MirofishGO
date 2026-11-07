package oasis

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// TimeConfig replica la parte de time_config que usa el runner.
type TimeConfig struct {
	TotalSimulationHours      int     `json:"total_simulation_hours"`
	MinutesPerRound           int     `json:"minutes_per_round"`
	AgentsPerHourMin          int     `json:"agents_per_hour_min"`
	AgentsPerHourMax          int     `json:"agents_per_hour_max"`
	PeakHours                 []int   `json:"peak_hours"`
	OffPeakHours              []int   `json:"off_peak_hours"`
	PeakActivityMultiplier    float64 `json:"peak_activity_multiplier"`
	OffPeakActivityMultiplier float64 `json:"off_peak_activity_multiplier"`
}

// AgentConfig replica la parte de agent_configs que usa el runner.
type AgentConfig struct {
	AgentID       int     `json:"agent_id"`
	ActivityLevel float64 `json:"activity_level"`
	ActiveHours   []int   `json:"active_hours"`
}

// InitialPost es una entrada de event_config.initial_posts.
type InitialPost struct {
	Content       string `json:"content"`
	PosterType    string `json:"poster_type"`
	PosterAgentID int    `json:"poster_agent_id"`
}

// EventConfig contiene los eventos iniciales.
type EventConfig struct {
	InitialPosts []InitialPost `json:"initial_posts"`
}

// SimulationConfig es el subconjunto de simulation_config.json que necesita
// esta rebanada. Los campos desconocidos se ignoran a propósito para no
// romperse con las claves que genera el configurador.
type SimulationConfig struct {
	SimulationID string        `json:"simulation_id"`
	ProjectID    string        `json:"project_id"`
	GraphID      string        `json:"graph_id"`
	TimeConfig   TimeConfig    `json:"time_config"`
	AgentConfigs []AgentConfig `json:"agent_configs"`
	EventConfig  EventConfig   `json:"event_config"`

	// SimDir lo fija el handler; el runner publica ahí run_state.json para
	// que /api/report/generate y las herramientas Python sigan funcionando.
	SimDir string `json:"-"`
}

// LoadSimulationConfig lee el JSON y aplica los defaults del runner Python.
func LoadSimulationConfig(path string) (*SimulationConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg SimulationConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("simulation_config.json inválido: %w", err)
	}
	if strings.TrimSpace(cfg.SimulationID) == "" {
		return nil, fmt.Errorf("simulation_config.json sin simulation_id")
	}
	if cfg.TimeConfig.TotalSimulationHours <= 0 {
		cfg.TimeConfig.TotalSimulationHours = 72
	}
	if cfg.TimeConfig.MinutesPerRound <= 0 {
		cfg.TimeConfig.MinutesPerRound = 60
	}
	if cfg.TimeConfig.AgentsPerHourMin <= 0 {
		cfg.TimeConfig.AgentsPerHourMin = 5
	}
	if cfg.TimeConfig.AgentsPerHourMax < cfg.TimeConfig.AgentsPerHourMin {
		cfg.TimeConfig.AgentsPerHourMax = cfg.TimeConfig.AgentsPerHourMin
	}
	if cfg.TimeConfig.PeakActivityMultiplier <= 0 {
		cfg.TimeConfig.PeakActivityMultiplier = 1.5
	}
	if cfg.TimeConfig.OffPeakActivityMultiplier <= 0 {
		cfg.TimeConfig.OffPeakActivityMultiplier = 0.3
	}
	if len(cfg.TimeConfig.PeakHours) == 0 {
		cfg.TimeConfig.PeakHours = []int{9, 10, 11, 14, 15, 20, 21, 22}
	}
	if len(cfg.TimeConfig.OffPeakHours) == 0 {
		cfg.TimeConfig.OffPeakHours = []int{0, 1, 2, 3, 4, 5}
	}
	return &cfg, nil
}

// TotalRounds calcula las rondas como el runner Python:
// (horas*60)/minutos_por_ronda, truncado opcionalmente por max_rounds.
func (c *SimulationConfig) TotalRounds(maxRounds int) int {
	total := c.TimeConfig.TotalSimulationHours * 60 / c.TimeConfig.MinutesPerRound
	if total < 0 {
		total = 0
	}
	if maxRounds > 0 && maxRounds < total {
		return maxRounds
	}
	return total
}

// LoadTwitterProfiles lee twitter_profiles.csv con la misma cabecera que
// escribe simx (user_id,name,username,user_char,description).
func LoadTwitterProfiles(path string) ([]Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("twitter_profiles.csv inválido: %w", err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("twitter_profiles.csv no contiene agentes")
	}
	header := map[string]int{}
	for i, name := range rows[0] {
		header[strings.TrimSpace(name)] = i
	}
	col := func(row []string, name string) string {
		i, ok := header[name]
		if !ok || i >= len(row) {
			return ""
		}
		return row[i]
	}

	profiles := []Profile{}
	seen := map[int]bool{}
	for idx, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		agentID := idx
		if raw := strings.TrimSpace(col(row, "user_id")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				agentID = n
			}
		}
		if seen[agentID] {
			return nil, fmt.Errorf("twitter_profiles.csv repite user_id=%d", agentID)
		}
		seen[agentID] = true

		name := strings.TrimSpace(col(row, "name"))
		if name == "" {
			name = fmt.Sprintf("Agent %d", agentID)
		}
		bio := strings.TrimSpace(col(row, "description"))
		if bio == "" {
			bio = strings.TrimSpace(col(row, "user_char"))
		}
		persona := strings.TrimSpace(col(row, "user_char"))
		if persona == "" {
			persona = bio
		}
		profiles = append(profiles, Profile{
			UserID:   agentID,
			UserName: col(row, "username"),
			Name:     name,
			Bio:      bio,
			Persona:  persona,
		})
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("twitter_profiles.csv no contiene agentes")
	}
	return profiles, nil
}
