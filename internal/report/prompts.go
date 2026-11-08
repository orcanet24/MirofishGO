// Prompts del Report Agent (port de report_agent.py).
//
// Los textos viven en el sistema de locales (internal/locale/*.json) bajo
// prompt.report.* y se resuelven con locale.T, con fallback a "es". Las
// plantillas conservan los placeholders de fmt.Sprintf (%s/%d/%v); las llaves
// { } que aparecen en ejemplos JSON son literales y no se interpolan.
package report

import "mirofish/internal/locale"

// defaultPromptLocale se usa cuando el agente no trae locale explícito.
const defaultPromptLocale = "es"

// promptLocale normaliza el locale del agente, con "es" como valor por defecto.
func promptLocale(loc string) string {
	if loc == "" {
		return defaultPromptLocale
	}
	return loc
}

// PlanSystemPrompt devuelve el system prompt de planificación del esquema.
func PlanSystemPrompt(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.plan_system", nil)
}

// PlanUserPromptTemplate devuelve la plantilla del user prompt de planificación.
func PlanUserPromptTemplate(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.plan_user", nil)
}

// SectionSystemPromptTemplate devuelve la plantilla del system prompt de sección.
func SectionSystemPromptTemplate(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.section_system", nil)
}

// SectionUserPromptTemplate devuelve la plantilla del user prompt de sección.
func SectionUserPromptTemplate(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.section_user", nil)
}

// ReactObservationTemplate devuelve la plantilla de observación del bucle ReAct.
func ReactObservationTemplate(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.react_observation", nil)
}

// ReactInsufficientToolsMsg devuelve el aviso de llamadas a herramientas insuficientes.
func ReactInsufficientToolsMsg(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.react_insufficient", nil)
}

// ReactToolLimitMsg devuelve el aviso de límite de llamadas a herramientas.
func ReactToolLimitMsg(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.react_tool_limit", nil)
}

// ReactForceFinalMsg devuelve la orden de forzar respuesta final.
func ReactForceFinalMsg(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.react_force_final", nil)
}

// ReactUnusedToolsHint devuelve la sugerencia de herramientas sin usar.
func ReactUnusedToolsHint(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.react_unused_hint", nil)
}

// ChatSystemPromptTemplate devuelve la plantilla del system prompt del chat.
func ChatSystemPromptTemplate(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.chat_system", nil)
}

// ChatObservationSuffix devuelve el sufijo añadido tras una observación en el chat.
func ChatObservationSuffix(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.chat_observation_suffix", nil)
}

// ToolDescInsightForge devuelve la descripción de la herramienta insight_forge.
func ToolDescInsightForge(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.tool.insight_forge", nil)
}

// ToolDescPanoramaSearch devuelve la descripción de la herramienta panorama_search.
func ToolDescPanoramaSearch(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.tool.panorama_search", nil)
}

// ToolDescQuickSearch devuelve la descripción de la herramienta quick_search.
func ToolDescQuickSearch(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.tool.quick_search", nil)
}

// ToolDescInterviewAgents devuelve la descripción de la herramienta interview_agents.
func ToolDescInterviewAgents(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.tool.interview_agents", nil)
}

// ToolDescWebSearch devuelve la descripción de la herramienta web_search.
func ToolDescWebSearch(loc string) string {
	return locale.T(promptLocale(loc), "prompt.report.tool.web_search", nil)
}
