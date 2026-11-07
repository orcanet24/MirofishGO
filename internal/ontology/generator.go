// Port de backend/app/services/ontology_generator.py — OntologyGenerator.
// Genera (vía LLM) entidades/relaciones tipo social-sim a partir de documentos.
package ontology

import (
	"fmt"
	"math"
	"strings"
	"unicode"

	"mirofish/internal/llm"
	"mirofish/internal/locale"
	tex "mirofish/internal/text"
)

// OntologySystemPrompt — misma cadena que ONTOLOGY_SYSTEM_PROMPT del Python.
const OntologySystemPrompt = `你是一个专业的知识图谱本体设计专家。你的任务是分析给定的文本内容和模拟需求，设计适合**社交媒体舆论模拟**的实体类型和关系类型。

**重要：你必须输出有效的JSON格式数据，不要输出任何其他内容。**

## 核心任务背景

我们正在构建一个**社交媒体舆论模拟系统**。在这个系统中：
- 每个实体都是一个可以在社交媒体上发声、互动、传播信息的"账号"或"主体"
- 实体之间会相互影响、转发、评论、回应
- 我们需要模拟舆论事件中各方的反应和信息传播路径

因此，**实体必须是现实中真实存在的、可以在社媒上发声和互动的主体**：

**可以是**：
- 具体的个人（公众人物、当事人、意见领袖、专家学者、普通人）
- 公司、企业（包括其官方账号）
- 组织机构（大学、协会、NGO、工会等）
- 政府部门、监管机构
- 媒体机构（报纸、电视台、自媒体、网站）
- 社交媒体平台本身
- 特定群体代表（如校友会、粉丝团、维权群体等）

**不可以是**：
- 抽象概念（如"舆论"、"情绪"、"趋势"）
- 主题/话题（如"学术诚信"、"教育改革"）
- 观点/态度（如"支持方"、"反对方"）

## 输出格式

请输出JSON格式，包含以下结构：

{
    "entity_types": [
        {
            "name": "实体类型名称（英文，PascalCase）",
            "description": "简短描述（英文，不超过100字符）",
            "attributes": [
                {
                    "name": "属性名（英文，snake_case）",
                    "type": "text",
                    "description": "属性描述"
                }
            ],
            "examples": ["示例实体1", "示例实体2"]
        }
    ],
    "edge_types": [
        {
            "name": "关系类型名称（英文，UPPER_SNAKE_CASE）",
            "description": "简短描述（英文，不超过100字符）",
            "source_targets": [
                {"source": "源实体类型", "target": "目标实体类型"}
            ],
            "attributes": []
        }
    ],
    "analysis_summary": "对文本内容的简要分析说明"
}

## 设计指南（极其重要！）

### 1. 实体类型设计 - 必须严格遵守

**数量要求：必须正好10个实体类型**

**层次结构要求（必须同时包含具体类型和兜底类型）**：

你的10个实体类型必须包含以下层次：

A. **兜底类型（必须包含，放在列表最后2个）**：
   - `+"`Person`"+`: 任何自然人个体的兜底类型。当一个人不属于其他更具体的人物类型时，归入此类。
   - `+"`Organization`"+`: 任何组织机构的兜底类型。当一个组织不属于其他更具体的组织类型时，归入此类。

B. **具体类型（8个，根据文本内容设计）**：
   - 针对文本中出现的主要角色，设计更具体的类型
   - 例如：如果文本涉及学术事件，可以有 `+"`Student`"+`, `+"`Professor`"+`, `+"`University`"+`
   - 例如：如果文本涉及商业事件，可以有 `+"`Company`"+`, `+"`CEO`"+`, `+"`Employee`"+`

**为什么需要兜底类型**：
- 文本中会出现各种人物，如"中小学教师"、"路人甲"、"某位网友"
- 如果没有专门的类型匹配，他们应该被归入 `+"`Person`"+`
- 同理，小型组织、临时团体等应该归入 `+"`Organization`"+`

**具体类型的设计原则**：
- 从文本中识别出高频出现或关键的角色类型
- 每个具体类型应该有明确的边界，避免重叠
- description 必须清晰说明这个类型和兜底类型的区别

### 2. 关系类型设计

- 数量：6-10个
- 关系应该反映社媒互动中的真实联系
- 确保关系的 source_targets 涵盖你定义的实体类型

### 3. 属性设计

- 每个实体类型1-3个关键属性
- **注意**：属性名不能使用 `+"`name`"+`、`+"`uuid`"+`、`+"`group_id`"+`、`+"`graph_id`"+`、`+"`created_at`"+`、`+"`summary`"+`（这些是系统保留字）
- 推荐使用：`+"`full_name`"+`, `+"`title`"+`, `+"`role`"+`, `+"`position`"+`, `+"`location`"+`, `+"`description`"+` 等

## 实体类型参考

**个人类（具体）**：
- Student: 学生
- Professor: 教授/学者
- Journalist: 记者
- Celebrity: 明星/网红
- Executive: 高管
- Official: 政府官员
- Lawyer: 律师
- Doctor: 医生

**个人类（兜底）**：
- Person: 任何自然人（不属于上述具体类型时使用）

**组织类（具体）**：
- University: 高校
- Company: 公司企业
- GovernmentAgency: 政府机构
- MediaOutlet: 媒体机构
- Hospital: 医院
- School: 中小学
- NGO: 非政府组织

**组织类（兜底）**：
- Organization: 任何组织机构（不属于上述具体类型时使用）

## 关系类型参考

- WORKS_FOR: 工作于
- STUDIES_AT: 就读于
- AFFILIATED_WITH: 隶属于
- REPRESENTS: 代表
- REGULATES: 监管
- REPORTS_ON: 报道
- COMMENTS_ON: 评论
- RESPONDS_TO: 回应
- SUPPORTS: 支持
- OPPOSES: 反对
- COLLABORATES_WITH: 合作
- COMPETES_WITH: 竞争
`

