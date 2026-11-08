// Package oasis — port del motor OASIS (clock, environment, social_agent y
// social_platform) sobre MySQL.
//
// Esta primera rebanada implementa la plataforma Twitter: reloj, entorno,
// agentes con perfil, signup, post, like, repost, quote, follow, refresh y
// recomendación. El estado social se guarda por simulation_id en la misma base
// MySQL del grafo, no en un SQLite por corrida.
package oasis

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ActionType replica los valores string de oasis.social_platform.typing.
type ActionType string

const (
	ActionExit        ActionType = "exit"
	ActionRefresh     ActionType = "refresh"
	ActionSearchUser  ActionType = "search_user"
	ActionSearchPosts ActionType = "search_posts"
	ActionCreatePost  ActionType = "create_post"
	ActionLikePost    ActionType = "like_post"
	ActionUnlikePost  ActionType = "unlike_post"
	ActionDislikePost ActionType = "dislike_post"
	ActionUndoDislike ActionType = "undo_dislike_post"
	ActionReportPost  ActionType = "report_post"
	ActionFollow      ActionType = "follow"
	ActionUnfollow    ActionType = "unfollow"
	ActionMute        ActionType = "mute"
	ActionUnmute      ActionType = "unmute"
	ActionTrend       ActionType = "trend"
	ActionSignUp      ActionType = "sign_up"
	ActionRepost      ActionType = "repost"
	ActionQuotePost   ActionType = "quote_post"
	ActionUpdateRec   ActionType = "update_rec_table"
	// ActionCreateComment replica Platform.create_comment de OASIS Twitter.
	ActionCreateComment ActionType = "create_comment"
	ActionDoNothing     ActionType = "do_nothing"
	ActionInterview     ActionType = "interview"
)

// SupportedTwitterActions es el subconjunto que esta rebanada ejecuta de
// verdad. El runner solo ofrece estas al LLM: prometer acciones que no existen
// haría que el agente eligiera una y fallara en cada ronda.
var SupportedTwitterActions = []ActionType{
	ActionCreatePost,
	ActionLikePost,
	ActionRepost,
	ActionFollow,
	ActionDoNothing,
	ActionQuotePost,
	ActionCreateComment,
}

// Action es una acción manual o la elegida por el LLM.
type Action struct {
	Type ActionType
	Args map[string]any
}

// ActionResult es el resultado observable de una acción.
type ActionResult struct {
	Action  ActionType     `json:"action"`
	Args    map[string]any `json:"args"`
	Data    map[string]any `json:"data,omitempty"`
	Success bool           `json:"success"`
	Error   string         `json:"error,omitempty"`
}

// Profile es la fila de twitter_profiles.csv que necesita el agente.
type Profile struct {
	UserID   int
	UserName string
	Name     string
	Bio      string
	Persona  string
}

// Post es una fila de oasis_posts.
type Post struct {
	PostID         int64
	UserID         int
	OriginalPostID *int64
	Content        string
	QuoteContent   string
	CreatedAt      string
	NumLikes       int
	NumDislikes    int
	NumShares      int
	NumReports     int
}

// ToMap replica la forma que devuelve Platform.refresh en Python.
func (p Post) ToMap() map[string]any {
	out := map[string]any{
		"post_id":      p.PostID,
		"user_id":      p.UserID,
		"content":      p.Content,
		"created_at":   p.CreatedAt,
		"num_likes":    p.NumLikes,
		"num_dislikes": p.NumDislikes,
		"num_shares":   p.NumShares,
	}
	if p.OriginalPostID != nil {
		out["original_post_id"] = *p.OriginalPostID
	} else {
		out["original_post_id"] = nil
	}
	if p.QuoteContent != "" {
		out["quote_content"] = p.QuoteContent
	} else {
		out["quote_content"] = nil
	}
	return out
}

// ToAPIMap devuelve la fila cruda de /api/simulation/<id>/posts, con la misma
// forma que la tabla post de OASIS (incluye num_reports).
func (p Post) ToAPIMap() map[string]any {
	out := p.ToMap()
	out["num_reports"] = p.NumReports
	return out
}

// Comment es una fila de oasis_comments.
type Comment struct {
	CommentID   int64
	PostID      int64
	UserID      int
	Content     string
	CreatedAt   string
	NumLikes    int
	NumDislikes int
}

// ToMap replica la fila cruda que devuelve /api/simulation/<id>/comments.
func (c Comment) ToMap() map[string]any {
	return map[string]any{
		"comment_id":   c.CommentID,
		"post_id":      c.PostID,
		"user_id":      c.UserID,
		"content":      c.Content,
		"created_at":   c.CreatedAt,
		"num_likes":    c.NumLikes,
		"num_dislikes": c.NumDislikes,
	}
}

