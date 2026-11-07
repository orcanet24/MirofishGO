// Rutas de lectura analítica del blueprint de simulación: timeline,
// agent-stats, posts y comments. Todas leen el estado OASIS en MySQL y
// respetan la forma de respuesta del backend Python.
package httpx

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"mirofish/internal/locale"
)

const (
	defaultReadLimit = 50
	maxReadLimit     = 1000
	// El Python agrega hasta 10000 acciones para timeline y agent-stats.
	timelineActionLimit = 10000
)

func (h *SimulationHandler) simulationTimeline(w http.ResponseWriter, r *http.Request, simulationID string) {
	query := r.URL.Query()
	startRound := 0
	if n, err := strconv.Atoi(query.Get("start_round")); err == nil {
		startRound = n
	}
	var endRound *int
	if raw := strings.TrimSpace(query.Get("end_round")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			endRound = &n
		}
	}

	actions, err := h.Oasis.Actions(simulationID, timelineActionLimit, 0, "", nil, nil)
	if err != nil {
		h.Logger.Error("获取时间线失败", "simulation_id", simulationID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	type roundAgg struct {
		round       int
		twitter     int
		reddit      int
		agents      map[int]bool
		actionTypes map[string]int
		first       string
		last        string
	}
	rounds := map[int]*roundAgg{}
	for _, action := range actions {
		if action.Round < startRound {
			continue
		}
		if endRound != nil && action.Round > *endRound {
			continue
		}
		agg := rounds[action.Round]
		if agg == nil {
			agg = &roundAgg{
				round:       action.Round,
				agents:      map[int]bool{},
				actionTypes: map[string]int{},
				first:       action.Timestamp,
				last:        action.Timestamp,
			}
			rounds[action.Round] = agg
		}
		// El motor nativo solo produce Twitter; el campo reddit se mantiene
		// en el contrato para cuando se porte la segunda plataforma.
		agg.twitter++
		agg.agents[action.AgentID] = true
		agg.actionTypes[action.ActionType]++
		if action.Timestamp < agg.first {
			agg.first = action.Timestamp
		}
		if action.Timestamp > agg.last {
			agg.last = action.Timestamp
		}
	}

	roundNums := make([]int, 0, len(rounds))
	for n := range rounds {
		roundNums = append(roundNums, n)
	}
	sort.Ints(roundNums)

	timeline := make([]map[string]any, 0, len(roundNums))
	for _, n := range roundNums {
		agg := rounds[n]
		agentIDs := make([]int, 0, len(agg.agents))
		for id := range agg.agents {
			agentIDs = append(agentIDs, id)
		}
		sort.Ints(agentIDs)
		timeline = append(timeline, map[string]any{
			"round_num":           agg.round,
			"twitter_actions":     agg.twitter,
			"reddit_actions":      agg.reddit,
			"total_actions":       agg.twitter + agg.reddit,
			"active_agents_count": len(agentIDs),
			"active_agents":       agentIDs,
			"action_types":        agg.actionTypes,
			"first_action_time":   agg.first,
			"last_action_time":    agg.last,
		})
	}

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"rounds_count": len(timeline),
			"timeline":     timeline,
		},
	})
}

func (h *SimulationHandler) simulationAgentStats(w http.ResponseWriter, r *http.Request, simulationID string) {
	actions, err := h.Oasis.Actions(simulationID, timelineActionLimit, 0, "", nil, nil)
	if err != nil {
		h.Logger.Error("获取Agent统计失败", "simulation_id", simulationID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	type agentAgg struct {
		agentID     int
		agentName   string
		total       int
		twitter     int
		reddit      int
		actionTypes map[string]int
		first       string
		last        string
	}
	byAgent := map[int]*agentAgg{}
	for _, action := range actions {
		agg := byAgent[action.AgentID]
		if agg == nil {
			agg = &agentAgg{
				agentID:     action.AgentID,
				agentName:   action.AgentName,
				actionTypes: map[string]int{},
				first:       action.Timestamp,
				last:        action.Timestamp,
			}
			byAgent[action.AgentID] = agg
		}
		agg.total++
		agg.twitter++
		if agg.agentName == "" {
			agg.agentName = action.AgentName
		}
		agg.actionTypes[action.ActionType]++
		if action.Timestamp < agg.first {
			agg.first = action.Timestamp
		}
		if action.Timestamp > agg.last {
			agg.last = action.Timestamp
		}
	}

	stats := make([]map[string]any, 0, len(byAgent))
	for _, agg := range byAgent {
		stats = append(stats, map[string]any{
			"agent_id":          agg.agentID,
			"agent_name":        agg.agentName,
			"total_actions":     agg.total,
			"twitter_actions":   agg.twitter,
			"reddit_actions":    agg.reddit,
			"action_types":      agg.actionTypes,
			"first_action_time": agg.first,
			"last_action_time":  agg.last,
		})
	}
	sort.Slice(stats, func(i, j int) bool {
		ti := stats[i]["total_actions"].(int)
		tj := stats[j]["total_actions"].(int)
		if ti != tj {
			return ti > tj
		}
		return stats[i]["agent_id"].(int) < stats[j]["agent_id"].(int)
	})

	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"agents_count": len(stats),
			"stats":        stats,
		},
	})
}