// límites de texto largo — defaults idénticos a los atributos de clase del
// Python (sobrescribibles por instancia, como en los tests).
const defaultMaxTextLengthForLLM = 50000

// Generator replica OntologyGenerator.
type Generator struct {

	// LLM: cliente inyectable (tests); nil → default desde config.
	LLM func() (*llm.Client, error)

	// límites de contexto largo (equivalentes a los atributos de clase
	// MAX_TEXT_LENGTH_FOR_LLM, etc. del Python, mutables en tests).
	MaxTextLengthForLLM int
	LongTextChunkSize   int
	LongTextChunkOverlap int
	MaxLongTextChunks   int
	MinLongTextExcerpt  int
}

// NewGenerator crea el generador con los límites por defecto del Python.
func NewGenerator() *Generator {
	return &Generator{
		MaxTextLengthForLLM:  defaultMaxTextLengthForLLM,
		LongTextChunkSize:    8000,
		LongTextChunkOverlap: 200,
		MaxLongTextChunks:    60,
		MinLongTextExcerpt:   400,
	}
}

// chatClient resuelve el cliente LLM.
func (g *Generator) chatClient() (*llm.Client, error) {
	if g.LLM != nil {
		return g.LLM()
	}
	return llm.NewDefaultClient()
}

// ensureLimits aplica los defaults del Python si el generador se construyó
// sin NewGenerator (defensa para rutas/testes).
func (g *Generator) ensureLimits() {
	if g.MaxTextLengthForLLM <= 0 {
		g.MaxTextLengthForLLM = defaultMaxTextLengthForLLM
	}
	if g.LongTextChunkSize <= 0 {
		g.LongTextChunkSize = 8000
	}
	if g.LongTextChunkOverlap < 0 {
		g.LongTextChunkOverlap = 200
	}
	if g.MaxLongTextChunks <= 0 {
		g.MaxLongTextChunks = 60
	}
	if g.MinLongTextExcerpt <= 0 {
		g.MinLongTextExcerpt = 400
	}
}

