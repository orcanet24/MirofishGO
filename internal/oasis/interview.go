// Entrevistas a agentes simulados: generan respuestas en el persona de cada
// agente usando el LLM (equivale a OASIS interview_agent / interview_agents_batch
// del backend Python, pero en proceso, sin IPC).
package oasis

import (
	"fmt"
	"math/rand"
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
	// Depth (1-5) — nivel de profundidad pedido por el usuario para esta tanda.
	Depth int `json:"depth,omitempty"`
	// Rounds — rondas de preguntas por agente (0 = derivar de Depth).
	Rounds int `json:"rounds,omitempty"`
}

// DepthPlan devuelve (agentes, rondas) según el nivel de profundidad 1-5.
// DepthPlan devuelve (agentes, rondas) para una profundidad 0-100 (estilo
// slider): 0 = mínimo (2 agentes, 1 ronda), 100 = máximo (50 agentes, 5 rondas).
func DepthPlan(level int) (agents, rounds int) {
	if level < 0 {
		level = 0
	}
	if level > 100 {
		level = 100
	}
	agents = 2 + int(float64(level)/100*48)
	rounds = 1 + int(float64(level)/100*4)
	return
}

// DepthComplexity devuelve el índice de complejidad 0-4 para una profundidad
// 0-100 (para la etiqueta de la UI).
func DepthComplexity(level int) int {
	switch {
	case level <= 20:
		return 0
	case level <= 40:
		return 1
	case level <= 60:
		return 2
	case level <= 80:
		return 3
	default:
		return 4
	}
}

// expertRatio devuelve la fracción de perfiles expertos deseada por nivel
// (0-100; niveles altos priorizan expertos).
func expertRatio(level int) float64 {
	if level < 0 {
		level = 0
	}
	if level > 100 {
		level = 100
	}
	switch {
	case level <= 20:
		return 0
	case level <= 40:
		return 0.1
	case level <= 60:
		return 0.35
	case level <= 80:
		return 0.6
	default:
		return 0.85
	}
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
		rounds := iv.Rounds
		if rounds <= 0 && iv.Depth > 0 {
			_, rounds = DepthPlan(iv.Depth)
		}
		content, err := interviewOne(client, system, iv.Prompt, rounds, loc)
		if err != nil {
			return nil, fmt.Errorf("el LLM no pudo responder la entrevista del agente %d: %w", iv.AgentID, err)
		}
		results = append(results, InterviewResult{
			AgentID: iv.AgentID, Prompt: iv.Prompt, Response: content, Platform: platform,
		})
	}
	return results, nil
}

// interviewOne ejecuta una entrevista de N rondas a un agente: pregunta
// inicial + seguimientos generados por el LLM (más profundidad = más rondas).
func interviewOne(client *llm.Client, system, question string, rounds int, loc string) (string, error) {
	if rounds <= 1 {
		return chatWithDegeneracyGuard(client, system, question, loc)
	}
	var b strings.Builder
	messages := []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: question},
	}
	answer, err := chatWithDegeneracyGuard(client, system, question, loc)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "Pregunta 1: %s\nRespuesta 1: %s", question, answer)
	messages = append(messages,
		llm.Message{Role: "assistant", Content: answer},
	)
	for r := 2; r <= rounds; r++ {
		probeSystem := locale.T(loc, "prompt.interview.probeSystem", nil)
		probe, err := client.Chat([]llm.Message{
			{Role: "system", Content: probeSystem},
			{Role: "user", Content: answer},
		}, 0.5, nil, nil)
		if err != nil || strings.TrimSpace(probe) == "" {
			break
		}
		probe = cleanProbe(probe)
		messages = append(messages, llm.Message{Role: "user", Content: probe})
		next, err := chatWithDegeneracyGuard(client, system, probe, loc)
		if err != nil {
			break
		}
		fmt.Fprintf(&b, "\n\nPregunta %d: %s\nRespuesta %d: %s", r, probe, r, next)
		messages = append(messages, llm.Message{Role: "assistant", Content: next})
		answer = next
	}
	return b.String(), nil
}

// cleanProbe deja la pregunta de seguimiento en una sola línea y sin prefijos.
func cleanProbe(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "- ")
	return strings.TrimSpace(s)
}

// SelectAgents elige índices de perfiles según la profundidad: niveles altos
// priorizan perfiles expertos (profesión/educación), niveles bajos cotidianos.
func SelectAgents(profiles []map[string]any, level, count int) []int {
	if count <= 0 {
		count, _ = DepthPlan(level)
	}
	n := len(profiles)
	if n == 0 {
		return []int{}
	}
	if count >= n {
		count = n
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	rand.Shuffle(n, func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	if expertRatio(level) <= 0 {
		return idx[:count]
	}
	// Ordena por puntuación experta y toma la élite requerida + aleatorios.
	scores := make([]int, n)
	for i, p := range profiles {
		scores[i] = expertScore(p)
	}
	rand.Shuffle(n, func(i, j int) {
		idx[i], idx[j] = idx[j], idx[i]
		scores[i], scores[j] = scores[j], scores[i]
	})
	// insertion sort descendente por score (estable con aleatorio previo)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && scores[j] > scores[j-1]; j-- {
			scores[j], scores[j-1] = scores[j-1], scores[j]
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
	expertCount := int(float64(count) * expertRatio(level))
	if expertCount > count {
		expertCount = count
	}
	_ = expertCount // los expertos quedan primeros por el orden de score
	return idx[:count]
}

// expertScore puntúa rasgos de "perfil experto" (0-3).
func expertScore(p map[string]any) int {
	score := 0
	prof := strings.ToLower(fmt.Sprint(p["profession"]))
	edu := strings.ToLower(fmt.Sprint(p["education"]))
	expertWords := []string{"doctor", "doctora", "ingenier", "profesor", "abogad", "economista",
		"científic", "cientific", "ceo", "director", "directora", "experto", "experta",
		"analista", "polític", "politic", "gerente", "ejecutiv", "investigador", "investigadora",
		"consultor", "consultora", "magíster", "magister", "phd", "licenciad"}
	for _, w := range expertWords {
		if strings.Contains(prof, w) {
			score += 2
			break
		}
	}
	if strings.Contains(edu, "highly") || strings.Contains(edu, "professional") || strings.Contains(edu, "universit") {
		score++
	}
	return score
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