// readPlatform normaliza el parámetro platform. El motor nativo solo tiene
// Twitter; cualquier otro valor devuelve una lista vacía, nunca datos falsos.
func readPlatform(r *http.Request) string {
	platform := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("platform")))
	if platform == "" || platform == "parallel" {
		return "twitter"
	}
	return platform
}

func readLimit(r *http.Request, def int) int {
	limit := def
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	if limit > maxReadLimit {
		limit = maxReadLimit
	}
	return limit
}

func readOffset(r *http.Request) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 {
		return n
	}
	return 0
}

func (h *SimulationHandler) simulationPosts(w http.ResponseWriter, r *http.Request, simulationID string) {
	loc := h.localeOf(r)
	platform := readPlatform(r)
	limit := readLimit(r, defaultReadLimit)
	offset := readOffset(r)

	if platform != "twitter" {
		JSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data": map[string]any{
				"platform": platform,
				"count":    0,
				"posts":    []any{},
				"message":  locale.T(loc, "api.dbNotExist", nil),
			},
		})
		return
	}

	posts, err := h.Oasis.Posts(simulationID, limit, offset)
	if err != nil {
		h.Logger.Error("获取帖子失败", "simulation_id", simulationID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	total, err := h.Oasis.CountPosts(simulationID)
	if err != nil {
		h.Logger.Error("获取帖子失败", "simulation_id", simulationID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	maps := make([]map[string]any, 0, len(posts))
	for _, post := range posts {
		maps = append(maps, post.ToAPIMap())
	}
	data := map[string]any{
		"platform": platform,
		"total":    total,
		"count":    len(maps),
		"posts":    maps,
	}
	// En Python el mensaje solo aparece cuando el archivo de la simulación
	// aún no existe. Aquí equivale a una simulación nunca ejecutada.
	if total == 0 {
		if state, _ := h.Oasis.State(simulationID); state == nil {
			data["message"] = locale.T(loc, "api.dbNotExist", nil)
		}
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

func (h *SimulationHandler) simulationComments(w http.ResponseWriter, r *http.Request, simulationID string) {
	platform := readPlatform(r)
	if platform != "twitter" {
		JSON(w, http.StatusOK, map[string]any{
			"success": true,
			"data":    map[string]any{"count": 0, "comments": []any{}},
		})
		return
	}

	limit := readLimit(r, defaultReadLimit)
	offset := readOffset(r)

	var postID *int64
	if raw := strings.TrimSpace(r.URL.Query().Get("post_id")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			// El Python compara el string contra post_id y no encuentra filas:
			// un id inválido es una lista vacía, no un 400.
			JSON(w, http.StatusOK, map[string]any{
				"success": true,
				"data":    map[string]any{"count": 0, "comments": []any{}},
			})
			return
		}
		postID = &n
	}

	comments, err := h.Oasis.Comments(simulationID, postID, limit, offset)
	if err != nil {
		h.Logger.Error("获取评论失败", "simulation_id", simulationID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	maps := make([]map[string]any, 0, len(comments))
	for _, comment := range comments {
		maps = append(maps, comment.ToMap())
	}
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"count":    len(maps),
			"comments": maps,
		},
	})
}