// Generate replica generate(): mensaje + chat_json (temp 0.3, sin cap de
// tokens, 2 intentos) + validación/post-proceso.
func (g *Generator) Generate(documentTexts []string, simulationRequirement, additionalContext string) (map[string]any, error) {
	g.ensureLimits()
	userMessage := g.BuildUserMessage(documentTexts, simulationRequirement, additionalContext)
	langInstruction := locale.GetLanguageInstruction("")
	systemPrompt := OntologySystemPrompt + "\n\n" + langInstruction + "\nIMPORTANT: Entity type names MUST be in English PascalCase (e.g., 'PersonEntity', 'MediaOrganization'). Relationship type names MUST be in English UPPER_SNAKE_CASE (e.g., 'WORKS_FOR'). Attribute names MUST be in English snake_case. Only description fields and analysis_summary should use the specified language above."

	client, err := g.chatClient()
	if err != nil {
		return nil, err
	}
	result, err := client.ChatJSON([]llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMessage},
	}, 0.3, nil, 2)
	if err != nil {
		return nil, err
	}
	return ValidateAndProcess(result)
}

// BuildUserMessage replica _build_user_message.
func (g *Generator) BuildUserMessage(documentTexts []string, simulationRequirement, additionalContext string) string {
	combined := g.BuildDocumentContext(documentTexts)

	msg := fmt.Sprintf(`## 模拟需求

%s

## 文档内容

%s
`, simulationRequirement, combined)

	if additionalContext != "" {
		msg += fmt.Sprintf(`
## 额外说明

%s
`, additionalContext)
	}

	msg += `
请根据以上内容，设计适合社会舆论模拟的实体类型和关系类型。

**必须遵守的规则**：
1. 必须正好输出10个实体类型
2. 最后2个必须是兜底类型：Person（个人兜底）和 Organization（组织兜底）
3. 前8个是根据文本内容设计的具体类型
4. 所有实体类型必须是现实中可以发声的主体，不能是抽象概念
5. 属性名不能使用 name、uuid、group_id、graph_id 等保留字，用 full_name、org_name 等替代
`
	return msg
}

// BuildDocumentContext replica _build_document_context: textos largos por
// chunking global con sampling representativo en vez de solo el inicio.
func (g *Generator) BuildDocumentContext(documentTexts []string) string {
	g.ensureLimits()
	combined := strings.Join(documentTexts, "\n\n---\n\n")
	originalLength := len([]rune(combined))

	if originalLength <= g.MaxTextLengthForLLM {
		return combined
	}

	chunks := g.CollectDocumentChunks(documentTexts)
	if len(chunks) == 0 {
		return ""
	}

	selected := g.SelectRepresentativeChunks(chunks)
	excerptBudget := g.CalculateExcerptBudget(len(selected))
	context := g.RenderChunkedContext(selected, originalLength, len(chunks), excerptBudget)

	runes := []rune(context)
	for len(runes) > g.MaxTextLengthForLLM && excerptBudget > g.MinLongTextExcerpt {
		excerptBudget = maxInt(g.MinLongTextExcerpt, int(float64(excerptBudget)*0.85))
		context = g.RenderChunkedContext(selected, originalLength, len(chunks), excerptBudget)
		runes = []rune(context)
	}

	if len(runes) > g.MaxTextLengthForLLM {
		marker := "\n\n...(分块上下文已压缩到本体分析长度限制内)..."
		keep := g.MaxTextLengthForLLM - len([]rune(marker))
		if keep < 0 {
			keep = 0
		}
		if keep > len(runes) {
			keep = len(runes)
		}
		runes = runes[:keep]
		context = string(runes) + marker
	}
	return context
}

// ChunkMeta replica el dict de _collect_document_chunks.
type ChunkMeta struct {
	DocumentIndex      int
	ChunkIndex         int
	TotalDocumentChunks int
	Text               string
}

