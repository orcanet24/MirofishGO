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

// límites de texto largo — defaults idénticos a los atributos de clase del
// Python (sobrescribibles por instancia, como en los tests).
const defaultMaxTextLengthForLLM = 50000

// Generator replica OntologyGenerator.
type Generator struct {

	// LLM: cliente inyectable (tests); nil → default desde config.
	LLM func() (*llm.Client, error)

	// Locale: idioma de los prompts (default "es"; "en" para inglés).
	Locale string

	// límites de contexto largo (equivalentes a los atributos de clase
	// MAX_TEXT_LENGTH_FOR_LLM, etc. del Python, mutables en tests).
	MaxTextLengthForLLM  int
	LongTextChunkSize    int
	LongTextChunkOverlap int
	MaxLongTextChunks    int
	MinLongTextExcerpt   int
}

// NewGenerator crea el generador con los límites por defecto del Python.
func NewGenerator() *Generator {
	return &Generator{
		Locale:               "es",
		MaxTextLengthForLLM:  defaultMaxTextLengthForLLM,
		LongTextChunkSize:    8000,
		LongTextChunkOverlap: 200,
		MaxLongTextChunks:    60,
		MinLongTextExcerpt:   400,
	}
}

// t resuelve una traducción del generador (fallback es del paquete locale).
func (g *Generator) t(key string, args map[string]string) string {
	return locale.T(g.Locale, key, args)
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
	if g.Locale == "" {
		g.Locale = "es"
	}
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
	langInstruction := locale.GetLanguageInstruction(g.Locale)
	systemPrompt := g.t("prompt.ontology.system", nil) + "\n\n" + langInstruction + "\nIMPORTANT: Entity type names MUST be in English PascalCase (e.g., 'PersonEntity', 'MediaOrganization'). Relationship type names MUST be in English UPPER_SNAKE_CASE (e.g., 'WORKS_FOR'). Attribute names MUST be in English snake_case. Only description fields and analysis_summary should use the specified language above."

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
	g.ensureLimits()
	combined := g.BuildDocumentContext(documentTexts)

	msg := fmt.Sprintf(g.t("prompt.ontology.user", nil), simulationRequirement, combined)

	if additionalContext != "" {
		msg += fmt.Sprintf(g.t("prompt.ontology.additionalContext", nil), additionalContext)
	}

	msg += g.t("prompt.ontology.instructions", nil)
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
		marker := g.t("prompt.ontology.contextTruncated", nil)
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
	DocumentIndex       int
	ChunkIndex          int
	TotalDocumentChunks int
	Text                string
}

// CollectDocumentChunks replica _collect_document_chunks.
func (g *Generator) CollectDocumentChunks(documentTexts []string) []ChunkMeta {
	allChunks := []ChunkMeta{}
	for docIndex, text := range documentTexts {
		docChunks := tex.SplitTextIntoChunks(text, g.LongTextChunkSize, g.LongTextChunkOverlap)
		for chunkIndex, chunk := range docChunks {
			allChunks = append(allChunks, ChunkMeta{
				DocumentIndex:       docIndex + 1,
				ChunkIndex:          chunkIndex + 1,
				TotalDocumentChunks: len(docChunks),
				Text:                chunk,
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
		fmt.Sprintf(g.t("prompt.ontology.chunkSummaryHeader", nil), originalLength, totalChunks),
		fmt.Sprintf(g.t("prompt.ontology.chunkSummaryIntro", nil), len(selectedChunks)),
	}
	for _, chunk := range selectedChunks {
		excerpt := g.excerptText(chunk.Text, excerptLimit)
		lines = append(lines, fmt.Sprintf(g.t("prompt.ontology.chunkHeader", nil),
			chunk.DocumentIndex, chunk.ChunkIndex, chunk.TotalDocumentChunks, excerpt))
	}
	return strings.Join(lines, "\n\n")
}

// ExcerptText replica _excerpt_text: mantiene inicio y fin del bloque.
// Se conserva como función por compatibilidad; usa el marcador en español.
func ExcerptText(text string, charLimit int) string {
	return excerptText(text, charLimit, "\n...(contenido central de este bloque omitido)...\n")
}

// excerptText es la variante localizada usada por RenderChunkedContext.
func (g *Generator) excerptText(text string, charLimit int) string {
	return excerptText(text, charLimit, g.t("prompt.ontology.excerptOmitted", nil))
}

func excerptText(text string, charLimit int, marker string) string {
	text = strings.TrimSpace(text)
	size := len([]rune(text))
	if size <= charLimit {
		return text
	}

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
