// Report Agent (port de ReportAgent).
//
// Flujo: planificar índice con el LLM → por cada capítulo ejecutar un bucle
// ReAct (mínimo 3 herramientas, máximo 8) → guardar section_NN.md en cuanto
// termina → ensamblar full_report.md. Los logs (agent_log.jsonl y
// console_log.txt) se escriben durante el proceso para que la UI pueda hacer
// streaming por polling con from_line.
package report

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"mirofish/internal/llm"
	"mirofish/internal/locale"
	"mirofish/internal/timex"
)

const (
	maxToolCallsPerSection = 8
	softToolLimit          = 5
	maxSectionIterations   = 5
	minToolCalls           = 3
	maxToolCallsPerChat    = 2
	maxSectionTokens       = 4096
)

// ProgressCallback replica progress_callback(stage, progress, message).
type ProgressCallback func(stage string, progress int, message string)

// Agent replica ReportAgent.
type Agent struct {
	GraphID               string
	SimulationID          string
	SimulationRequirement string
	UserID                string
	Locale                string

	Manager    *Manager
	Tools      *Tools
	LLMFactory func() (*llm.Client, error)

	logger  *agentLogger
	console *consoleLogger
}

// NewAgent crea el agente para una simulación/grafo.
func NewAgent(manager *Manager, tools *Tools, graphID, simulationID, requirement, userID, localeCode string) *Agent {
	return &Agent{
		GraphID:               graphID,
		SimulationID:          simulationID,
		SimulationRequirement: requirement,
		UserID:                userID,
		Locale:                localeCode,
		Manager:               manager,
		Tools:                 tools,
	}
}

func (a *Agent) t(key string, args map[string]string) string {
	return locale.T(a.Locale, key, args)
}

func (a *Agent) llmClient() (*llm.Client, error) {
	if a.LLMFactory != nil {
		return a.LLMFactory()
	}
	return llm.NewDefaultClient()
}

func (a *Agent) langInstruction() string {
	return locale.GetLanguageInstruction(a.Locale)
}

// NewReportID replica f"report_{uuid4().hex[:12]}".
func NewReportID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "report_" + hex.EncodeToString(b[:])
}

// ===== herramientas =====

type toolDef struct {
	Name        string
	Description string
	Parameters  map[string]string
}

func (a *Agent) toolDefs() map[string]toolDef {
	return map[string]toolDef{
		"insight_forge": {
			Name:        "insight_forge",
			Description: toolDescInsightForge,
			Parameters: map[string]string{
				"query":          "你想深入分析的问题或话题",
				"report_context": "当前报告章节的上下文（可选）",
			},
		},
		"panorama_search": {
			Name:        "panorama_search",
			Description: toolDescPanoramaSearch,
			Parameters: map[string]string{
				"query":           "搜索查询，用于相关性排序",
				"include_expired": "是否包含过期/历史内容（默认True）",
			},
		},
		"quick_search": {
			Name:        "quick_search",
			Description: toolDescQuickSearch,
			Parameters: map[string]string{
				"query": "搜索查询字符串",
				"limit": "返回结果数量（可选，默认10）",
			},
		},
		"interview_agents": {
			Name:        "interview_agents",
			Description: toolDescInterviewAgents,
			Parameters: map[string]string{
				"interview_topic": "采访主题或需求描述",
				"max_agents":      "最多采访的Agent数量（可选，默认5，最大10）",
			},
		},
	}
}

func (a *Agent) toolsDescription() string {
	parts := []string{"可用工具："}
	defs := a.toolDefs()
	for _, name := range []string{"insight_forge", "panorama_search", "quick_search", "interview_agents"} {
		tool := defs[name]
		parts = append(parts, "- "+name+": "+tool.Description)
		params := []string{}
		for k, v := range tool.Parameters {
			params = append(params, k+": "+v)
		}
		if len(params) > 0 {
			parts = append(parts, "  参数: "+strings.Join(params, ", "))
		}
	}
	return strings.Join(parts, "\n")
}

