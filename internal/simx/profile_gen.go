// Port de backend/app/services/oasis_profile_generator.py.
package simx

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"mirofish/internal/graph"
	"mirofish/internal/llm"
	"mirofish/internal/locale"
)

// MBTI_TYPES replica la lista de tipos MBTI.
var MBTI_TYPES = []string{
	"INTJ", "INTP", "ENTJ", "ENTP",
	"INFJ", "INFP", "ENFJ", "ENFP",
	"ISTJ", "ISFJ", "ESTJ", "ESFJ",
	"ISTP", "ISFP", "ESTP", "ESFP",
}

const (
	defaultProfileGender = "other"
	defaultProfileAge    = 30
	defaultProfileCountry = "Venezuela"
)

var profileCountries = []string{
	"China", "US", "UK", "Japan", "Germany", "France",
	"Canada", "Australia", "Brazil", "India", "South Korea",
}

var individualEntityTypes = []string{
	"student", "alumni", "professor", "person", "publicfigure",
	"expert", "faculty", "official", "journalist", "activist",
}

var groupEntityTypes = []string{
	"university", "governmentagency", "organization", "ngo",
	"mediaoutlet", "company", "institution", "group", "community",
}

// OasisAgentProfile replica el dataclass OasisAgentProfile.
type OasisAgentProfile struct {
	UserID   int    `json:"user_id"`
	UserName string `json:"user_name"`
	Name     string `json:"name"`
	Bio      string `json:"bio"`
	Persona  string `json:"persona"`

	Karma         int `json:"karma"`
	FriendCount   int `json:"friend_count"`
	FollowerCount int `json:"follower_count"`
	StatusesCount int `json:"statuses_count"`

	Age             *int     `json:"age"`
	Gender          *string  `json:"gender"`
	Mbti            *string  `json:"mbti"`
	Country         *string  `json:"country"`
	Profession      *string  `json:"profession"`
	InterestedTopics []string `json:"interested_topics"`

	SourceEntityUUID *string `json:"source_entity_uuid"`
	SourceEntityType *string `json:"source_entity_type"`

	CreatedAt string `json:"created_at"`
}

// NewOasisAgentProfile aplica la normalización de __post_init__.
func NewOasisAgentProfile(userID int, userName, name, bio, persona string, karma, friendCount, followerCount, statusesCount int) *OasisAgentProfile {
	p := &OasisAgentProfile{
		UserID:         userID,
		UserName:       userName,
		Name:           name,
		Bio:            coerceStr(any(bio)),
		Persona:        coerceStr(any(persona)),
		Karma:          karma,
		FriendCount:    friendCount,
		FollowerCount:  followerCount,
		StatusesCount:  statusesCount,
		InterestedTopics: []string{},
		CreatedAt:      time.Now().Format("2006-01-02"),
	}
	if p.Bio == "" {
		p.Bio = name
	}
	if p.Persona == "" {
		p.Persona = fmt.Sprintf("%s is a participant in social discussions.", name)
	}
	return p
}

// finalize replica __post_init__ para campos opcionales.
func (p *OasisAgentProfile) finalize() {
	if p.Bio == "" {
		p.Bio = p.Name
	}
	if p.Persona == "" {
		p.Persona = fmt.Sprintf("%s is a participant in social discussions.", p.Name)
	}
	if p.Country == nil || coerceStr(any(*p.Country)) == "" {
		c := defaultProfileCountry
		p.Country = &c
	}
	if p.Profession != nil && coerceStr(any(*p.Profession)) == "" {
		p.Profession = nil
	}
	if p.Gender == nil || coerceStr(any(*p.Gender)) == "" {
		g := defaultProfileGender
		p.Gender = &g
	}
	if p.Mbti == nil || coerceStr(any(*p.Mbti)) == "" {
		m := MBTI_TYPES[rand.Intn(len(MBTI_TYPES))]
		p.Mbti = &m
	}
	if p.Age == nil || *p.Age <= 0 {
		a := defaultProfileAge
		p.Age = &a
	}
	if p.InterestedTopics == nil {
		p.InterestedTopics = []string{}
	}
}

// ToRedditFormat replica to_reddit_format.
func (p *OasisAgentProfile) ToRedditFormat() map[string]any {
	profile := map[string]any{
		"user_id":   p.UserID,
		"username":  p.UserName,
		"name":      p.Name,
		"bio":       p.Bio,
		"persona":   p.Persona,
		"karma":     p.Karma,
		"created_at": p.CreatedAt,
		"mbti":      derefStr(p.Mbti),
		"gender":    derefStr(p.Gender),
		"age":       derefInt(p.Age),
		"country":   derefStr(p.Country),
	}
	if p.Profession != nil {
		profile["profession"] = *p.Profession
	}
	if len(p.InterestedTopics) > 0 {
		profile["interested_topics"] = p.InterestedTopics
	}
	return profile
}

