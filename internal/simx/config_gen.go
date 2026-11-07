// Port de backend/app/services/simulation_config_generator.py.
package simx

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"mirofish/internal/llm"
	"mirofish/internal/locale"
)

// AgentActivityConfig replica el dataclass.
type AgentActivityConfig struct {
	AgentID     int     `json:"agent_id"`
	EntityUUID  string  `json:"entity_uuid"`
	EntityName  string  `json:"entity_name"`
	EntityType  string  `json:"entity_type"`
	ActivityLevel float64 `json:"activity_level"`
	PostsPerHour  float64 `json:"posts_per_hour"`
	CommentsPerHour float64 `json:"comments_per_hour"`
	ActiveHours   []int   `json:"active_hours"`
	ResponseDelayMin int  `json:"response_delay_min"`
	ResponseDelayMax int  `json:"response_delay_max"`
	SentimentBias float64 `json:"sentiment_bias"`
	Stance        string  `json:"stance"`
	InfluenceWeight float64 `json:"influence_weight"`
}

// TimeSimulationConfig replica el dataclass.
type TimeSimulationConfig struct {
	TotalSimulationHours int     `json:"total_simulation_hours"`
	MinutesPerRound      int     `json:"minutes_per_round"`
	AgentsPerHourMin     int     `json:"agents_per_hour_min"`
	AgentsPerHourMax     int     `json:"agents_per_hour_max"`
	PeakHours            []int   `json:"peak_hours"`
	PeakActivityMultiplier float64 `json:"peak_activity_multiplier"`
	OffPeakHours         []int   `json:"off_peak_hours"`
	OffPeakActivityMultiplier float64 `json:"off_peak_activity_multiplier"`
	MorningHours         []int   `json:"morning_hours"`
	MorningActivityMultiplier float64 `json:"morning_activity_multiplier"`
	WorkHours            []int   `json:"work_hours"`
	WorkActivityMultiplier float64 `json:"work_activity_multiplier"`
}

// EventConfig replica el dataclass.
type EventConfig struct {
	InitialPosts      []map[string]any `json:"initial_posts"`
	ScheduledEvents   []map[string]any `json:"scheduled_events"`
	HotTopics         []string         `json:"hot_topics"`
	NarrativeDirection string          `json:"narrative_direction"`
}

// PlatformConfig replica el dataclass.
type PlatformConfig struct {
	Platform             string  `json:"platform"`
	RecencyWeight        float64 `json:"recency_weight"`
	PopularityWeight     float64 `json:"popularity_weight"`
	RelevanceWeight      float64 `json:"relevance_weight"`
	ViralThreshold       int     `json:"viral_threshold"`
	EchoChamberStrength  float64 `json:"echo_chamber_strength"`
}

// SimulationParameters replica el dataclass completo.
type SimulationParameters struct {
	SimulationID          string              `json:"simulation_id"`
	ProjectID             string              `json:"project_id"`
	GraphID               string              `json:"graph_id"`
	SimulationRequirement string              `json:"simulation_requirement"`
	TimeConfig            TimeSimulationConfig `json:"time_config"`
	AgentConfigs          []AgentActivityConfig `json:"agent_configs"`
	EventConfig           EventConfig         `json:"event_config"`
	TwitterConfig         *PlatformConfig     `json:"twitter_config"`
	RedditConfig          *PlatformConfig     `json:"reddit_config"`
	LLMModel              string              `json:"llm_model"`
	LLMBaseURL            string              `json:"llm_base_url"`
	GeneratedAt           string              `json:"generated_at"`
	GenerationReasoning   string              `json:"generation_reasoning"`
}

// ToDict replica to_dict.
func (p *SimulationParameters) ToDict() map[string]any {
	return map[string]any{
		"simulation_id":          p.SimulationID,
		"project_id":             p.ProjectID,
		"graph_id":               p.GraphID,
		"simulation_requirement": p.SimulationRequirement,
		"time_config":            p.TimeConfig,
		"agent_configs":          p.AgentConfigs,
		"event_config":           p.EventConfig,
		"twitter_config":         p.TwitterConfig,
		"reddit_config":          p.RedditConfig,
		"llm_model":              p.LLMModel,
		"llm_base_url":           p.LLMBaseURL,
		"generated_at":           p.GeneratedAt,
		"generation_reasoning":   p.GenerationReasoning,
	}
}