// ExecuteTool replica _execute_tool: devuelve texto para el LLM y nunca
// propaga errores (los convierte en mensajes de herramienta).
func (a *Agent) ExecuteTool(name string, params map[string]any, reportContext string) string {
	if a.console != nil {
		a.console.Info("执行工具: %s %v", name, params)
	}
	switch name {
	case "insight_forge":
		query := paramStr(params, "query")
		ctx := paramStr(params, "report_context")
		if ctx == "" {
			ctx = reportContext
		}
		return a.Tools.InsightForge(a.GraphID, query, a.SimulationRequirement, ctx, 5).ToText()
	case "panorama_search":
		query := paramStr(params, "query")
		includeExpired := paramBool(params, "include_expired", true)
		return a.Tools.PanoramaSearch(a.GraphID, query, includeExpired, 50).ToText()
	case "quick_search":
		query := paramStr(params, "query")
		limit := paramInt(params, "limit", 10)
		return a.Tools.QuickSearch(a.GraphID, query, limit).ToText()
	case "interview_agents":
		topic := paramStr(params, "interview_topic")
		if topic == "" {
			topic = paramStr(params, "query")
		}
		maxAgents := paramInt(params, "max_agents", 5)
		if maxAgents > 10 {
			maxAgents = 10
		}
		return a.Tools.InterviewAgents(a.SimulationID, topic, maxAgents)
	case "search_graph":
		return a.ExecuteTool("quick_search", params, reportContext)
	case "get_graph_statistics":
		b, _ := json.MarshalIndent(a.Tools.GraphStatistics(a.GraphID), "", "  ")
		return string(b)
	case "get_entity_summary":
		summary := a.Tools.EntitySummary(a.GraphID, paramStr(params, "entity_name"))
		if summary == nil {
			return "未找到该实体"
		}
		b, _ := json.MarshalIndent(summary, "", "  ")
		return string(b)
	case "get_simulation_context":
		query := paramStr(params, "query")
		if query == "" {
			query = a.SimulationRequirement
		}
		return a.ExecuteTool("insight_forge", map[string]any{"query": query}, reportContext)
	case "get_entities_by_type":
		b, _ := json.MarshalIndent(a.Tools.EntitiesByType(a.GraphID, paramStr(params, "entity_type")), "", "  ")
		return string(b)
	default:
		return "未知工具: " + name + "。请使用以下工具之一: insight_forge, panorama_search, quick_search"
	}
}

type toolCall struct {
	Name       string
	Parameters map[string]any
}

var reToolCall = regexp.MustCompile(`(?s)<tool_call>\s*(\{.*?\})\s*</tool_call>`)

var validToolNames = map[string]bool{
	"insight_forge": true, "panorama_search": true, "quick_search": true, "interview_agents": true,
}

// ParseToolCalls replica _parse_tool_calls: XML estándar y, como fallback,
// un objeto JSON suelto que sea una llamada válida.
func ParseToolCalls(response string) []toolCall {
	calls := []toolCall{}
	for _, match := range reToolCall.FindAllStringSubmatch(response, -1) {
		if call, ok := decodeToolCall([]byte(match[1])); ok {
			calls = append(calls, call)
		}
	}
	if len(calls) > 0 {
		return calls
	}

	stripped := strings.TrimSpace(response)
	if strings.HasPrefix(stripped, "{") && strings.HasSuffix(stripped, "}") {
		if call, ok := decodeToolCall([]byte(stripped)); ok {
			return []toolCall{call}
		}
	}
	return calls
}

func decodeToolCall(raw []byte) (toolCall, bool) {
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return toolCall{}, false
	}
	name := str(data["name"])
	if name == "" {
		name = str(data["tool"])
	}
	if !validToolNames[name] {
		return toolCall{}, false
	}
	params, _ := data["parameters"].(map[string]any)
	if params == nil {
		params, _ = data["params"].(map[string]any)
	}
	if params == nil {
		params = map[string]any{}
	}
	return toolCall{Name: name, Parameters: params}, true
}