// ToTwitterFormat replica to_twitter_format.
func (p *OasisAgentProfile) ToTwitterFormat() map[string]any {
	profile := map[string]any{
		"user_id":        p.UserID,
		"username":       p.UserName,
		"name":           p.Name,
		"bio":            p.Bio,
		"persona":        p.Persona,
		"friend_count":   p.FriendCount,
		"follower_count": p.FollowerCount,
		"statuses_count": p.StatusesCount,
		"created_at":     p.CreatedAt,
	}
	if p.Age != nil {
		profile["age"] = *p.Age
	}
	if p.Gender != nil {
		profile["gender"] = *p.Gender
	}
	if p.Mbti != nil {
		profile["mbti"] = *p.Mbti
	}
	if p.Country != nil {
		profile["country"] = *p.Country
	}
	if p.Profession != nil {
		profile["profession"] = *p.Profession
	}
	if len(p.InterestedTopics) > 0 {
		profile["interested_topics"] = p.InterestedTopics
	}
	return profile
}

// ToDict replica to_dict.
func (p *OasisAgentProfile) ToDict() map[string]any {
	return map[string]any{
		"user_id":            p.UserID,
		"user_name":          p.UserName,
		"name":               p.Name,
		"bio":                p.Bio,
		"persona":            p.Persona,
		"karma":              p.Karma,
		"friend_count":       p.FriendCount,
		"follower_count":     p.FollowerCount,
		"statuses_count":     p.StatusesCount,
		"age":                p.Age,
		"gender":             p.Gender,
		"mbti":               p.Mbti,
		"country":            p.Country,
		"profession":         p.Profession,
		"interested_topics":  p.InterestedTopics,
		"source_entity_uuid": p.SourceEntityUUID,
		"source_entity_type": p.SourceEntityType,
		"created_at":         p.CreatedAt,
	}
}

// ===== coerción de valores LLM =====

func coerceStr(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case map[string]any:
		for _, key := range []string{"text", "value", "description", "content", "summary", "name"} {
			if raw, ok := value[key]; ok {
				if candidate := coerceStr(raw); candidate != "" {
					return candidate
				}
			}
		}
		b, _ := json.Marshal(value)
		return string(b)
	case []any:
		items := []string{}
		for _, item := range value {
			if s := coerceStr(item); s != "" {
				items = append(items, s)
			}
		}
		return strings.Join(items, ", ")
	case []string:
		return strings.Join(value, ", ")
	default:
		return fmt.Sprintf("%v", value)
	}
}