// ToJSON replica to_json (indent 2).
func (p *SimulationParameters) ToJSON() string {
	b, _ := json.MarshalIndent(p.ToDict(), "", "  ")
	return string(b)
}

// ConfigGenerator replica SimulationConfigGenerator.
type ConfigGenerator struct {
	Locale string

	// LLMFactory inyectable (tests).
	LLMFactory func() (*llm.Client, error)
}

// NewConfigGenerator crea el generador.
func NewConfigGenerator() *ConfigGenerator { return &ConfigGenerator{Locale: "zh"} }

const (
	maxContextLength        = 50000
	agentsPerBatch          = 15
	timeConfigContextLength = 10000
	eventConfigContextLength = 8000
	entitySummaryLength     = 300
	agentSummaryLength      = 300
	entitiesPerTypeDisplay  = 20
)

func (g *ConfigGenerator) llmClient() (*llm.Client, error) {
	if g.LLMFactory != nil {
		return g.LLMFactory()
	}
	return llm.NewDefaultClient()
}

func (g *ConfigGenerator) t(key string, args map[string]string) string {
	return locale.T(g.Locale, key, args)
}

func (g *ConfigGenerator) langInstruction() string {
	return locale.GetLanguageInstruction(g.Locale)
}

// GenerateConfig replica generate_config con progreso por pasos.
func (g *ConfigGenerator) GenerateConfig(
	simulationID, projectID, graphID, simulationRequirement, documentText string,
	entities []EntityNode,
	enableTwitter, enableReddit bool,
	progressCallback func(step, total int, message string),
) (*SimulationParameters, error) {
	numBatches := int(math.Ceil(float64(len(entities)) / float64(agentsPerBatch)))
	totalSteps := 3 + numBatches
	reportProgress := func(step int, message string) {
		if progressCallback != nil {
			progressCallback(step, totalSteps, message)
		}
		logger.Info(fmt.Sprintf("[%d/%d] %s", step, totalSteps, message))
	}

	context := g.buildContext(simulationRequirement, documentText, entities)
	reasoningParts := []string{}

	// 1. tiempo
	reportProgress(1, g.t("progress.generatingTimeConfig", nil))
	timeResult := g.generateTimeConfig(context, len(entities))
	timeConfig := g.parseTimeConfig(timeResult, len(entities))
	reasoning := strOr(timeResult["reasoning"])
	if reasoning == "" {
		reasoning = g.t("common.success", nil)
	}
	reasoningParts = append(reasoningParts, fmt.Sprintf("%s: %s", g.t("progress.timeConfigLabel", nil), reasoning))

	// 2. eventos
	reportProgress(2, g.t("progress.generatingEventConfig", nil))
	eventResult := g.generateEventConfig(context, simulationRequirement, entities)
	eventConfig := g.parseEventConfig(eventResult)
	reasoning = strOr(eventResult["reasoning"])
	if reasoning == "" {
		reasoning = g.t("common.success", nil)
	}
	reasoningParts = append(reasoningParts, fmt.Sprintf("%s: %s", g.t("progress.eventConfigLabel", nil), reasoning))

	// 3-N. agentes por lotes
	allAgentConfigs := []AgentActivityConfig{}
	for batchIdx := 0; batchIdx < numBatches; batchIdx++ {
		startIdx := batchIdx * agentsPerBatch
		endIdx := startIdx + agentsPerBatch
		if endIdx > len(entities) {
			endIdx = len(entities)
		}
		reportProgress(3+batchIdx, g.t("progress.generatingAgentConfig", map[string]string{
			"start": fmt.Sprintf("%d", startIdx+1),
			"end":   fmt.Sprintf("%d", endIdx),
			"total": fmt.Sprintf("%d", len(entities)),
		}))
		allAgentConfigs = append(allAgentConfigs, g.generateAgentConfigsBatch(context, entities[startIdx:endIdx], startIdx, simulationRequirement)...)
	}
	reasoningParts = append(reasoningParts, g.t("progress.agentConfigResult", map[string]string{"count": fmt.Sprintf("%d", len(allAgentConfigs))}))

	// asignar posters
	eventConfig = g.assignInitialPostAgents(eventConfig, allAgentConfigs)
	assigned := 0
	for _, post := range eventConfig.InitialPosts {
		if v, ok := post["poster_agent_id"]; ok && v != nil {
			assigned++
		}
	}
	reasoningParts = append(reasoningParts, g.t("progress.postAssignResult", map[string]string{"count": fmt.Sprintf("%d", assigned)}))

	// plataforma
	reportProgress(totalSteps, g.t("progress.generatingPlatformConfig", nil))
	var twitterConfig, redditConfig *PlatformConfig
	if enableTwitter {
		twitterConfig = &PlatformConfig{Platform: "twitter", RecencyWeight: 0.4, PopularityWeight: 0.3, RelevanceWeight: 0.3, ViralThreshold: 10, EchoChamberStrength: 0.5}
	}
	if enableReddit {
		redditConfig = &PlatformConfig{Platform: "reddit", RecencyWeight: 0.3, PopularityWeight: 0.4, RelevanceWeight: 0.3, ViralThreshold: 15, EchoChamberStrength: 0.6}
	}

	params := &SimulationParameters{
		SimulationID:          simulationID,
		ProjectID:             projectID,
		GraphID:               graphID,
		SimulationRequirement: simulationRequirement,
		TimeConfig:            timeConfig,
		AgentConfigs:          allAgentConfigs,
		EventConfig:           eventConfig,
		TwitterConfig:         twitterConfig,
		RedditConfig:          redditConfig,
		LLMModel:              "",
		LLMBaseURL:            "",
		GeneratedAt:           time.Now().Format("2006-01-02T15:04:05.999999"),
		GenerationReasoning:   strings.Join(reasoningParts, " | "),
	}
	logger.Info(fmt.Sprintf("模拟配置生成完成: %d 个Agent配置", len(params.AgentConfigs)))
	return params, nil
}

