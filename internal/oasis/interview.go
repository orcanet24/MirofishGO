// Entrevistas a agentes simulados: generan respuestas en el persona de cada
// agente usando el LLM (equivale a OASIS interview_agent / interview_agents_batch
// del backend Python, pero en proceso, sin IPC).
package oasis

import (
	"fmt"
	"strconv"
	"strings"

	"mirofish/internal/llm"
	"mirofish/internal/locale"
)

// InterviewRequest — una entrevista a un agente simulado.
type InterviewRequest struct {
	AgentID  int    `json:"agent_id"`
	Prompt   string `json:"prompt"`
	Platform string `json:"platform,omitempty"`
}

// InterviewResult — respuesta generada en el persona del agente.
type InterviewResult struct {
	AgentID  int    `json:"agent_id"`
	Prompt   string `json:"prompt"`
	Response string `json:"response"`
	Platform string `json:"platform"`
}

// ToMap — forma dict para la respuesta HTTP (p.ej. {"twitter_0": {...}}).
func (r InterviewResult) ToMap() map[string]any {
	return map[string]any{
		"agent_id": r.AgentID,
		"prompt":   r.Prompt,
		"response": r.Response,
		"platform": r.Platform,
	}
}

// InterviewAgents responde cada entrevista con el LLM adoptando el perfil del
// agente indicado. profiles son los mapas de GetProfiles (claves user_id, name,
// username, user_char/description). loc define el idioma de la respuesta.
// factory opcional: config LLM por usuario; nil = global del .env.
func InterviewAgents(interviews []InterviewRequest, profiles []map[string]any, requirement, loc string, factory ...func() (*llm.Client, error)) ([]InterviewResult, error) {
	if len(interviews) == 0 {
		return nil, fmt.Errorf("no hay entrevistas")
	}
	resolve := func() (*llm.Client, error) {
		if len(factory) > 0 && factory[0] != nil {
			return factory[0]()
		}
		return llm.NewDefaultClient()
	}
	client, err := resolve()
	if err != nil {
		return nil, err
	}

	results := []InterviewResult{}
	for _, iv := range interviews {
		platform := iv.Platform
		if platform == "" {
			platform = "twitter"
		}
		profile := pickProfile(profiles, iv.AgentID)
		if profile == nil {
			results = append(results, InterviewResult{
				AgentID: iv.AgentID, Prompt: iv.Prompt, Platform: platform,
				Response: locale.T(loc, "prompt.interview.no_agent", nil),
			})
			continue
		}
		system := locale.T(loc, "prompt.interview.system", nil) + "\n\n" +
			profileText(profile) + "\n\n" +
			locale.T(loc, "prompt.interview.context", map[string]string{"requirement": requirement})
		content, err := chatWithDegeneracyGuard(client, system, iv.Prompt, loc)
		if err != nil {
			return nil, fmt.Errorf("el LLM no pudo responder la entrevista del agente %d: %w", iv.AgentID, err)
		}
		results = append(results, InterviewResult{
			AgentID: iv.AgentID, Prompt: iv.Prompt, Response: content, Platform: platform,
		})
	}
	return results, nil
}

// chatWithDegeneracyGuard evita las respuestas con bucles de repetición: si el
// primer intento degenera (muchas repeticiones), reintenta con temperatura
// baja y una instrucción anti-repetición; si sigue mal, devuelve error claro.
func chatWithDegeneracyGuard(client *llm.Client, system, prompt, loc string) (string, error) {
	content, err := client.Chat([]llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: prompt},
	}, 0.7, nil, nil)
	if err != nil {
		return "", err
	}
	if !degenerate(content) {
		return content, nil
	}
	retrySystem := system + "\n\n" + locale.T(loc, "prompt.interview.anti_repeat", nil)
	content2, err := client.Chat([]llm.Message{
		{Role: "system", Content: retrySystem},
		{Role: "user", Content: prompt},
	}, 0.2, nil, nil)
	if err != nil {
		return "", err
	}
	if degenerate(content2) {
		return "", fmt.Errorf("el modelo generó una respuesta degenerada (repeticiones); reintenta la pregunta")
	}
	return content2, nil
}

// degenerate detecta bucles de repetición: el mismo trigrama de palabras
// repetido más de 4 veces.
func degenerate(text string) bool {
	words := strings.Fields(text)
	if len(words) < 12 {
		return false
	}
	seen := map[string]int{}
	for i := 0; i+2 < len(words); i++ {
		g := strings.Join(words[i:i+3], " ")
		seen[g]++
		if seen[g] > 4 {
			return true
		}
	}
	return false
}

// pickProfile localiza el perfil por índice de lista (agent_id del frontend) o
// por user_id si coincide.
func pickProfile(profiles []map[string]any, agentID int) map[string]any {
	if agentID >= 0 && agentID < len(profiles) {
		return profiles[agentID]
	}
	for _, p := range profiles {
		if id, ok := p["user_id"]; ok {
			if n, err := strconv.Atoi(fmt.Sprint(id)); err == nil && n == agentID {
				return p
			}
		}
	}
	return nil
}

func profileText(p map[string]any) string {
	str := func(k string) string { return fmt.Sprint(p[k]) }
	name := str("name")
	username := str("username")
	bio := str("description")
	if bio == "" {
		bio = str("bio")
	}
	persona := str("user_char")
	if persona == "" {
		persona = str("persona")
	}
	out := "Nombre: " + name
	if username != "" {
		out += "\nUsuario: @" + username
	}
	if bio != "" {
		out += "\nBio: " + bio
	}
	if persona != "" {
		out += "\nPersonalidad: " + persona
	}
	return out
}