func coerceStrList(v any) []string {
	if v == nil {
		return []string{}
	}
	if list, ok := v.([]any); ok {
		out := []string{}
		for _, item := range list {
			if sub, ok := item.([]any); ok {
				out = append(out, coerceStrList(sub)...)
				continue
			}
			if sub, ok := item.([]string); ok {
				out = append(out, sub...)
				continue
			}
			if s := coerceStr(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	if list, ok := v.([]string); ok {
		return list
	}
	if s := coerceStr(v); s != "" {
		return []string{s}
	}
	return []string{}
}

// ===== generador =====

// ProfileGenerator replica OasisProfileGenerator.
type ProfileGenerator struct {
	Client *graph.Client
	GraphID string
	Locale  string

	// LLMFactory inyectable (tests); default: llm.NewDefaultClient.
	LLMFactory func() (*llm.Client, error)
}

// NewProfileGenerator crea el generador.
func NewProfileGenerator(client *graph.Client, graphID string) *ProfileGenerator {
	return &ProfileGenerator{Client: client, GraphID: graphID, Locale: "zh"}
}

func (g *ProfileGenerator) llmClient() (*llm.Client, error) {
	if g.LLMFactory != nil {
		return g.LLMFactory()
	}
	return llm.NewDefaultClient()
}

func (g *ProfileGenerator) t(key string, args map[string]string) string {
	return locale.T(g.Locale, key, args)
}

func (g *ProfileGenerator) langInstruction() string {
	return locale.GetLanguageInstruction(g.Locale)
}

// GenerateProfileFromEntity replica generate_profile_from_entity.
func (g *ProfileGenerator) GenerateProfileFromEntity(entity *EntityNode, userID int, useLLM bool) *OasisAgentProfile {
	entityType := "Entity"
	if t := entity.GetEntityType(); t != nil {
		entityType = *t
	}

	name := entity.Name
	userName := g.generateUsername(name)
	context := g.BuildEntityContext(entity)

	var profileData map[string]any
	if useLLM {
		profileData = g.generateProfileWithLLM(name, entityType, entity.Summary, entity.Attributes, context)
	} else {
		profileData = g.generateProfileRuleBased(name, entityType, entity.Summary, entity.Attributes)
	}

	bio := coerceStr(profileData["bio"])
	if bio == "" {
		bio = fmt.Sprintf("%s: %s", entityType, name)
	}
	persona := coerceStr(profileData["persona"])
	if persona == "" {
		persona = entity.Summary
		if persona == "" {
			persona = fmt.Sprintf("A %s named %s.", entityType, name)
		}
	}

	p := NewOasisAgentProfile(userID, userName, name, bio, persona,
		intFromAny(profileData["karma"], rand.Intn(4501)+500),
		intFromAny(profileData["friend_count"], rand.Intn(451)+50),
		intFromAny(profileData["follower_count"], rand.Intn(901)+100),
		intFromAny(profileData["statuses_count"], rand.Intn(1901)+100),
	)

	if v, ok := profileData["age"]; ok {
		if n := intFromAny(v, 0); n > 0 {
			p.Age = &n
		}
	}
	if v, ok := profileData["gender"]; ok {
		if s := coerceStr(v); s != "" {
			p.Gender = &s
		}
	}
	if v, ok := profileData["mbti"]; ok {
		if s := coerceStr(v); s != "" {
			p.Mbti = &s
		}
	}
	if v, ok := profileData["country"]; ok {
		if s := coerceStr(v); s != "" {
			p.Country = &s
		}
	}
	if v, ok := profileData["profession"]; ok {
		if s := coerceStr(v); s != "" {
			p.Profession = &s
		}
	}
	if v, ok := profileData["interested_topics"]; ok {
		p.InterestedTopics = coerceStrList(v)
	}
	uuid := entity.UUID
	p.SourceEntityUUID = &uuid
	etype := entityType
	p.SourceEntityType = &etype
	p.finalize()
	return p
}

func (g *ProfileGenerator) generateUsername(name string) string {
	username := strings.ToLower(strings.ReplaceAll(name, " ", "_"))
	var b strings.Builder
	for _, r := range username {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return fmt.Sprintf("%s_%d", b.String(), rand.Intn(900)+100)
}

// searchZepForEntity replica _search_zep_for_entity con búsquedas paralelas.
func (g *ProfileGenerator) searchZepForEntity(entity *EntityNode) map[string]any {
	results := map[string]any{"facts": []string{}, "node_summaries": []string{}, "context": ""}
	if g.Client == nil || g.GraphID == "" {
		return results
	}

	query := g.t("progress.zepSearchQuery", map[string]string{"name": entity.Name})

	type searchOutcome struct {
		edges *graph.SearchResult
		nodes *graph.SearchResult
		errE  error
		errN  error
	}
	out := make(chan searchOutcome, 1)
	go func() {
		edges, errE := g.searchScope(query, "edges")
		nodes, errN := g.searchScope(query, "nodes")
		out <- searchOutcome{edges, nodes, errE, errN}
	}()
	res := <-out

	if res.errE != nil || res.errN != nil {
		logger.Warn(fmt.Sprintf("Zep检索失败 (%s): edges=%v nodes=%v", entity.Name, res.errE, res.errN))
		return results
	}

	factsSet := map[string]bool{}
	if res.edges != nil {
		for _, e := range res.edges.Edges {
			if e.Fact != "" {
				factsSet[e.Fact] = true
			}
		}
	}
	summariesSet := map[string]bool{}
	if res.nodes != nil {
		for _, n := range res.nodes.Nodes {
			if n.Summary != "" {
				summariesSet[n.Summary] = true
			}
			if n.Name != "" && n.Name != entity.Name {
				summariesSet["相关实体: "+n.Name] = true
			}
		}
	}
	facts := []string{}
	for f := range factsSet {
		facts = append(facts, f)
	}
	summaries := []string{}
	for s := range summariesSet {
		summaries = append(summaries, s)
	}
	results["facts"] = facts
	results["node_summaries"] = summaries

	parts := []string{}
	if len(facts) > 0 {
		lines := []string{}
		for i, f := range facts {
			if i >= 20 {
				break
			}
			lines = append(lines, "- "+f)
		}
		parts = append(parts, "事实信息:\n"+strings.Join(lines, "\n"))
	}
	if len(summaries) > 0 {
		lines := []string{}
		for i, s := range summaries {
			if i >= 10 {
				break
			}
			lines = append(lines, "- "+s)
		}
		parts = append(parts, "相关实体:\n"+strings.Join(lines, "\n"))
	}
	results["context"] = strings.Join(parts, "\n\n")
	return results
}

func (g *ProfileGenerator) searchScope(query, scope string) (*graph.SearchResult, error) {
	limit := 20
	if scope == "edges" {
		limit = 30
	}
	res := g.Client.Search(g.GraphID, query, limit, scope)
	return res, nil
}

// BuildEntityContext replica _build_entity_context.
func (g *ProfileGenerator) BuildEntityContext(entity *EntityNode) string {
	parts := []string{}

	if len(entity.Attributes) > 0 {
		attrs := []string{}
		for key, value := range entity.Attributes {
			if s := coerceStr(value); strings.TrimSpace(s) != "" {
				attrs = append(attrs, fmt.Sprintf("- %s: %s", key, s))
			}
		}
		if len(attrs) > 0 {
			parts = append(parts, "### 实体属性\n"+strings.Join(attrs, "\n"))
		}
	}

	existingFacts := map[string]bool{}
	if len(entity.RelatedEdges) > 0 {
		relationships := []string{}
		for _, edge := range entity.RelatedEdges {
			fact := strOr(edge["fact"])
			edgeName := strOr(edge["edge_name"])
			direction := strOr(edge["direction"])
			if fact != "" {
				relationships = append(relationships, "- "+fact)
				existingFacts[fact] = true
			} else if edgeName != "" {
				if direction == "outgoing" {
					relationships = append(relationships, fmt.Sprintf("- %s --[%s]--> (相关实体)", entity.Name, edgeName))
				} else {
					relationships = append(relationships, fmt.Sprintf("- (相关实体) --[%s]--> %s", edgeName, entity.Name))
				}
			}
		}
		if len(relationships) > 0 {
			parts = append(parts, "### 相关事实和关系\n"+strings.Join(relationships, "\n"))
		}
	}

	if len(entity.RelatedNodes) > 0 {
		relatedInfo := []string{}
		for _, node := range entity.RelatedNodes {
			nodeName := strOr(node["name"])
			nodeLabels := stringList(node["labels"])
			nodeSummary := strOr(node["summary"])
			customLabels := []string{}
			for _, l := range nodeLabels {
				if l != "Entity" && l != "Node" {
					customLabels = append(customLabels, l)
				}
			}
			labelStr := ""
			if len(customLabels) > 0 {
				labelStr = fmt.Sprintf(" (%s)", strings.Join(customLabels, ", "))
			}
			if nodeSummary != "" {
				relatedInfo = append(relatedInfo, fmt.Sprintf("- **%s**%s: %s", nodeName, labelStr, nodeSummary))
			} else {
				relatedInfo = append(relatedInfo, fmt.Sprintf("- **%s**%s", nodeName, labelStr))
			}
		}
		if len(relatedInfo) > 0 {
			parts = append(parts, "### 关联实体信息\n"+strings.Join(relatedInfo, "\n"))
		}
	}

	zepResults := g.searchZepForEntity(entity)
	if facts, ok := zepResults["facts"].([]string); ok && len(facts) > 0 {
		newFacts := []string{}
		for _, f := range facts {
			if !existingFacts[f] {
				newFacts = append(newFacts, f)
			}
		}
		if len(newFacts) > 0 {
			lines := []string{}
			for i, f := range newFacts {
				if i >= 15 {
					break
				}
				lines = append(lines, "- "+f)
			}
			parts = append(parts, "### Zep检索到的事实信息\n"+strings.Join(lines, "\n"))
		}
	}
	if summaries, ok := zepResults["node_summaries"].([]string); ok && len(summaries) > 0 {
		lines := []string{}
		for i, s := range summaries {
			if i >= 10 {
				break
			}
			lines = append(lines, "- "+s)
		}
		parts = append(parts, "### Zep检索到的相关节点\n"+strings.Join(lines, "\n"))
	}

	return strings.Join(parts, "\n\n")
}

func (g *ProfileGenerator) isIndividualEntity(entityType string) bool {
	lt := strings.ToLower(entityType)
	for _, t := range individualEntityTypes {
		if lt == t {
			return true
		}
	}
	return false
}

func (g *ProfileGenerator) generateProfileWithLLM(entityName, entityType, entitySummary string, entityAttributes map[string]any, context string) map[string]any {
	isIndividual := g.isIndividualEntity(entityType)
	var prompt string
	if isIndividual {
		prompt = g.buildIndividualPersonaPrompt(entityName, entityType, entitySummary, entityAttributes, context)
	} else {
		prompt = g.buildGroupPersonaPrompt(entityName, entityType, entitySummary, entityAttributes, context)
	}

	systemPrompt := g.systemPrompt(isIndividual)
	maxAttempts := 3
	lastError := error(nil)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		client, err := g.llmClient()
		if err != nil {
			lastError = err
			time.Sleep(time.Duration(attempt+1) * time.Second)
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
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		content := llm.ExtractChatCompletionText(resp)
		if len(resp.Choices) > 0 && resp.Choices[0].FinishReason == "length" {
			logger.Warn(fmt.Sprintf("LLM输出被截断 (attempt %d), 尝试修复...", attempt+1))
			content = fixTruncatedJSON(content)
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(content), &result); err == nil {
			if bio, ok := result["bio"]; !ok || coerceStr(bio) == "" {
				if entitySummary != "" && len([]rune(entitySummary)) > 200 {
					result["bio"] = string([]rune(entitySummary)[:200])
				} else {
					result["bio"] = fmt.Sprintf("%s: %s", entityType, entityName)
				}
			}
			if persona, ok := result["persona"]; !ok || coerceStr(persona) == "" {
				if entitySummary != "" {
					result["persona"] = entitySummary
				} else {
					result["persona"] = fmt.Sprintf("%s是一个%s。", entityName, entityType)
				}
			}
			return result
		} else {
			lastError = err
			fixed := g.tryFixJSON(content, entityName, entityType, entitySummary)
			if fixed != nil {
				if _, hadFixed := fixed["_fixed"]; hadFixed {
					delete(fixed, "_fixed")
					return fixed
				}
			}
		}
	}

	logger.Warn(fmt.Sprintf("LLM生成人设失败（%d次尝试）: %v, 使用规则生成", maxAttempts, lastError))
	return g.generateProfileRuleBased(entityName, entityType, entitySummary, entityAttributes)
}

func fixTruncatedJSON(content string) string {
	content = strings.TrimSpace(content)
	openBraces := strings.Count(content, "{") - strings.Count(content, "}")
	openBrackets := strings.Count(content, "[") - strings.Count(content, "]")
	if len(content) > 0 && !strings.ContainsRune("\",}]", rune(content[len(content)-1])) {
		content += "\""
	}
	content += strings.Repeat("]", openBrackets)
	content += strings.Repeat("}", openBraces)
	return content
}

var reJSONBlock = regexp.MustCompile(`(?s)\{.*\}`)
var reJSONString = regexp.MustCompile(`"[^"\\]*(?:\\.[^"\\]*)*"`)
var reControlChars = regexp.MustCompile("[\\x00-\\x1f\\x{7f}-\\x{9f}]")

func (g *ProfileGenerator) tryFixJSON(content, entityName, entityType, entitySummary string) map[string]any {
	content = fixTruncatedJSON(content)
	if match := reJSONBlock.FindString(content); match != "" {
		jsonStr := match
		jsonStr = reJSONString.ReplaceAllStringFunc(jsonStr, func(m string) string {
			s := strings.ReplaceAll(m, "\n", " ")
			s = strings.ReplaceAll(s, "\r", " ")
			return regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
		})
		var result map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &result); err == nil {
			result["_fixed"] = true
			return result
		}
		jsonStr = reControlChars.ReplaceAllString(jsonStr, " ")
		jsonStr = regexp.MustCompile(`\s+`).ReplaceAllString(jsonStr, " ")
		if err := json.Unmarshal([]byte(jsonStr), &result); err == nil {
			result["_fixed"] = true
			return result
		}
	}

	bio := ""
	persona := ""
	if m := regexp.MustCompile(`"bio"\s*:\s*"([^"]*)"`).FindStringSubmatch(content); len(m) > 1 {
		bio = m[1]
	} else if entitySummary != "" {
		if len([]rune(entitySummary)) > 200 {
			bio = string([]rune(entitySummary)[:200])
		} else {
			bio = entitySummary
		}
	} else {
		bio = fmt.Sprintf("%s: %s", entityType, entityName)
	}
	if m := regexp.MustCompile(`"persona"\s*:\s*"([^"]*)`).FindStringSubmatch(content); len(m) > 1 {
		persona = m[1]
	} else if entitySummary != "" {
		persona = entitySummary
	} else {
		persona = fmt.Sprintf("%s是一个%s。", entityName, entityType)
	}
	return map[string]any{"bio": bio, "persona": persona, "_fixed": true}
}

func (g *ProfileGenerator) systemPrompt(isIndividual bool) string {
	base := "你是社交媒体用户画像生成专家。生成详细、真实的人设用于舆论模拟,最大程度还原已有现实情况。必须返回有效的JSON格式，所有字符串值不能包含未转义的换行符。"
	return base + "\n\n" + g.langInstruction()
}

func (g *ProfileGenerator) buildIndividualPersonaPrompt(entityName, entityType, entitySummary string, entityAttributes map[string]any, context string) string {
	attrsStr := "无"
	if len(entityAttributes) > 0 {
		if b, err := json.Marshal(entityAttributes); err == nil {
			attrsStr = string(b)
		}
	}
	contextStr := "无额外上下文"
	if context != "" {
		if len([]rune(context)) > 3000 {
			contextStr = string([]rune(context)[:3000])
		} else {
			contextStr = context
		}
	}
	return fmt.Sprintf(`为实体生成详细的社交媒体用户人设,最大程度还原已有现实情况。

实体名称: %s
实体类型: %s
实体摘要: %s
实体属性: %s

上下文信息:
%s

请生成JSON，包含以下字段:

1. bio: 社交媒体简介，200字
2. persona: 详细人设描述（2000字的纯文本），需包含:
   - 基本信息（年龄、职业、教育背景、所在地）
   - 人物背景（重要经历、与事件的关联、社会关系）
   - 性格特征（MBTI类型、核心性格、情绪表达方式）
   - 社交媒体行为（发帖频率、内容偏好、互动风格、语言特点）
   - 立场观点（对话题的态度、可能被激怒/感动的内容）
   - 独特特征（口头禅、特殊经历、个人爱好）
   - 个人记忆（人设的重要部分，要介绍这个个体与事件的关联，以及这个个体在事件中的已有动作与反应）
3. age: 年龄数字（必须是整数）
4. gender: 性别，必须是英文: "male" 或 "female"
5. mbti: MBTI类型（如INTJ、ENFP等）
6. country: 国家（使用中文，如"中国"）
7. profession: 职业
8. interested_topics: 感兴趣话题数组

重要:
- 所有字段值必须是字符串或数字，不要使用换行符
- persona必须是一段连贯的文字描述
- %s (gender字段必须用英文male/female)
- 内容要与实体信息保持一致
- age必须是有效的整数，gender必须是"male"或"female"
`, entityName, entityType, entitySummary, attrsStr, contextStr, g.langInstruction())
}

func (g *ProfileGenerator) buildGroupPersonaPrompt(entityName, entityType, entitySummary string, entityAttributes map[string]any, context string) string {
	attrsStr := "无"
	if len(entityAttributes) > 0 {
		if b, err := json.Marshal(entityAttributes); err == nil {
			attrsStr = string(b)
		}
	}
	contextStr := "无额外上下文"
	if context != "" {
		if len([]rune(context)) > 3000 {
			contextStr = string([]rune(context)[:3000])
		} else {
			contextStr = context
		}
	}
	return fmt.Sprintf(`为机构/群体实体生成详细的社交媒体账号设定,最大程度还原已有现实情况。

实体名称: %s
实体类型: %s
实体摘要: %s
实体属性: %s

上下文信息:
%s

请生成JSON，包含以下字段:

1. bio: 官方账号简介，200字，专业得体
2. persona: 详细账号设定描述（2000字的纯文本），需包含:
   - 机构基本信息（正式名称、机构性质、成立背景、主要职能）
   - 账号定位（账号类型、目标受众、核心功能）
   - 发言风格（语言特点、常用表达、禁忌话题）
   - 发布内容特点（内容类型、发布频率、活跃时间段）
   - 立场态度（对核心话题的官方立场、面对争议的处理方式）
   - 特殊说明（代表的群体画像、运营习惯）
   - 机构记忆（机构人设的重要部分，要介绍这个机构与事件的关联，以及这个机构在事件中的已有动作与反应）
3. age: 固定填30（机构账号的虚拟年龄）
4. gender: 固定填"other"（机构账号使用other表示非个人）
5. mbti: MBTI类型，用于描述账号风格，如ISTJ代表严谨保守
6. country: 国家（使用中文，如"中国"）
7. profession: 机构职能描述
8. interested_topics: 关注领域数组

重要:
- 所有字段值必须是字符串或数字，不允许null值
- persona必须是一段连贯的文字描述，不要使用换行符
- %s (gender字段必须用英文"other")
- age必须是整数30，gender必须是字符串"other"
- 机构账号发言要符合其身份定位`, entityName, entityType, entitySummary, attrsStr, contextStr, g.langInstruction())
}

func (g *ProfileGenerator) generateProfileRuleBased(entityName, entityType, entitySummary string, entityAttributes map[string]any) map[string]any {
	lt := strings.ToLower(entityType)
	switch {
	case lt == "student" || lt == "alumni":
		return map[string]any{
			"bio":               fmt.Sprintf("%s with interests in academics and social issues.", entityType),
			"persona":           fmt.Sprintf("%s is a %s who is actively engaged in academic and social discussions. They enjoy sharing perspectives and connecting with peers.", entityName, lt),
			"age":               rand.Intn(13) + 18,
			"gender":            []string{"male", "female"}[rand.Intn(2)],
			"mbti":              MBTI_TYPES[rand.Intn(len(MBTI_TYPES))],
			"country":           profileCountries[rand.Intn(len(profileCountries))],
			"profession":        "Student",
			"interested_topics": []string{"Education", "Social Issues", "Technology"},
		}
	case lt == "publicfigure" || lt == "expert" || lt == "faculty":
		return map[string]any{
			"bio":               "Expert and thought leader in their field.",
			"persona":           fmt.Sprintf("%s is a recognized %s who shares insights and opinions on important matters. They are known for their expertise and influence in public discourse.", entityName, lt),
			"age":               rand.Intn(26) + 35,
			"gender":            []string{"male", "female"}[rand.Intn(2)],
			"mbti":              []string{"ENTJ", "INTJ", "ENTP", "INTP"}[rand.Intn(4)],
			"country":           profileCountries[rand.Intn(len(profileCountries))],
			"profession":        defaultStr(strOr(entityAttributes["occupation"]), "Expert"),
			"interested_topics": []string{"Politics", "Economics", "Culture & Society"},
		}
	case lt == "mediaoutlet" || lt == "socialmediaplatform":
		return map[string]any{
			"bio":               fmt.Sprintf("Official account for %s. News and updates.", entityName),
			"persona":           fmt.Sprintf("%s is a media entity that reports news and facilitates public discourse. The account shares timely updates and engages with the audience on current events.", entityName),
			"age":               30,
			"gender":            "other",
			"mbti":              "ISTJ",
			"country":           "中国",
			"profession":        "Media",
			"interested_topics": []string{"General News", "Current Events", "Public Affairs"},
		}
	case lt == "university" || lt == "governmentagency" || lt == "ngo" || lt == "organization":
		return map[string]any{
			"bio":               fmt.Sprintf("Official account of %s.", entityName),
			"persona":           fmt.Sprintf("%s is an institutional entity that communicates official positions, announcements, and engages with stakeholders on relevant matters.", entityName),
			"age":               30,
			"gender":            "other",
			"mbti":              "ISTJ",
			"country":           "中国",
			"profession":        entityType,
			"interested_topics": []string{"Public Policy", "Community", "Official Announcements"},
		}
	default:
		bio := ""
		if entitySummary != "" {
			if len([]rune(entitySummary)) > 150 {
				bio = string([]rune(entitySummary)[:150])
			} else {
				bio = entitySummary
			}
		} else {
			bio = fmt.Sprintf("%s: %s", entityType, entityName)
		}
		persona := entitySummary
		if persona == "" {
			persona = fmt.Sprintf("%s is a %s participating in social discussions.", entityName, lt)
		}
		return map[string]any{
			"bio":               bio,
			"persona":           persona,
			"age":               rand.Intn(26) + 25,
			"gender":            []string{"male", "female"}[rand.Intn(2)],
			"mbti":              MBTI_TYPES[rand.Intn(len(MBTI_TYPES))],
			"country":           profileCountries[rand.Intn(len(profileCountries))],
			"profession":        entityType,
			"interested_topics": []string{"General", "Social Issues"},
		}
	}
}

// GenerateProfilesFromEntities replica generate_profiles_from_entities con
// ejecución paralela, guardado en tiempo real y progreso.
func (g *ProfileGenerator) GenerateProfilesFromEntities(
	entities []EntityNode,
	useLLM bool,
	progressCallback func(current, total int, msg string),
	graphID string,
	parallelCount int,
	realtimeOutputPath string,
	outputPlatform string,
) []*OasisAgentProfile {
	if graphID != "" {
		g.GraphID = graphID
	}
	total := len(entities)
	if parallelCount <= 0 {
		parallelCount = 5
	}
	profiles := make([]*OasisAgentProfile, total)
	var mu sync.Mutex
	completedCount := 0

	saveRealtime := func() {
		if realtimeOutputPath == "" {
			return
		}
		mu.Lock()
		existing := []*OasisAgentProfile{}
		for _, p := range profiles {
			if p != nil {
				existing = append(existing, p)
			}
		}
		mu.Unlock()
		if len(existing) == 0 {
			return
		}
		if outputPlatform == "twitter" {
			_ = g.SaveProfiles(existing, realtimeOutputPath, "twitter")
		} else {
			_ = g.SaveProfiles(existing, realtimeOutputPath, "reddit")
		}
	}

	sem := make(chan struct{}, parallelCount)
	var wg sync.WaitGroup
	for idx := range entities {
		wg.Add(1)
		go func(idx int, entity EntityNode) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			entityType := "Entity"
			if t := entity.GetEntityType(); t != nil {
				entityType = *t
			}
			profile := g.GenerateProfileFromEntity(&entity, idx, useLLM)

			mu.Lock()
			profiles[idx] = profile
			completedCount++
			current := completedCount
			mu.Unlock()

			saveRealtime()
			if progressCallback != nil {
				progressCallback(current, total, fmt.Sprintf("已完成 %d/%d: %s（%s）", current, total, entity.Name, entityType))
			}
		}(idx, entities[idx])
	}
	wg.Wait()

	out := []*OasisAgentProfile{}
	for _, p := range profiles {
		if p != nil {
			out = append(out, p)
		}
	}
	return out
}

// SaveProfiles replica save_profiles (reddit JSON / twitter CSV).
func (g *ProfileGenerator) SaveProfiles(profiles []*OasisAgentProfile, filePath, platform string) error {
	if platform == "twitter" {
		return g.saveTwitterCSV(profiles, filePath)
	}
	return g.saveRedditJSON(profiles, filePath)
}

func (g *ProfileGenerator) saveTwitterCSV(profiles []*OasisAgentProfile, filePath string) error {
	if !strings.HasSuffix(filePath, ".csv") {
		filePath = strings.Replace(filePath, ".json", ".csv", 1)
	}
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"user_id", "name", "username", "user_char", "description"}); err != nil {
		return err
	}
	for idx, profile := range profiles {
		userChar := profile.Bio
		if profile.Persona != "" && profile.Persona != profile.Bio {
			userChar = profile.Bio + " " + profile.Persona
		}
		userChar = strings.ReplaceAll(strings.ReplaceAll(userChar, "\n", " "), "\r", " ")
		description := strings.ReplaceAll(strings.ReplaceAll(profile.Bio, "\n", " "), "\r", " ")
		if err := w.Write([]string{
			fmt.Sprintf("%d", idx), profile.Name, profile.UserName, userChar, description,
		}); err != nil {
			return err
		}
	}
	return nil
}