func (g *ConfigGenerator) buildContext(simulationRequirement, documentText string, entities []EntityNode) string {
	entitySummary := g.summarizeEntities(entities)
	parts := []string{
		fmt.Sprintf("## 模拟需求\n%s", simulationRequirement),
		fmt.Sprintf("\n## 实体信息 (%d个)\n%s", len(entities), entitySummary),
	}
	currentLength := 0
	for _, p := range parts {
		currentLength += len([]rune(p))
	}
	remainingLength := maxContextLength - currentLength - 500
	if remainingLength > 0 && documentText != "" {
		docText := documentText
		if len([]rune(docText)) > remainingLength {
			docText = string([]rune(docText)[:remainingLength]) + "\n...(文档已截断)"
		}
		parts = append(parts, fmt.Sprintf("\n## 原始文档内容\n%s", docText))
	}
	return strings.Join(parts, "\n")
}

func (g *ConfigGenerator) summarizeEntities(entities []EntityNode) string {
	byType := map[string][]EntityNode{}
	order := []string{}
	for _, e := range entities {
		t := "Unknown"
		if et := e.GetEntityType(); et != nil {
			t = *et
		}
		if _, ok := byType[t]; !ok {
			order = append(order, t)
		}
		byType[t] = append(byType[t], e)
	}
	lines := []string{}
	for _, entityType := range order {
		typeEntities := byType[entityType]
		lines = append(lines, fmt.Sprintf("\n### %s (%d个)", entityType, len(typeEntities)))
		displayCount := entitiesPerTypeDisplay
		for i, e := range typeEntities {
			if i >= displayCount {
				break
			}
			summaryPreview := e.Summary
			if len([]rune(summaryPreview)) > entitySummaryLength {
				summaryPreview = string([]rune(summaryPreview)[:entitySummaryLength]) + "..."
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", e.Name, summaryPreview))
		}
		if len(typeEntities) > displayCount {
			lines = append(lines, fmt.Sprintf("  ... 还有 %d 个", len(typeEntities)-displayCount))
		}
	}
	return strings.Join(lines, "\n")
}

func (g *ConfigGenerator) callLLMWithRetry(prompt, systemPrompt string) (map[string]any, error) {
	maxAttempts := 3
	var lastError error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		client, err := g.llmClient()
		if err != nil {
			lastError = err
			time.Sleep(time.Duration(2*(attempt+1)) * time.Second)
			continue
		}
		temp := 0.7 - float64(attempt)*0.1
		responseFormat := llm.ResponseFormat{Type: "json_object"}
		resp, err := client.CreateCompletion([]llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		}, &temp, nil, &responseFormat)
		if err != nil {
			lastError = err
			time.Sleep(time.Duration(2*(attempt+1)) * time.Second)
			continue
		}
		content := llm.ExtractChatCompletionText(resp)
		if len(resp.Choices) > 0 && resp.Choices[0].FinishReason == "length" {
			logger.Warn(fmt.Sprintf("LLM输出被截断 (attempt %d)", attempt+1))
			content = fixTruncatedJSON(content)
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(content), &result); err == nil {
			return result, nil
		} else {
			logger.Warn(fmt.Sprintf("JSON解析失败 (attempt %d): %v", attempt+1, truncateErr(err)))
			if fixed := g.tryFixConfigJSON(content); fixed != nil {
				return fixed, nil
			}
			lastError = err
		}
	}
	if lastError == nil {
		lastError = fmt.Errorf("LLM调用失败")
	}
	return nil, lastError
}