var (
	reToolResultBlock = regexp.MustCompile(`(?is)<tool_result\b[^>]*>.*?</tool_result>`)
	reToolResultLoose = regexp.MustCompile(`(?is)<tool_result\b.*$`)
	reToolCallBlock   = regexp.MustCompile(`(?s)<tool_call>.*?</tool_call>`)
	reManyNewlines    = regexp.MustCompile(`\n{3,}`)
)

// StripFakeToolResults elimina bloques <tool_result> fabricados por el LLM.
func StripFakeToolResults(response string) string {
	cleaned := reToolResultBlock.ReplaceAllString(response, "")
	cleaned = reToolResultLoose.ReplaceAllString(cleaned, "")
	cleaned = reManyNewlines.ReplaceAllString(cleaned, "\n\n")
	return strings.TrimSpace(cleaned)
}

// ===== planificación =====

// PlanOutline replica plan_outline, con índice de respaldo si el LLM falla.
func (a *Agent) PlanOutline(cb ProgressCallback) *Outline {
	if cb != nil {
		cb("planning", 0, a.t("progress.analyzingRequirements", nil))
	}
	if a.console != nil {
		a.console.Info("获取模拟上下文: graph_id=%s", a.GraphID)
	}
	context := a.Tools.SimulationContext(a.GraphID, a.SimulationRequirement, 30)

	if cb != nil {
		cb("planning", 30, a.t("progress.generatingOutline", nil))
	}

	stats, _ := context["graph_statistics"].(map[string]any)
	entityTypes := []string{}
	if types, ok := stats["entity_types"].(map[string]int); ok {
		for name := range types {
			entityTypes = append(entityTypes, name)
		}
	}
	facts, _ := context["related_facts"].([]string)
	if len(facts) > 10 {
		facts = facts[:10]
	}
	factsJSON, _ := json.MarshalIndent(facts, "", "  ")
	systemPrompt := planSystemPrompt + "\n\n" + a.langInstruction()
	userPrompt := fmt.Sprintf(planUserPromptTemplate,
		a.SimulationRequirement,
		intFromMap(stats, "total_nodes"),
		intFromMap(stats, "total_edges"),
		entityTypes,
		intFromMap(context, "total_entities"),
		string(factsJSON),
	)

	client, err := a.llmClient()
	if err != nil {
		if a.console != nil {
			a.console.Warning("LLM no disponible para planificar: %v", err)
		}
		return defaultOutline()
	}
	response, err := client.ChatJSON([]llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, 0.3, nil, 2)
	if err != nil {
		if a.console != nil {
			a.console.Warning("规划大纲失败: %v", err)
		}
		return defaultOutline()
	}

	if cb != nil {
		cb("planning", 80, a.t("progress.parsingOutline", nil))
	}
	outline := &Outline{Title: str(response["title"]), Summary: str(response["summary"])}
	if outline.Title == "" {
		outline.Title = "模拟分析报告"
	}
	if list, ok := response["sections"].([]any); ok {
		for _, raw := range list {
			if m, ok := raw.(map[string]any); ok {
				title := strings.TrimSpace(str(m["title"]))
				if title != "" {
					outline.Sections = append(outline.Sections, Section{Title: title})
				}
			}
		}
	}
	if len(outline.Sections) == 0 {
		return defaultOutline()
	}
	if len(outline.Sections) > 5 {
		outline.Sections = outline.Sections[:5]
	}

	if cb != nil {
		cb("planning", 100, a.t("progress.outlinePlanComplete", nil))
	}
	if a.console != nil {
		a.console.Info("大纲规划完成: %d 个章节", len(outline.Sections))
	}
	return outline
}

