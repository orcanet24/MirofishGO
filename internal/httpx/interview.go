// Entrevistas a agentes simulados vía HTTP (port de los endpoints de interview
// del backend Python, generadas en proceso con el LLM configurado).
package httpx

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mirofish/internal/locale"
	"mirofish/internal/oasis"
)

// interviewHTTPError — error con código HTTP propio (validación vs interno).
type interviewHTTPError struct {
	status int
	msg    string
}

func (e *interviewHTTPError) Error() string { return e.msg }

// interview — POST /api/simulation/interview (una entrevista individual).
func (h *SimulationHandler) interview(w http.ResponseWriter, r *http.Request) {
	loc := h.localeOf(r)
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	body, err := decodeBody(r)
	if err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	simulationID := strings.TrimSpace(stringField(body, "simulation_id"))
	if simulationID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": t("api.requireSimulationId", nil)})
		return
	}
	if !h.denyUnlessSimulationOwner(r, w, simulationID) {
		return
	}
	prompt := strings.TrimSpace(stringField(body, "prompt"))
	if prompt == "" {
		JSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": t("api.requirePrompt", nil)})
		return
	}
	interviews := []oasis.InterviewRequest{{
		AgentID:  intFromAny(body["agent_id"]),
		Prompt:   prompt,
		Platform: strings.TrimSpace(stringField(body, "platform")),
	}}
	out, err := h.runInterviews(r, simulationID, interviews)
	if err != nil {
		h.writeInterviewError(w, err)
		return
	}
	result := out["result"].(map[string]any)
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"agent_id":  interviews[0].AgentID,
			"prompt":    prompt,
			"result":    result["results"].(map[string]any)[interviewKey(interviews[0].Platform, interviews[0].AgentID)],
			"timestamp": time.Now().Format(time.RFC3339),
		},
	})
}

// interviewBatch — POST /api/simulation/interview/batch (la que usa el chat).
func (h *SimulationHandler) interviewBatch(w http.ResponseWriter, r *http.Request) {
	loc := h.localeOf(r)
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	body, err := decodeBody(r)
	if err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	simulationID := strings.TrimSpace(stringField(body, "simulation_id"))
	if simulationID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": t("api.requireSimulationId", nil)})
		return
	}
	if !h.denyUnlessSimulationOwner(r, w, simulationID) {
		return
	}
	rawList, ok := body["interviews"].([]any)
	if !ok || len(rawList) == 0 {
		JSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": t("api.requireInterviews", nil)})
		return
	}
	interviews := []oasis.InterviewRequest{}
	for i, raw := range rawList {
		m, ok := raw.(map[string]any)
		if !ok {
			JSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": t("api.requireInterviews", nil)})
			return
		}
		prompt := strings.TrimSpace(stringField(m, "prompt"))
		if prompt == "" {
			JSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": t("api.interviewListMissingPrompt", map[string]string{"index": strconv.Itoa(i + 1)})})
			return
		}
		interviews = append(interviews, oasis.InterviewRequest{
			AgentID:  intFromAny(m["agent_id"]),
			Prompt:   prompt,
			Platform: strings.TrimSpace(stringField(m, "platform")),
		})
	}
	out, err := h.runInterviews(r, simulationID, interviews)
	if err != nil {
		h.writeInterviewError(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

// interviewAll — POST /api/simulation/interview/all (todos los agentes).
func (h *SimulationHandler) interviewAll(w http.ResponseWriter, r *http.Request) {
	loc := h.localeOf(r)
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	body, err := decodeBody(r)
	if err != nil {
		HTTPError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	simulationID := strings.TrimSpace(stringField(body, "simulation_id"))
	if simulationID == "" {
		JSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": t("api.requireSimulationId", nil)})
		return
	}
	if !h.denyUnlessSimulationOwner(r, w, simulationID) {
		return
	}
	prompt := strings.TrimSpace(stringField(body, "prompt"))
	if prompt == "" {
		JSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": t("api.requirePrompt", nil)})
		return
	}
	maxAgents := intFromAny(body["max_agents"])
	if maxAgents <= 0 {
		maxAgents = 50
	}
	state := h.Manager.GetSimulation(simulationID)
	if state == nil {
		JSON(w, http.StatusNotFound, map[string]any{"success": false, "error": t("api.simulationNotFound", map[string]string{"id": simulationID})})
		return
	}
	platform := strings.TrimSpace(stringField(body, "platform"))
	if platform == "" {
		platform = state.GetDefaultPlatform()
	}
	profiles, err := h.Manager.GetProfiles(simulationID, platform)
	if err != nil {
		h.writeInterviewError(w, err)
		return
	}
	if len(profiles) == 0 {
		h.writeInterviewError(w, &interviewHTTPError{status: http.StatusBadRequest, msg: t("api.noProfilesForInterview", nil)})
		return
	}
	if len(profiles) > maxAgents {
		profiles = profiles[:maxAgents]
	}
	interviews := []oasis.InterviewRequest{}
	for i := range profiles {
		interviews = append(interviews, oasis.InterviewRequest{AgentID: i, Prompt: prompt, Platform: platform})
	}
	out, err := h.runInterviews(r, simulationID, interviews)
	if err != nil {
		h.writeInterviewError(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

// runInterviews — valida perfiles y requisito, y genera las respuestas.
func (h *SimulationHandler) runInterviews(r *http.Request, simulationID string, interviews []oasis.InterviewRequest) (map[string]any, error) {
	loc := h.localeOf(r)
	t := func(key string, args map[string]string) string { return locale.T(loc, key, args) }

	state := h.Manager.GetSimulation(simulationID)
	if state == nil {
		return nil, &interviewHTTPError{status: http.StatusNotFound, msg: t("api.simulationNotFound", map[string]string{"id": simulationID})}
	}
	platform := interviews[0].Platform
	if platform == "" {
		platform = state.GetDefaultPlatform()
	}
	if platform != "twitter" && platform != "reddit" {
		return nil, &interviewHTTPError{status: http.StatusBadRequest, msg: t("api.invalidInterviewPlatform", nil)}
	}
	profiles, err := h.Manager.GetProfiles(simulationID, platform)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, &interviewHTTPError{status: http.StatusBadRequest, msg: t("api.noProfilesForInterview", nil)}
	}
	requirement := ""
	if project := h.PM.GetProject(state.ProjectID); project != nil && project.SimulationRequirement != nil {
		requirement = *project.SimulationRequirement
	}
	results, err := oasis.InterviewAgents(interviews, profiles, requirement, loc, llmFactory(r))
	if err != nil {
		return nil, err
	}
	dict := map[string]any{}
	for _, res := range results {
		if res.Platform == "" {
			res.Platform = platform
		}
		dict[interviewKey(res.Platform, res.AgentID)] = res.ToMap()
	}
	return map[string]any{"result": map[string]any{
		"results":         dict,
		"interviews_count": len(results),
	}}, nil
}

func interviewKey(platform string, agentID int) string {
	return platform + "_" + strconv.Itoa(agentID)
}

func (h *SimulationHandler) writeInterviewError(w http.ResponseWriter, err error) {
	var he *interviewHTTPError
	if errors.As(err, &he) {
		JSON(w, he.status, map[string]any{"success": false, "error": he.msg})
		return
	}
	h.Logger.Error("interview error", "error", err)
	JSON(w, http.StatusInternalServerError, map[string]any{
		"success": false,
		"error":   "no se pudo generar la entrevista; revisa la config LLM",
	})
}