func truncateErr(err error) string {
	s := err.Error()
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

var reConfigJSONBlock = regexp.MustCompile(`(?s)\{.*\}`)
var reConfigJSONString = regexp.MustCompile(`"[^"\\]*(?:\\.[^"\\]*)*"`)
var reConfigControl = regexp.MustCompile("[\\x00-\\x1f\\x{7f}-\\x{9f}]")
var reConfigWhitespace = regexp.MustCompile(`\s+`)

func (g *ConfigGenerator) tryFixConfigJSON(content string) map[string]any {
	content = fixTruncatedJSON(content)
	if match := reConfigJSONBlock.FindString(content); match != "" {
		jsonStr := match
		jsonStr = reConfigJSONString.ReplaceAllStringFunc(jsonStr, func(m string) string {
			s := strings.ReplaceAll(m, "\n", " ")
			s = strings.ReplaceAll(s, "\r", " ")
			return reConfigWhitespace.ReplaceAllString(s, " ")
		})
		var result map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &result); err == nil {
			return result
		}
		jsonStr = reConfigControl.ReplaceAllString(jsonStr, " ")
		jsonStr = reConfigWhitespace.ReplaceAllString(jsonStr, " ")
		if err := json.Unmarshal([]byte(jsonStr), &result); err == nil {
			return result
		}
	}
	return nil
}

func (g *ConfigGenerator) generateTimeConfig(context string, numEntities int) map[string]any {
	contextTruncated := truncateRunes(context, timeConfigContextLength)
	maxAgentsAllowed := maxInt(1, int(float64(numEntities)*0.9))

	prompt := fmt.Sprintf(`基于以下模拟需求，生成时间模拟配置。

%s

## 任务
请生成时间配置JSON。

### 基本原则（仅供参考，需根据具体事件和参与群体灵活调整）：
- 请根据模拟场景推断目标用户群体所在时区和作息习惯，以下为东八区(UTC+8)的参考示例
- 凌晨0-5点几乎无人活动（活跃度系数0.05）
- 早上6-8点逐渐活跃（活跃度系数0.4）
- 工作时间9-18点中等活跃（活跃度系数0.7）
- 晚间19-22点是高峰期（活跃度系数1.5）
- 23点后活跃度下降（活跃度系数0.5）
- 一般规律：凌晨低活跃、早间渐增、工作时段中等、晚间高峰
- **重要**：以下示例值仅供参考，你需要根据事件性质、参与群体特点来调整具体时段
  - 例如：学生群体高峰可能是21-23点；媒体全天活跃；官方机构只在工作时间
  - 例如：突发热点可能导致深夜也有讨论，off_peak_hours 可适当缩短

### 返回JSON格式（不要markdown）

示例：
{
    "total_simulation_hours": 72,
    "minutes_per_round": 60,
    "agents_per_hour_min": 5,
    "agents_per_hour_max": 50,
    "peak_hours": [19, 20, 21, 22],
    "off_peak_hours": [0, 1, 2, 3, 4, 5],
    "morning_hours": [6, 7, 8],
    "work_hours": [9, 10, 11, 12, 13, 14, 15, 16, 17, 18],
    "reasoning": "针对该事件的时间配置说明"
}

字段说明：
- total_simulation_hours (int): 模拟总时长，24-168小时，突发事件短、持续话题长
- minutes_per_round (int): 每轮时长，30-120分钟，建议60分钟
- agents_per_hour_min (int): 每小时最少激活Agent数（取值范围: 1-%d）
- agents_per_hour_max (int): 每小时最多激活Agent数（取值范围: 1-%d）
- peak_hours (int数组): 高峰时段，根据事件参与群体调整
- off_peak_hours (int数组): 低谷时段，通常深夜凌晨
- morning_hours (int数组): 早间时段
- work_hours (int数组): 工作时段
- reasoning (string): 简要说明为什么这样配置`, contextTruncated, maxAgentsAllowed, maxAgentsAllowed)

	systemPrompt := "你是社交媒体模拟专家。返回纯JSON格式，时间配置需符合模拟场景中目标用户群体的作息习惯。"
	systemPrompt = systemPrompt + "\n\n" + g.langInstruction()

	result, err := g.callLLMWithRetry(prompt, systemPrompt)
	if err != nil {
		logger.Warn(fmt.Sprintf("时间配置LLM生成失败: %v, 使用默认配置", err))
		return g.defaultTimeConfig(numEntities)
	}
	return result
}