func defaultOutline() *Outline {
	return &Outline{
		Title:   "未来预测报告",
		Summary: "基于模拟预测的未来趋势与风险分析",
		Sections: []Section{
			{Title: "预测场景与核心发现"},
			{Title: "人群行为预测分析"},
			{Title: "趋势展望与风险提示"},
		},
	}
}

// ===== sección (ReAct) =====

// GenerateSection replica _generate_section_react.
func (a *Agent) GenerateSection(section Section, outline *Outline, previousSections []string, cb ProgressCallback, sectionIndex int) string {
	sectionTitle := &section.Title
	idx := sectionIndex
	if a.logger != nil {
		a.logger.log("section_start", "generating", sectionTitle, &idx, map[string]any{"title": section.Title})
	}
	if a.console != nil {
		a.console.Info("开始生成章节 %d: %s", sectionIndex, section.Title)
	}

	systemPrompt := fmt.Sprintf(sectionSystemPromptTemplate,
		outline.Title, outline.Summary, a.SimulationRequirement, section.Title, a.toolsDescription())
	systemPrompt += "\n\n" + a.langInstruction()

	previousContent := "（这是第一个章节）"
	if len(previousSections) > 0 {
		parts := []string{}
		for _, sec := range previousSections {
			runes := []rune(sec)
			if len(runes) > 4000 {
				parts = append(parts, string(runes[:4000])+"...")
			} else {
				parts = append(parts, sec)
			}
		}
		previousContent = strings.Join(parts, "\n\n---\n\n")
	}
	userPrompt := fmt.Sprintf(sectionUserPromptTemplate, previousContent, section.Title, section.Title)

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	toolCallsCount := 0
	conflictRetries := 0
	usedTools := map[string]bool{}
	allTools := []string{"insight_forge", "panorama_search", "quick_search", "interview_agents"}
	reportContext := "章节标题: " + section.Title + "\n模拟需求: " + a.SimulationRequirement

	finalAnswer := ""
	completed := false

	for iteration := 0; iteration < maxSectionIterations && !completed; iteration++ {
		if cb != nil {
			cb("generating", iteration*100/maxSectionIterations,
				a.t("progress.deepSearchAndWrite", map[string]string{
					"current": fmt.Sprintf("%d", toolCallsCount),
					"max":     fmt.Sprintf("%d", maxToolCallsPerSection),
				}))
		}

		client, err := a.llmClient()
		if err != nil {
			if a.console != nil {
				a.console.Error("LLM no disponible: %v", err)
			}
			break
		}
		tokenLimit := maxSectionTokens
		response, err := client.Chat(messages, 0.5, &tokenLimit, nil)
		if err != nil {
			if a.console != nil {
				a.console.Warning("LLM 调用失败: %v", err)
			}
			if iteration < maxSectionIterations-1 {
				messages = append(messages,
					llm.Message{Role: "assistant", Content: "（响应为空）"},
					llm.Message{Role: "user", Content: "请继续生成内容。"})
				continue
			}
			break
		}

		calls := ParseToolCalls(response)
		hasToolCalls := len(calls) > 0
		hasFinalAnswer := strings.Contains(response, "Final Answer:")

		// El LLM no puede devolver herramienta y respuesta final a la vez.
		if hasToolCalls && hasFinalAnswer {
			conflictRetries++
			if a.console != nil {
				a.console.Warning("章节 %s: conflicto herramienta/Final Answer (#%d)", section.Title, conflictRetries)
			}
			if conflictRetries <= 2 {
				messages = append(messages,
					llm.Message{Role: "assistant", Content: StripFakeToolResults(response)},
					llm.Message{Role: "user", Content: "【格式错误】你在一次回复中同时包含了工具调用和 Final Answer，这是不允许的。每次回复只能做以下两件事之一：调用一个工具，或输出以 'Final Answer:' 开头的最终内容。请重新回复，只做其中一件事。"})
				continue
			}
			if end := strings.Index(response, "</tool_call>"); end != -1 {
				response = response[:end+len("</tool_call>")]
				calls = ParseToolCalls(response)
				hasToolCalls = len(calls) > 0
				hasFinalAnswer = false
				conflictRetries = 0
			}
		}

		if a.logger != nil {
			it := iteration + 1
			a.logger.log("llm_response", "generating", sectionTitle, &idx, map[string]any{
				"iteration":        it,
				"response":         truncateRunes(response, 2000),
				"has_tool_calls":   hasToolCalls,
				"has_final_answer": hasFinalAnswer,
			})
		}

		if hasFinalAnswer {
			cleaned := StripFakeToolResults(response)
			if toolCallsCount < minToolCalls {
				hint := unusedToolsHint(allTools, usedTools)
				messages = append(messages,
					llm.Message{Role: "assistant", Content: cleaned},
					llm.Message{Role: "user", Content: fmt.Sprintf(reactInsufficientToolsMsg, toolCallsCount, minToolCalls, hint)})
				continue
			}
			finalAnswer = strings.TrimSpace(lastAfter(cleaned, "Final Answer:"))
			if a.logger != nil {
				a.logger.log("section_content", "generating", sectionTitle, &idx, map[string]any{
					"tool_calls_count": toolCallsCount,
					"content":          finalAnswer,
				})
			}
			completed = true
			break
		}

		if hasToolCalls {
			if toolCallsCount >= maxToolCallsPerSection {
				if smartSectionComplete(response, toolCallsCount) {
					finalAnswer = StripFakeToolResults(response)
					completed = true
					break
				}
				messages = append(messages,
					llm.Message{Role: "assistant", Content: StripFakeToolResults(response)},
					llm.Message{Role: "user", Content: fmt.Sprintf(reactToolLimitMsg, toolCallsCount, maxToolCallsPerSection)})
				continue
			}

			call := calls[0] // solo se ejecuta la primera llamada de la respuesta
			if a.logger != nil {
				a.logger.log("tool_call", "generating", sectionTitle, &idx, map[string]any{
					"tool_name":  call.Name,
					"parameters": call.Parameters,
					"iteration":  iteration + 1,
				})
			}
			result := a.ExecuteTool(call.Name, call.Parameters, reportContext)
			if a.logger != nil {
				a.logger.log("tool_result", "generating", sectionTitle, &idx, map[string]any{
					"tool_name": call.Name,
					"result":    truncateRunes(result, 2000),
				})
			}

			toolCallsCount++
			usedTools[call.Name] = true
			hint := ""
			if len(unusedTools(allTools, usedTools)) > 0 && toolCallsCount < maxToolCallsPerSection {
				hint = fmt.Sprintf(reactUnusedToolsHint, strings.Join(unusedTools(allTools, usedTools), "、"))
			}
			messages = append(messages,
				llm.Message{Role: "assistant", Content: StripFakeToolResults(response)},
				llm.Message{Role: "user", Content: fmt.Sprintf(reactObservationTemplate,
					call.Name, result, toolCallsCount, maxToolCallsPerSection,
					strings.Join(keys(usedTools), ", "), hint)})
			continue
		}

		// Sin herramienta y sin "Final Answer:".
		cleaned := StripFakeToolResults(response)
		messages = append(messages, llm.Message{Role: "assistant", Content: cleaned})
		if toolCallsCount < minToolCalls {
			hint := unusedToolsHint(allTools, usedTools)
			messages = append(messages, llm.Message{Role: "user", Content: fmt.Sprintf(
				reactInsufficientToolsMsg, toolCallsCount, minToolCalls, hint)})
			continue
		}
		finalAnswer = cleaned
		completed = true
		break
	}

	if !completed {
		messages = append(messages, llm.Message{Role: "user", Content: reactForceFinalMsg})
		client, err := a.llmClient()
		if err == nil {
			tokenLimit := maxSectionTokens
			if response, err := client.Chat(messages, 0.5, &tokenLimit, nil); err == nil {
				finalAnswer = strings.TrimSpace(lastAfter(response, "Final Answer:"))
			}
		}
		if strings.TrimSpace(finalAnswer) == "" {
			finalAnswer = a.t("report.sectionGenFailedContent", nil)
			if a.console != nil {
				a.console.Warning("章节 %s: no se pudo generar contenido", section.Title)
			}
		}
		if a.logger != nil {
			a.logger.log("section_content", "generating", sectionTitle, &idx, map[string]any{
				"tool_calls_count": toolCallsCount,
				"content":          finalAnswer,
				"forced":           true,
			})
		}
	}

	return finalAnswer
}