// CollectDocumentChunks replica _collect_document_chunks.
func (g *Generator) CollectDocumentChunks(documentTexts []string) []ChunkMeta {
	allChunks := []ChunkMeta{}
	for docIndex, text := range documentTexts {
		docChunks := tex.SplitTextIntoChunks(text, g.LongTextChunkSize, g.LongTextChunkOverlap)
		for chunkIndex, chunk := range docChunks {
			allChunks = append(allChunks, ChunkMeta{
				DocumentIndex:      docIndex + 1,
				ChunkIndex:         chunkIndex + 1,
				TotalDocumentChunks: len(docChunks),
				Text:               chunk,
			})
		}
	}
	return allChunks
}

// SelectRepresentativeChunks replica _select_representative_chunks: muestreo
// equidistante que cubre inicio, medio y final, ordenado (set+sorted Python).
func (g *Generator) SelectRepresentativeChunks(chunks []ChunkMeta) []ChunkMeta {
	if len(chunks) <= g.MaxLongTextChunks {
		return chunks
	}
	if g.MaxLongTextChunks <= 1 {
		return chunks[:1]
	}
	lastIndex := len(chunks) - 1
	selectedIndexes := map[int]bool{}
	for i := 0; i < g.MaxLongTextChunks; i++ {
		// round() de Python: banker's rounding → math.RoundToEven.
		idx := int(math.RoundToEven(float64(i*lastIndex) / float64(g.MaxLongTextChunks-1)))
		selectedIndexes[idx] = true
	}
	out := []ChunkMeta{}
	for i := 0; i < len(chunks); i++ {
		if selectedIndexes[i] {
			out = append(out, chunks[i])
		}
	}
	return out
}

// CalculateExcerptBudget replica _calculate_excerpt_budget.
func (g *Generator) CalculateExcerptBudget(selectedCount int) int {
	headerBudget := 600
	chunkHeaderBudget := 120 * selectedCount
	available := maxInt(g.MinLongTextExcerpt*selectedCount, g.MaxTextLengthForLLM-headerBudget-chunkHeaderBudget)
	denom := selectedCount
	if denom < 1 {
		denom = 1
	}
	return maxInt(g.MinLongTextExcerpt, available/denom)
}

// RenderChunkedContext replica _render_chunked_context.
func (g *Generator) RenderChunkedContext(selectedChunks []ChunkMeta, originalLength, totalChunks, excerptLimit int) string {
	lines := []string{
		fmt.Sprintf("【长文本自动分块摘要】原文共%d字，已分为%d个文本块用于全局覆盖分析。", originalLength, totalChunks),
		fmt.Sprintf("以下展示其中%d个代表性文本块的摘录，覆盖开头、中段和结尾；请基于这些跨全文线索设计本体，不要只依赖第一段内容。", len(selectedChunks)),
	}
	for _, chunk := range selectedChunks {
		excerpt := ExcerptText(chunk.Text, excerptLimit)
		lines = append(lines, fmt.Sprintf("--- 文档 %d / 分块 %d/%d ---\n%s",
			chunk.DocumentIndex, chunk.ChunkIndex, chunk.TotalDocumentChunks, excerpt))
	}
	return strings.Join(lines, "\n\n")
}

// ExcerptText replica _excerpt_text: mantiene inicio y fin del bloque.
func ExcerptText(text string, charLimit int) string {
	text = strings.TrimSpace(text)
	size := len([]rune(text))
	if size <= charLimit {
		return text
	}

	marker := "\n...(本分块中间内容省略)...\n"
	if charLimit <= len([]rune(marker))+20 {
		return string([]rune(text)[:charLimit])
	}
	remaining := charLimit - len([]rune(marker))
	headLen := remaining / 2
	tailLen := remaining - headLen
	runes := []rune(text)
	head := strings.TrimRightFunc(string(runes[:headLen]), unicode.IsSpace)
	tail := strings.TrimLeftFunc(string(runes[size-tailLen:]), unicode.IsSpace)
	return head + marker + tail
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