func (g *ConfigGenerator) defaultTimeConfig(numEntities int) map[string]any {
	return map[string]any{
		"total_simulation_hours": 72,
		"minutes_per_round":      60,
		"agents_per_hour_min":    maxInt(1, numEntities/15),
		"agents_per_hour_max":    maxInt(5, numEntities/5),
		"peak_hours":             []int{19, 20, 21, 22},
		"off_peak_hours":         []int{0, 1, 2, 3, 4, 5},
		"morning_hours":          []int{6, 7, 8},
		"work_hours":             intRange(9, 19),
		"reasoning":              "使用默认中国人作息配置（每轮1小时）",
	}
}

func (g *ConfigGenerator) parseTimeConfig(result map[string]any, numEntities int) TimeSimulationConfig {
	agentsPerHourMin := intFromAny(result["agents_per_hour_min"], maxInt(1, numEntities/15))
	agentsPerHourMax := intFromAny(result["agents_per_hour_max"], maxInt(5, numEntities/5))

	if agentsPerHourMin > numEntities {
		logger.Warn(fmt.Sprintf("agents_per_hour_min (%d) 超过总Agent数 (%d)，已修正", agentsPerHourMin, numEntities))
		agentsPerHourMin = maxInt(1, numEntities/10)
	}
	if agentsPerHourMax > numEntities {
		logger.Warn(fmt.Sprintf("agents_per_hour_max (%d) 超过总Agent数 (%d)，已修正", agentsPerHourMax, numEntities))
		agentsPerHourMax = maxInt(agentsPerHourMin+1, numEntities/2)
	}
	if agentsPerHourMin >= agentsPerHourMax {
		agentsPerHourMin = maxInt(1, agentsPerHourMax/2)
		logger.Warn(fmt.Sprintf("agents_per_hour_min >= max，已修正为 %d", agentsPerHourMin))
	}

	return TimeSimulationConfig{
		TotalSimulationHours:  intFromAny(result["total_simulation_hours"], 72),
		MinutesPerRound:       intFromAny(result["minutes_per_round"], 60),
		AgentsPerHourMin:      agentsPerHourMin,
		AgentsPerHourMax:      agentsPerHourMax,
		PeakHours:             intList(result["peak_hours"], []int{19, 20, 21, 22}),
		OffPeakHours:          intList(result["off_peak_hours"], []int{0, 1, 2, 3, 4, 5}),
		OffPeakActivityMultiplier: 0.05,
		MorningHours:          intList(result["morning_hours"], []int{6, 7, 8}),
		MorningActivityMultiplier: 0.4,
		WorkHours:             intList(result["work_hours"], intRange(9, 19)),
		WorkActivityMultiplier: 0.7,
		PeakActivityMultiplier: 1.5,
	}
}