func smartSectionComplete(content string, toolCallsCount int) bool {
	if len([]rune(content)) > 100 {
		return true
	}
	return toolCallsCount >= softToolLimit
}

func unusedTools(all []string, used map[string]bool) []string {
	out := []string{}
	for _, name := range all {
		if !used[name] {
			out = append(out, name)
		}
	}
	return out
}

func unusedToolsHint(all []string, used map[string]bool) string {
	remaining := unusedTools(all, used)
	if len(remaining) == 0 {
		return ""
	}
	return fmt.Sprintf(reactUnusedToolsHint, strings.Join(remaining, ", "))
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func lastAfter(s, marker string) string {
	if idx := strings.LastIndex(s, marker); idx >= 0 {
		return s[idx+len(marker):]
	}
	return s
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// ===== generación completa =====

// GenerateReport replica generate_report: guarda cada capítulo en cuanto está
// listo y escribe progress.json para el polling de la UI.
func (a *Agent) GenerateReport(cb ProgressCallback, reportID string) *Report {
	if reportID == "" {
		reportID = NewReportID()
	}
	start := time.Now()
	report := &Report{
		ReportID:              reportID,
		SimulationID:          a.SimulationID,
		GraphID:               a.GraphID,
		SimulationRequirement: a.SimulationRequirement,
		Status:                StatusPending,
		CreatedAt:             timex.Now(),
	}
	if a.UserID != "" {
		uid := a.UserID
		report.UserID = &uid
	}
	completedTitles := []string{}

	fail := func(err error) *Report {
		report.Status = StatusFailed
		msg := err.Error()
		report.Error = &msg
		if a.logger != nil {
			a.logger.log("error", "failed", nil, nil, map[string]any{"error": msg})
		}
		if a.console != nil {
			a.console.Error("报告生成失败: %s", msg)
		}
		_ = a.Manager.SaveReport(report)
		_ = a.Manager.UpdateProgress(reportID, "failed", -1,
			a.t("progress.reportFailed", map[string]string{"error": msg}), nil, completedTitles)
		if a.console != nil {
			a.console.Close()
		}
		return report
	}

	if _, err := a.Manager.EnsureFolder(reportID); err != nil {
		return fail(err)
	}
	a.logger = newAgentLogger(a.Manager, reportID)
	a.console = newConsoleLogger(a.Manager, reportID)
	a.logger.log("start", "pending", nil, nil, map[string]any{
		"simulation_id":          a.SimulationID,
		"graph_id":               a.GraphID,
		"simulation_requirement": a.SimulationRequirement,
	})

	if err := a.Manager.UpdateProgress(reportID, "pending", 0, a.t("progress.initReport", nil), nil, nil); err != nil {
		return fail(err)
	}
	if err := a.Manager.SaveReport(report); err != nil {
		return fail(err)
	}

	// Fase 1: planificación.
	report.Status = StatusPlanning
	_ = a.Manager.UpdateProgress(reportID, "planning", 5, a.t("progress.startPlanningOutline", nil), nil, nil)
	a.logger.log("planning_start", "planning", nil, nil, nil)
	if cb != nil {
		cb("planning", 0, a.t("progress.startPlanningOutline", nil))
	}
	outline := a.PlanOutline(func(stage string, progress int, message string) {
		if cb != nil {
			cb(stage, progress/5, message)
		}
	})
	report.Outline = outline
	a.logger.log("planning_complete", "planning", nil, nil, map[string]any{"outline": outline.ToDict()})
	if err := a.Manager.SaveOutline(reportID, outline); err != nil {
		return fail(err)
	}
	_ = a.Manager.UpdateProgress(reportID, "planning", 15,
		a.t("progress.outlineDone", map[string]string{"count": fmt.Sprintf("%d", len(outline.Sections))}), nil, nil)
	_ = a.Manager.SaveReport(report)

	// Fase 2: capítulos.
	report.Status = StatusGenerating
	total := len(outline.Sections)
	generated := []string{}
	for i := range outline.Sections {
		section := &outline.Sections[i]
		sectionNum := i + 1
		baseProgress := 20
		if total > 0 {
			baseProgress += int(float64(i) / float64(total) * 70)
		}
		msg := a.t("progress.generatingSection", map[string]string{
			"title":   section.Title,
			"current": fmt.Sprintf("%d", sectionNum),
			"total":   fmt.Sprintf("%d", total),
		})
		_ = a.Manager.UpdateProgress(reportID, "generating", baseProgress, msg, &section.Title, completedTitles)
		if cb != nil {
			cb("generating", baseProgress, msg)
		}

		content := a.GenerateSection(*section, outline, generated,
			func(stage string, progress int, message string) {
				if cb != nil {
					next := baseProgress
					if total > 0 {
						next += int(float64(progress) * 0.7 / float64(total))
					}
					cb(stage, next, message)
				}
			}, sectionNum)
		section.Content = content
		generated = append(generated, "## "+section.Title+"\n\n"+content)

		if _, err := a.Manager.SaveSection(reportID, sectionNum, *section); err != nil {
			return fail(err)
		}
		completedTitles = append(completedTitles, section.Title)
		a.logger.log("section_complete", "generating", &section.Title, &sectionNum, map[string]any{
			"full_content": "## " + section.Title + "\n\n" + content,
		})
		if a.console != nil {
			a.console.Info("章节已保存: section_%02d.md (%s)", sectionNum, section.Title)
		}

		doneProgress := baseProgress
		if total > 0 {
			doneProgress += 70 / total
		}
		_ = a.Manager.UpdateProgress(reportID, "generating", doneProgress,
			a.t("progress.sectionDone", map[string]string{"title": section.Title}), nil, completedTitles)
	}

	// Fase 3: ensamblado.
	if cb != nil {
		cb("generating", 95, a.t("progress.assemblingReport", nil))
	}
	_ = a.Manager.UpdateProgress(reportID, "generating", 95, a.t("progress.assemblingReport", nil), nil, completedTitles)

	md, err := a.Manager.AssembleFullReport(reportID, outline)
	if err != nil {
		return fail(err)
	}
	report.MarkdownContent = md
	report.Status = StatusCompleted
	report.CompletedAt = timex.Now()

	a.logger.log("report_complete", "completed", nil, nil, map[string]any{
		"total_sections":     total,
		"total_time_seconds": time.Since(start).Seconds(),
	})
	if err := a.Manager.SaveReport(report); err != nil {
		return fail(err)
	}
	_ = a.Manager.UpdateProgress(reportID, "completed", 100, a.t("progress.reportComplete", nil), nil, completedTitles)
	if cb != nil {
		cb("completed", 100, a.t("progress.reportComplete", nil))
	}
	if a.console != nil {
		a.console.Info("报告生成完成: %s (%d 章节)", reportID, total)
		a.console.Close()
	}
	return report
}

// ===== chat =====

// Chat replica chat(): respuesta + tool_calls + sources.
func (a *Agent) Chat(message string, history []map[string]any) map[string]any {
	reportContent := ""
	if existing := a.Manager.GetReportBySimulation(a.SimulationID); existing != nil && existing.MarkdownContent != "" {
		runes := []rune(existing.MarkdownContent)
		if len(runes) > 15000 {
			reportContent = string(runes[:15000]) + "\n\n... [报告内容已截断] ..."
		} else {
			reportContent = existing.MarkdownContent
		}
	}
	if reportContent == "" {
		reportContent = "（暂无报告）"
	}

	systemPrompt := fmt.Sprintf(chatSystemPromptTemplate, a.SimulationRequirement, reportContent, a.toolsDescription())
	systemPrompt += "\n\n" + a.langInstruction()

	messages := []llm.Message{{Role: "system", Content: systemPrompt}}
	if len(history) > 10 {
		history = history[len(history)-10:]
	}
	for _, h := range history {
		role := str(h["role"])
		content := str(h["content"])
		if role != "" && content != "" {
			messages = append(messages, llm.Message{Role: role, Content: content})
		}
	}
	messages = append(messages, llm.Message{Role: "user", Content: message})

	toolCallsMade := []map[string]any{}
	sources := []string{}

	cleanResponse := func(text string) string {
		cleaned := reToolCallBlock.ReplaceAllString(text, "")
		return StripFakeToolResults(cleaned)
	}

	for iteration := 0; iteration < 2; iteration++ {
		client, err := a.llmClient()
		if err != nil {
			return map[string]any{"response": "LLM no disponible: " + err.Error(), "tool_calls": toolCallsMade, "sources": sources}
		}
		response, err := client.Chat(messages, 0.5, nil, nil)
		if err != nil {
			return map[string]any{"response": "Error al consultar el LLM: " + err.Error(), "tool_calls": toolCallsMade, "sources": sources}
		}

		calls := ParseToolCalls(response)
		if len(calls) == 0 {
			return map[string]any{
				"response":   strings.TrimSpace(cleanResponse(response)),
				"tool_calls": toolCallsMade,
				"sources":    sources,
			}
		}

		toolResults := []string{}
		for _, call := range calls[:1] {
			if len(toolCallsMade) >= maxToolCallsPerChat {
				break
			}
			result := a.ExecuteTool(call.Name, call.Parameters, "")
			toolResults = append(toolResults, "["+call.Name+"结果]\n"+truncateRunes(result, 1500))
			toolCallsMade = append(toolCallsMade, map[string]any{
				"name":       call.Name,
				"parameters": call.Parameters,
			})
			sources = append(sources, paramStr(call.Parameters, "query"))
		}

		messages = append(messages, llm.Message{Role: "assistant", Content: StripFakeToolResults(response)})
		messages = append(messages, llm.Message{Role: "user", Content: strings.Join(toolResults, "\n") + chatObservationSuffix})
	}

	client, err := a.llmClient()
	if err != nil {
		return map[string]any{"response": "LLM no disponible: " + err.Error(), "tool_calls": toolCallsMade, "sources": sources}
	}
	final, err := client.Chat(messages, 0.5, nil, nil)
	if err != nil {
		return map[string]any{"response": "Error al consultar el LLM: " + err.Error(), "tool_calls": toolCallsMade, "sources": sources}
	}
	return map[string]any{
		"response":   strings.TrimSpace(cleanResponse(final)),
		"tool_calls": toolCallsMade,
		"sources":    sources,
	}
}

// ===== helpers de parámetros =====

func paramStr(params map[string]any, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

func paramInt(params map[string]any, key string, def int) int {
	switch v := params[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

func paramBool(params map[string]any, key string, def bool) bool {
	switch v := params[key].(type) {
	case bool:
		return v
	case string:
		lower := strings.ToLower(strings.TrimSpace(v))
		return lower == "true" || lower == "1" || lower == "yes"
	}
	return def
}

func intFromMap(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}