func normalizeGender(gender *string) string {
	if gender == nil || *gender == "" {
		return defaultProfileGender
	}
	switch strings.ToLower(strings.TrimSpace(*gender)) {
	case "男", "male":
		return "male"
	case "女", "female":
		return "female"
	case "机构", "其他", "other":
		return "other"
	default:
		return "other"
	}
}

func (g *ProfileGenerator) buildRedditItem(profile *OasisAgentProfile, idx int) map[string]any {
	item := map[string]any{
		"user_id":    profile.UserID,
		"username":   profile.UserName,
		"name":       profile.Name,
		"bio":        truncateRunes(profile.Bio, 150),
		"persona":    profile.Persona,
		"karma":      profile.Karma,
		"created_at": profile.CreatedAt,
		"age":        defaultProfileAge,
		"gender":     normalizeGender(profile.Gender),
		"mbti":       defaultMbti(),
		"country":    defaultProfileCountry,
	}
	if profile.UserID == 0 {
		item["user_id"] = idx
	}
	if profile.Karma == 0 {
		item["karma"] = 1000
	}
	if profile.Age != nil && *profile.Age > 0 {
		item["age"] = *profile.Age
	}
	if profile.Mbti != nil && *profile.Mbti != "" {
		item["mbti"] = *profile.Mbti
	}
	if profile.Country != nil && *profile.Country != "" {
		item["country"] = *profile.Country
	}
	if profile.Profession != nil {
		item["profession"] = *profile.Profession
	}
	if len(profile.InterestedTopics) > 0 {
		item["interested_topics"] = profile.InterestedTopics
	}
	return item
}

func (g *ProfileGenerator) saveRedditJSON(profiles []*OasisAgentProfile, filePath string) error {
	data := []map[string]any{}
	for idx, p := range profiles {
		data = append(data, g.buildRedditItem(p, idx))
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, b, 0o644)
}

// ===== helpers =====

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

func intFromAny(v any, def int) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	}
	return def
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func defaultMbti() string { return MBTI_TYPES[rand.Intn(len(MBTI_TYPES))] }