func (g *ConfigGenerator) generateEventConfig(context, simulationRequirement string, entities []EntityNode) map[string]any {
	entityTypesAvailable := []string{}
	seen := map[string]bool{}
	typeExamples := map[string][]string{}
	for _, e := range entities {
		t := "Unknown"
		if et := e.GetEntityType(); et != nil {
			t = *et
		}
		if !seen[t] {
			seen[t] = true
			entityTypesAvailable = append(entityTypesAvailable, t)
		}
		if len(typeExamples[t]) < 3 {
			typeExamples[t] = append(typeExamples[t], e.Name)
		}
	}
	lines := []string{}
	for _, t := range entityTypesAvailable {
		lines = append(lines, fmt.Sprintf("- %s: %s", t, strings.Join(typeExamples[t], ", ")))
	}
	typeInfo := strings.Join(lines, "\n")
	contextTruncated := truncateRunes(context, eventConfigContextLength)

	prompt := fmt.Sprintf(`基于以下模拟需求，生成事件配置。

模拟需求: %s

%s

## 可用实体类型及示例
%s

## 任务
请生成事件配置JSON：
- 提取热点话题关键词
- 描述舆论发展方向
- 设计初始帖子内容，**每个帖子必须指定 poster_type（发布者类型）**

**重要**: poster_type 必须从上面的"可用实体类型"中选择，这样初始帖子才能分配给合适的 Agent 发布。
例如：官方声明应由 Official/University 类型发布，新闻由 MediaOutlet 发布，学生观点由 Student 发布。

返回JSON格式（不要markdown）：
{
    "hot_topics": ["关键词1", "关键词2", ...],
    "narrative_direction": "<舆论发展方向描述>",
    "initial_posts": [
        {"content": "帖子内容", "poster_type": "实体类型（必须从可用类型中选择）"},
        ...
    ],
    "reasoning": "<简要说明>"
}`, simulationRequirement, contextTruncated, typeInfo)

	systemPrompt := "你是舆论分析专家。返回纯JSON格式。注意 poster_type 必须精确匹配可用实体类型。"
	systemPrompt = systemPrompt + "\n\n" + g.langInstruction() + "\nIMPORTANT: The 'poster_type' field value MUST be in English PascalCase exactly matching the available entity types. Only 'content', 'narrative_direction', 'hot_topics' and 'reasoning' fields should use the specified language."

	result, err := g.callLLMWithRetry(prompt, systemPrompt)
	if err != nil {
		logger.Warn(fmt.Sprintf("事件配置LLM生成失败: %v, 使用默认配置", err))
		return map[string]any{
			"hot_topics":          []string{},
			"narrative_direction": "",
			"initial_posts":       []map[string]any{},
			"reasoning":           "使用默认配置",
		}
	}
	return result
}

func (g *ConfigGenerator) parseEventConfig(result map[string]any) EventConfig {
	return EventConfig{
		InitialPosts:       anyMapList(result["initial_posts"]),
		ScheduledEvents:    []map[string]any{},
		HotTopics:          stringList(result["hot_topics"]),
		NarrativeDirection: strOr(result["narrative_direction"]),
	}
}

var typeAliases = map[string][]string{
	"official":      {"official", "university", "governmentagency", "government"},
	"university":    {"university", "official"},
	"mediaoutlet":   {"mediaoutlet", "media"},
	"student":       {"student", "person"},
	"professor":     {"professor", "expert", "teacher"},
	"alumni":        {"alumni", "person"},
	"organization":  {"organization", "ngo", "company", "group"},
	"person":        {"person", "student", "alumni"},
}

func (g *ConfigGenerator) assignInitialPostAgents(eventConfig EventConfig, agentConfigs []AgentActivityConfig) EventConfig {
	if len(eventConfig.InitialPosts) == 0 {
		return eventConfig
	}
	agentsByType := map[string][]AgentActivityConfig{}
	for _, agent := range agentConfigs {
		lt := strings.ToLower(agent.EntityType)
		agentsByType[lt] = append(agentsByType[lt], agent)
	}
	usedIndices := map[string]int{}
	aliasOrder := []string{"official", "university", "mediaoutlet", "student", "professor", "alumni", "organization", "person"}

	updatedPosts := []map[string]any{}
	for _, post := range eventConfig.InitialPosts {
		posterType := strings.ToLower(strOr(post["poster_type"]))
		content := strOr(post["content"])
		matchedAgentID := 0
		found := false

		if agents, ok := agentsByType[posterType]; ok {
			idx := usedIndices[posterType] % len(agents)
			matchedAgentID = agents[idx].AgentID
			usedIndices[posterType] = idx + 1
			found = true
		} else {
			for _, aliasKey := range aliasOrder {
				aliases := typeAliases[aliasKey]
				if posterType == aliasKey || containsStr(aliases, posterType) {
					for _, alias := range aliases {
						if agents, ok := agentsByType[alias]; ok {
							idx := usedIndices[alias] % len(agents)
							matchedAgentID = agents[idx].AgentID
							usedIndices[alias] = idx + 1
							found = true
							break
						}
					}
				}
				if found {
					break
				}
			}
		}

		if !found {
			logger.Warn(fmt.Sprintf("未找到类型 '%s' 的匹配 Agent，使用影响力最高的 Agent", posterType))
			if len(agentConfigs) > 0 {
				sorted := make([]AgentActivityConfig, len(agentConfigs))
				copy(sorted, agentConfigs)
				sort.Slice(sorted, func(i, j int) bool { return sorted[i].InfluenceWeight > sorted[j].InfluenceWeight })
				matchedAgentID = sorted[0].AgentID
			} else {
				matchedAgentID = 0
			}
		}

		updatedPosts = append(updatedPosts, map[string]any{
			"content":        content,
			"poster_type":    strOr(post["poster_type"]),
			"poster_agent_id": matchedAgentID,
		})
		logger.Info(fmt.Sprintf("初始帖子分配: poster_type='%s' -> agent_id=%d", posterType, matchedAgentID))
	}
	eventConfig.InitialPosts = updatedPosts
	return eventConfig
}