// RunState es el estado de una ejecución (run_state.json en Python).
type RunState struct {
	SimulationID         string  `json:"simulation_id"`
	RunnerStatus         string  `json:"runner_status"`
	CurrentRound         int     `json:"current_round"`
	TotalRounds          int     `json:"total_rounds"`
	TwitterCurrentRound  int     `json:"twitter_current_round"`
	RedditCurrentRound   int     `json:"reddit_current_round"`
	TotalSimulationHours int     `json:"total_simulation_hours"`
	MinutesPerRound      int     `json:"minutes_per_round"`
	TwitterRunning       bool    `json:"twitter_running"`
	RedditRunning        bool    `json:"reddit_running"`
	TwitterActionsCount  int     `json:"twitter_actions_count"`
	RedditActionsCount   int     `json:"reddit_actions_count"`
	StartedAt            string  `json:"started_at"`
	UpdatedAt            string  `json:"updated_at"`
	Error                *string `json:"error"`
}

// ToMap replica RunState.to_dict de simulation_runner.py.
func (r RunState) ToMap() map[string]any {
	progress := 0.0
	if r.TotalRounds > 0 {
		progress = float64(r.CurrentRound) / float64(r.TotalRounds) * 100
		progress = float64(int(progress*10+0.5)) / 10
	}
	simulatedHours := 0
	if r.MinutesPerRound > 0 {
		simulatedHours = r.CurrentRound * r.MinutesPerRound / 60
	}
	return map[string]any{
		"simulation_id":          r.SimulationID,
		"runner_status":          r.RunnerStatus,
		"current_round":          r.CurrentRound,
		"total_rounds":           r.TotalRounds,
		"progress_percent":       progress,
		"simulated_hours":        simulatedHours,
		"twitter_simulated_hours": simulatedHours,
		"reddit_simulated_hours":  0,
		"total_simulation_hours":  r.TotalSimulationHours,
		"twitter_running":         r.TwitterRunning,
		"reddit_running":          r.RedditRunning,
		"twitter_current_round":   r.TwitterCurrentRound,
		"reddit_current_round":    r.RedditCurrentRound,
		"twitter_actions_count":   r.TwitterActionsCount,
		"reddit_actions_count":    r.RedditActionsCount,
		"total_actions_count":     r.TwitterActionsCount + r.RedditActionsCount,
		"twitter_completed":       r.RunnerStatus == "completed" && !r.TwitterRunning,
		"reddit_completed":        false,
		"started_at":              r.StartedAt,
		"updated_at":              r.UpdatedAt,
		"error":                   r.Error,
	}
}

// ActionRecord es una entrada de oasis_actions (y de actions.jsonl).
type ActionRecord struct {
	Round      int            `json:"round"`
	Timestamp  string         `json:"timestamp"`
	AgentID    int            `json:"agent_id"`
	AgentName  string         `json:"agent_name"`
	ActionType string         `json:"action_type"`
	ActionArgs map[string]any `json:"action_args"`
	Result     any            `json:"result"`
	Success    bool           `json:"success"`
	Platform   string         `json:"platform,omitempty"`
}

// ToMap replica la entrada de action_logger.py. La plataforma es "twitter"
// cuando no viene registrada: el motor nativo solo produce acciones de
// Twitter (Reddit no está portado) y el frontend las agrupa por plataforma.
func (a ActionRecord) ToMap() map[string]any {
	platform := a.Platform
	if platform == "" {
		platform = "twitter"
	}
	return map[string]any{
		"round":       a.Round,
		"timestamp":   a.Timestamp,
		"agent_id":    a.AgentID,
		"agent_name":  a.AgentName,
		"action_type": strings.ToUpper(a.ActionType),
		"action_args": a.ActionArgs,
		"result":      a.Result,
		"success":     a.Success,
		"platform":    platform,
	}
}

// ParseActionJSON interpreta la respuesta del LLM. Acepta el objeto directo o
// un fence de Markdown; rechaza acciones fuera de la lista soportada.
func ParseActionJSON(raw string) (Action, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var payload struct {
		Action string         `json:"action"`
		Args   map[string]any `json:"args"`
	}
	if err := json.Unmarshal([]byte(cleaned), &payload); err != nil {
		return Action{}, fmt.Errorf("respuesta del agente no es JSON válido: %w", err)
	}
	actionType := ActionType(strings.TrimSpace(payload.Action))
	if !IsSupportedTwitterAction(actionType) {
		return Action{}, fmt.Errorf("acción no soportada: %q", payload.Action)
	}
	if payload.Args == nil {
		payload.Args = map[string]any{}
	}
	return Action{Type: actionType, Args: payload.Args}, nil
}

// IsSupportedTwitterAction indica si la acción pertenece a la rebanada.
func IsSupportedTwitterAction(a ActionType) bool {
	for _, supported := range SupportedTwitterActions {
		if a == supported {
			return true
		}
	}
	return false
}

// intArg extrae un entero de args aceptando float64 (JSON) e int.
func intArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i), true
		}
	}
	return 0, false
}

// stringArg extrae un string de args.
func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}