func (g *ConfigGenerator) generateAgentConfigsBatch(context string, entities []EntityNode, startIdx int, simulationRequirement string) []AgentActivityConfig {
	entityList := []map[string]any{}
	for i, e := range entities {
		summary := e.Summary
		if len([]rune(summary)) > agentSummaryLength {
			summary = string([]rune(summary)[:agentSummaryLength])
		}
		entityType := "Unknown"
		if et := e.GetEntityType(); et != nil {
			entityType = *et
		}
		entityList = append(entityList, map[string]any{
			"agent_id":    startIdx + i,
			"entity_name": e.Name,
			"entity_type": entityType,
			"summary":     summary,
		})
	}
	entityJSON, _ := json.MarshalIndent(entityList, "", "  ")

	prompt := fmt.Sprintf(`基于以下信息，为每个实体生成社交媒体活动配置。

模拟需求: %s

## 实体列表
`+"```json"+`
%s
`+"```"+`

## 任务
为每个实体生成活动配置，注意：
- **时间符合目标用户群体作息**：以下为参考（东八区），请根据模拟场景调整
- **官方机构**（University/GovernmentAgency）：活跃度低(0.1-0.3)，工作时间(9-17)活动，响应慢(60-240分钟)，影响力高(2.5-3.0)
- **媒体**（MediaOutlet）：活跃度中(0.4-0.6)，全天活动(8-23)，响应快(5-30分钟)，影响力高(2.0-2.5)
- **个人**（Student/Person/Alumni）：活跃度高(0.6-0.9)，主要晚间活动(18-23)，响应快(1-15分钟)，影响力低(0.8-1.2)
- **公众人物/专家**：活跃度中(0.4-0.6)，影响力中高(1.5-2.0)

返回JSON格式（不要markdown）：
{
    "agent_configs": [
        {
            "agent_id": <必须与输入一致>,
            "activity_level": <0.0-1.0>,
            "posts_per_hour": <发帖频率>,
            "comments_per_hour": <评论频率>,
            "active_hours": [<活跃小时列表，考虑中国人作息>],
            "response_delay_min": <最小响应延迟分钟>,
            "response_delay_max": <最大响应延迟分钟>,
            "sentiment_bias": <-1.0到1.0>,
            "stance": "<supportive/opposing/neutral/observer>",
            "influence_weight": <影响力权重>
        },
        ...
    ]
}`, simulationRequirement, string(entityJSON))

	systemPrompt := "你是社交媒体行为分析专家。返回纯JSON，配置需符合模拟场景中目标用户群体的作息习惯。"
	systemPrompt = systemPrompt + "\n\n" + g.langInstruction() + "\nIMPORTANT: The 'stance' field value MUST be one of the English strings: 'supportive', 'opposing', 'neutral', 'observer'. All JSON field names and numeric values must remain unchanged. Only natural language text fields should use the specified language."

	llmConfigs := map[int]map[string]any{}
	if result, err := g.callLLMWithRetry(prompt, systemPrompt); err != nil {
		logger.Warn(fmt.Sprintf("Agent配置批次LLM生成失败: %v, 使用规则生成", err))
	} else if list, ok := result["agent_configs"].([]any); ok {
		for _, raw := range list {
			cfg, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id := intFromAny(cfg["agent_id"], -1)
			llmConfigs[id] = cfg
		}
	}

	configs := []AgentActivityConfig{}
	for i, entity := range entities {
		agentID := startIdx + i
		cfg, ok := llmConfigs[agentID]
		if !ok {
			cfg = g.generateAgentConfigByRule(entity)
		}
		entityType := "Unknown"
		if et := entity.GetEntityType(); et != nil {
			entityType = *et
		}
		configs = append(configs, AgentActivityConfig{
			AgentID:          agentID,
			EntityUUID:       entity.UUID,
			EntityName:       entity.Name,
			EntityType:       entityType,
			ActivityLevel:    floatFromAny(cfg["activity_level"], 0.5),
			PostsPerHour:     floatFromAny(cfg["posts_per_hour"], 0.5),
			CommentsPerHour:  floatFromAny(cfg["comments_per_hour"], 1.0),
			ActiveHours:      intList(cfg["active_hours"], intRange(9, 23)),
			ResponseDelayMin: intFromAny(cfg["response_delay_min"], 5),
			ResponseDelayMax: intFromAny(cfg["response_delay_max"], 60),
			SentimentBias:    floatFromAny(cfg["sentiment_bias"], 0.0),
			Stance:           defaultStr(strOr(cfg["stance"]), "neutral"),
			InfluenceWeight:  floatFromAny(cfg["influence_weight"], 1.0),
		})
	}
	return configs
}

func (g *ConfigGenerator) generateAgentConfigByRule(entity EntityNode) map[string]any {
	entityType := strings.ToLower("Unknown")
	if et := entity.GetEntityType(); et != nil {
		entityType = strings.ToLower(*et)
	}
	switch entityType {
	case "university", "governmentagency", "ngo":
		return map[string]any{
			"activity_level": 0.2, "posts_per_hour": 0.1, "comments_per_hour": 0.05,
			"active_hours": intRange(9, 18), "response_delay_min": 60, "response_delay_max": 240,
			"sentiment_bias": 0.0, "stance": "neutral", "influence_weight": 3.0,
		}
	case "mediaoutlet":
		return map[string]any{
			"activity_level": 0.5, "posts_per_hour": 0.8, "comments_per_hour": 0.3,
			"active_hours": intRange(7, 24), "response_delay_min": 5, "response_delay_max": 30,
			"sentiment_bias": 0.0, "stance": "observer", "influence_weight": 2.5,
		}
	case "professor", "expert", "official":
		return map[string]any{
			"activity_level": 0.4, "posts_per_hour": 0.3, "comments_per_hour": 0.5,
			"active_hours": intRange(8, 22), "response_delay_min": 15, "response_delay_max": 90,
			"sentiment_bias": 0.0, "stance": "neutral", "influence_weight": 2.0,
		}
	case "student":
		return map[string]any{
			"activity_level": 0.8, "posts_per_hour": 0.6, "comments_per_hour": 1.5,
			"active_hours": []int{8, 9, 10, 11, 12, 13, 18, 19, 20, 21, 22, 23},
			"response_delay_min": 1, "response_delay_max": 15,
			"sentiment_bias": 0.0, "stance": "neutral", "influence_weight": 0.8,
		}
	case "alumni":
		return map[string]any{
			"activity_level": 0.6, "posts_per_hour": 0.4, "comments_per_hour": 0.8,
			"active_hours": []int{12, 13, 19, 20, 21, 22, 23},
			"response_delay_min": 5, "response_delay_max": 30,
			"sentiment_bias": 0.0, "stance": "neutral", "influence_weight": 1.0,
		}
	default:
		return map[string]any{
			"activity_level": 0.7, "posts_per_hour": 0.5, "comments_per_hour": 1.2,
			"active_hours": []int{9, 10, 11, 12, 13, 18, 19, 20, 21, 22, 23},
			"response_delay_min": 2, "response_delay_max": 20,
			"sentiment_bias": 0.0, "stance": "neutral", "influence_weight": 1.0,
		}
	}
}

// ===== helpers =====

func intRange(start, end int) []int {
	out := []int{}
	for i := start; i < end; i++ {
		out = append(out, i)
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func floatFromAny(v any, def float64) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return def
}

func intList(v any, def []int) []int {
	list, ok := v.([]any)
	if !ok {
		if l, ok := v.([]int); ok {
			return l
		}
		return def
	}
	out := []int{}
	for _, item := range list {
		out = append(out, intFromAny(item, 0))
	}
	if len(out) == 0 {
		return def
	}
	return out
}

func anyMapList(v any) []map[string]any {
	list, ok := v.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := []map[string]any{}
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func containsStr(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
