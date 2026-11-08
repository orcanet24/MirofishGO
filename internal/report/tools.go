// Herramientas de recuperación del Report Agent (port de zep_tools.py).
//
// Sobre el grafo local MySQL: búsqueda léxica FULLTEXT/LIKE (sin embeddings), por
// lo que "semántica" aquí significa relevancia léxica, igual que el backend
// local de Python. Los formatos de texto son los que consume el LLM.
package report

import (
	"fmt"
	"sort"
	"strings"

	"mirofish/internal/graph"
	"mirofish/internal/llm"
)

// Tools replica ZepToolsService sobre el backend local.
type Tools struct {
	Client     *graph.Client
	LLMFactory func() (*llm.Client, error)

	// Interview genera entrevistas reales a agentes simulados (lo inyecta
	// httpx, que conoce el gestor de simulaciones y el LLM del usuario).
	Interview func(simulationID, topic string, maxAgents int) string
}

// NewTools crea el servicio de herramientas.
func NewTools(client *graph.Client) *Tools {
	return &Tools{Client: client}
}

func (t *Tools) llmClient() (*llm.Client, error) {
	if t.LLMFactory != nil {
		return t.LLMFactory()
	}
	return llm.NewDefaultClient()
}

// SearchResult replica SearchResult (facts/edges/nodes/query/total_count).
type SearchResult struct {
	Facts      []string
	Edges      []map[string]any
	Nodes      []map[string]any
	Query      string
	TotalCount int
}

// ToDict replica SearchResult.to_dict.
func (r *SearchResult) ToDict() map[string]any {
	return map[string]any{
		"facts":       r.Facts,
		"edges":       r.Edges,
		"nodes":       r.Nodes,
		"query":       r.Query,
		"total_count": r.TotalCount,
	}
}

// ToText replica SearchResult.to_text, con las secciones de aristas y nodos
// que el frontend parsea (mismos marcadores que el Python: español).
func (r *SearchResult) ToText() string {
	parts := []string{
		"Consulta de búsqueda: " + r.Query,
		fmt.Sprintf("Se encontraron %d resultados relevantes", r.TotalCount),
	}
	if len(r.Facts) > 0 {
		parts = append(parts, "\n### Hechos relevantes:")
		for i, fact := range r.Facts {
			parts = append(parts, fmt.Sprintf("%d. %s", i+1, fact))
		}
	}
	if len(r.Edges) > 0 {
		parts = append(parts, "\n### Aristas relevantes:")
		for i, e := range r.Edges {
			parts = append(parts, fmt.Sprintf("%d. %v", i+1, e))
		}
	}
	if len(r.Nodes) > 0 {
		parts = append(parts, "\n### Nodos relevantes:")
		for i, n := range r.Nodes {
			parts = append(parts, fmt.Sprintf("%d. %v", i+1, n))
		}
	}
	return strings.Join(parts, "\n")
}

// NodeDict convierte un nodo al dict del Python.
func NodeDict(n graph.LocalNode) map[string]any {
	labels := n.Labels
	if labels == nil {
		labels = []string{}
	}
	return map[string]any{
		"uuid":       n.UUID,
		"name":       n.Name,
		"labels":     labels,
		"summary":    n.Summary,
		"attributes": n.Attributes,
	}
}

// EdgeDict convierte una arista al dict del Python (con nombres de extremos).
func EdgeDict(e graph.LocalEdge, names map[string]string) map[string]any {
	var sourceName any
	if name, ok := names[e.SourceNodeUUID]; ok {
		sourceName = name
	}
	var targetName any
	if name, ok := names[e.TargetNodeUUID]; ok {
		targetName = name
	}
	return map[string]any{
		"uuid":             e.UUID,
		"name":             e.Name,
		"fact":             e.Fact,
		"source_node_uuid": e.SourceNodeUUID,
		"target_node_uuid": e.TargetNodeUUID,
		"source_node_name": sourceName,
		"target_node_name": targetName,
		"created_at":       derefStr(e.CreatedAt),
		"valid_at":         e.ValidAt,
		"invalid_at":       e.InvalidAt,
		"expired_at":       e.ExpiredAt,
	}
}

func derefStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// nodeNameMap indexa uuid → nombre (para enriquecer aristas).
func (t *Tools) nodeNameMap(graphID string) map[string]string {
	names := map[string]string{}
	nodes, err := t.Client.DB.GetAllNodes(graphID)
	if err != nil {
		return names
	}
	for _, n := range nodes {
		names[n.UUID] = n.Name
	}
	return names
}

// SearchGraph replica search_graph.
func (t *Tools) SearchGraph(graphID, query string, limit int, scope string) *SearchResult {
	if limit <= 0 {
		limit = 10
	}
	raw := t.Client.Search(graphID, query, limit, scope)
	names := t.nodeNameMap(graphID)

	facts := []string{}
	for _, edge := range raw.Edges {
		if edge.Fact != "" {
			facts = append(facts, edge.Fact)
		}
	}
	edges := []map[string]any{}
	for _, edge := range raw.Edges {
		edges = append(edges, EdgeDict(edge, names))
	}
	nodes := []map[string]any{}
	for _, node := range raw.Nodes {
		nodes = append(nodes, NodeDict(node))
	}
	return &SearchResult{
		Facts:      facts,
		Edges:      edges,
		Nodes:      nodes,
		Query:      query,
		TotalCount: len(facts),
	}
}

// quickSearch replica quick_search (scope edges).
func (t *Tools) QuickSearch(graphID, query string, limit int) *SearchResult {
	return t.SearchGraph(graphID, query, limit, "edges")
}

// PanoramaResult replica PanoramaResult.
type PanoramaResult struct {
	Query           string
	AllNodes        []map[string]any
	AllEdges        []map[string]any
	ActiveFacts     []string
	HistoricalFacts []string
	TotalNodes      int
	TotalEdges      int
	ActiveCount     int
	HistoricalCount int
}

// ToDict replica PanoramaResult.to_dict.
func (p *PanoramaResult) ToDict() map[string]any {
	return map[string]any{
		"query":            p.Query,
		"all_nodes":        p.AllNodes,
		"all_edges":        p.AllEdges,
		"active_facts":     p.ActiveFacts,
		"historical_facts": p.HistoricalFacts,
		"total_nodes":      p.TotalNodes,
		"total_edges":      p.TotalEdges,
		"active_count":     p.ActiveCount,
		"historical_count": p.HistoricalCount,
	}
}

// ToText replica PanoramaResult.to_text.
func (p *PanoramaResult) ToText() string {
	parts := []string{
		"## Resultados de búsqueda panorámica (vista de futuro completa)",
		"Consulta: " + p.Query,
		"\n### Estadísticas",
		fmt.Sprintf("- Nodos totales: %d", p.TotalNodes),
		fmt.Sprintf("- Aristas totales: %d", p.TotalEdges),
		fmt.Sprintf("- Hechos vigentes: %d", p.ActiveCount),
		fmt.Sprintf("- Hechos históricos/expirados: %d", p.HistoricalCount),
	}
	if len(p.ActiveFacts) > 0 {
		parts = append(parts, "\n### 【Hechos vigentes】(texto original de la simulación)")
		for i, fact := range p.ActiveFacts {
			parts = append(parts, fmt.Sprintf("%d. \"%s\"", i+1, fact))
		}
	}
	if len(p.HistoricalFacts) > 0 {
		parts = append(parts, "\n### 【Hechos históricos/expirados】(registro de la evolución)")
		for i, fact := range p.HistoricalFacts {
			parts = append(parts, fmt.Sprintf("%d. \"%s\"", i+1, fact))
		}
	}
	if len(p.AllNodes) > 0 {
		parts = append(parts, "\n### 【Entidades implicadas】")
		for _, node := range p.AllNodes {
			parts = append(parts, fmt.Sprintf("- **%s** (%s)", str(node["name"]), entityTypeOf(node)))
		}
	}
	return strings.Join(parts, "\n")
}

// entityTypeOf replica el "primer label que no es Entity/Node".
func entityTypeOf(node map[string]any) string {
	labels, _ := node["labels"].([]string)
	for _, l := range labels {
		if l != "Entity" && l != "Node" {
			return l
		}
	}
	if raw, ok := node["labels"].([]any); ok {
		for _, item := range raw {
			if l, ok := item.(string); ok && l != "Entity" && l != "Node" {
				return l
			}
		}
	}
	return "Entidad"
}

// PanoramaSearch replica panorama_search.
func (t *Tools) PanoramaSearch(graphID, query string, includeExpired bool, limit int) *PanoramaResult {
	if limit <= 0 {
		limit = 50
	}
	result := &PanoramaResult{Query: query, AllNodes: []map[string]any{}, AllEdges: []map[string]any{}}

	nodes, err := t.Client.DB.GetAllNodes(graphID)
	if err == nil {
		for _, n := range nodes {
			result.AllNodes = append(result.AllNodes, NodeDict(n))
		}
	}
	names := map[string]string{}
	for _, n := range nodes {
		names[n.UUID] = n.Name
	}
	result.TotalNodes = len(result.AllNodes)

	edges, err := t.Client.DB.GetAllEdges(graphID)
	if err == nil {
		for _, e := range edges {
			result.AllEdges = append(result.AllEdges, EdgeDict(e, names))
		}
	}
	result.TotalEdges = len(result.AllEdges)

	active := []string{}
	historical := []string{}
	for _, e := range edges {
		if e.Fact == "" {
			continue
		}
		if e.ExpiredAt != nil || e.InvalidAt != nil {
			validAt := "desconocido"
			if e.ValidAt != nil {
				validAt = *e.ValidAt
			}
			invalidAt := "desconocido"
			if e.InvalidAt != nil {
				invalidAt = *e.InvalidAt
			} else if e.ExpiredAt != nil {
				invalidAt = *e.ExpiredAt
			}
			historical = append(historical, fmt.Sprintf("[%s - %s] %s", validAt, invalidAt, e.Fact))
		} else {
			active = append(active, e.Fact)
		}
	}

	sortByRelevance(active, query)
	sortByRelevance(historical, query)
	result.ActiveCount = len(active)
	result.HistoricalCount = len(historical)
	if len(active) > limit {
		active = active[:limit]
	}
	if includeExpired {
		if len(historical) > limit {
			historical = historical[:limit]
		}
		result.HistoricalFacts = historical
	} else {
		result.HistoricalFacts = []string{}
	}
	result.ActiveFacts = active
	return result
}

// sortByRelevance replica el sort por coincidencia de la query.
func sortByRelevance(facts []string, query string) {
	q := strings.ToLower(query)
	keywords := []string{}
	for _, w := range strings.FieldsFunc(q, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if len([]rune(w)) > 1 {
			keywords = append(keywords, w)
		}
	}
	score := func(fact string) int {
		lf := strings.ToLower(fact)
		s := 0
		if q != "" && strings.Contains(lf, q) {
			s += 100
		}
		for _, kw := range keywords {
			if strings.Contains(lf, kw) {
				s += 10
			}
		}
		return s
	}
	sort.SliceStable(facts, func(i, j int) bool { return score(facts[i]) > score(facts[j]) })
}

// InsightResult replica InsightForgeResult.
type InsightResult struct {
	Query                 string
	SimulationRequirement string
	SubQueries            []string
	SemanticFacts         []string
	EntityInsights        []map[string]any
	RelationshipChains    []string
	TotalFacts            int
	TotalEntities         int
	TotalRelationships    int
}

// ToDict replica InsightForgeResult.to_dict.
func (r *InsightResult) ToDict() map[string]any {
	return map[string]any{
		"query":                  r.Query,
		"simulation_requirement": r.SimulationRequirement,
		"sub_queries":            r.SubQueries,
		"semantic_facts":         r.SemanticFacts,
		"entity_insights":        r.EntityInsights,
		"relationship_chains":    r.RelationshipChains,
		"total_facts":            r.TotalFacts,
		"total_entities":         r.TotalEntities,
		"total_relationships":    r.TotalRelationships,
	}
}

// ToText replica InsightForgeResult.to_text.
func (r *InsightResult) ToText() string {
	parts := []string{
		"## Análisis profundo de predicción de futuro",
		"Pregunta analizada: " + r.Query,
		"Escenario de predicción: " + r.SimulationRequirement,
		"\n### Estadísticas de predicción",
		fmt.Sprintf("- Hechos predictivos relevantes: %d", r.TotalFacts),
		fmt.Sprintf("- Entidades implicadas: %d", r.TotalEntities),
		fmt.Sprintf("- Cadenas de relación: %d", r.TotalRelationships),
	}
	if len(r.SubQueries) > 0 {
		parts = append(parts, "\n### Subpreguntas analizadas")
		for i, sq := range r.SubQueries {
			parts = append(parts, fmt.Sprintf("%d. %s", i+1, sq))
		}
	}
	if len(r.SemanticFacts) > 0 {
		parts = append(parts, "\n### 【Hechos clave】(cita estos textos originales en el informe)")
		for i, fact := range r.SemanticFacts {
			parts = append(parts, fmt.Sprintf("%d. \"%s\"", i+1, fact))
		}
	}
	if len(r.EntityInsights) > 0 {
		parts = append(parts, "\n### 【Entidades principales】")
		for _, entity := range r.EntityInsights {
			parts = append(parts, fmt.Sprintf("- **%s** (%s)", str(entity["name"]), str(entity["type"])))
			if summary := str(entity["summary"]); summary != "" {
				parts = append(parts, "  Resumen: \""+summary+"\"")
			}
			if facts, ok := entity["related_facts"].([]string); ok && len(facts) > 0 {
				parts = append(parts, fmt.Sprintf("  Hechos relacionados: %d", len(facts)))
			}
		}
	}
	if len(r.RelationshipChains) > 0 {
		parts = append(parts, "\n### 【Cadenas de relación】")
		for _, chain := range r.RelationshipChains {
			parts = append(parts, "- "+chain)
		}
	}
	return strings.Join(parts, "\n")
}

// InsightForge replica insight_forge: descompone la pregunta con el LLM,
// busca cada sub-pregunta y agrega hechos, entidades y cadenas de relación.
func (t *Tools) InsightForge(graphID, query, simulationRequirement, reportContext string, maxSubQueries int) *InsightResult {
	if maxSubQueries <= 0 {
		maxSubQueries = 5
	}
	result := &InsightResult{
		Query:                 query,
		SimulationRequirement: simulationRequirement,
		SubQueries:            t.generateSubQueries(query, simulationRequirement, reportContext, maxSubQueries),
	}

	seen := map[string]bool{}
	edges := []map[string]any{}
	collect := func(res *SearchResult) {
		for _, fact := range res.Facts {
			if fact != "" && !seen[fact] {
				seen[fact] = true
				result.SemanticFacts = append(result.SemanticFacts, fact)
			}
		}
		edges = append(edges, res.Edges...)
	}

	for _, subQuery := range result.SubQueries {
		collect(t.SearchGraph(graphID, subQuery, 15, "edges"))
	}
	main := t.SearchGraph(graphID, query, 15, "both")
	collect(main)

	// Entidades: las que aparecen en los resultados de la búsqueda principal.
	seenEntities := map[string]bool{}
	for _, node := range main.Nodes {
		name := str(node["name"])
		if name == "" || seenEntities[name] {
			continue
		}
		seenEntities[name] = true
		related := []string{}
		for _, edge := range edges {
			if str(edge["source_node_name"]) == name || str(edge["target_node_name"]) == name {
				if fact := str(edge["fact"]); fact != "" && len(related) < 5 {
					related = append(related, fact)
				}
			}
		}
		result.EntityInsights = append(result.EntityInsights, map[string]any{
			"name":          name,
			"type":          entityTypeOf(node),
			"summary":       str(node["summary"]),
			"related_facts": related,
		})
		if len(result.EntityInsights) >= 10 {
			break
		}
	}

	// Cadenas de relación (máx 15, sin duplicados).
	seenChains := map[string]bool{}
	for _, edge := range edges {
		src := str(edge["source_node_name"])
		tgt := str(edge["target_node_name"])
		if src == "" || tgt == "" {
			continue
		}
		chain := fmt.Sprintf("%s --[%s]--> %s", src, str(edge["name"]), tgt)
		if seenChains[chain] {
			continue
		}
		seenChains[chain] = true
		result.RelationshipChains = append(result.RelationshipChains, chain)
		if len(result.RelationshipChains) >= 15 {
			break
		}
	}

	result.TotalFacts = len(result.SemanticFacts)
	result.TotalEntities = len(result.EntityInsights)
	result.TotalRelationships = len(result.RelationshipChains)
	return result
}

// generateSubQueries replica _generate_sub_queries con fallback local.
func (t *Tools) generateSubQueries(query, simulationRequirement, reportContext string, maxQueries int) []string {
	systemPrompt := "Eres un experto en análisis de problemas. Tu tarea es descomponer un problema complejo en varios subproblemas que puedan observarse de forma independiente en el mundo simulado.\n\n" +
		"Requisitos:\n1. Cada subproblema debe ser lo bastante concreto para encontrar comportamientos o eventos de Agentes relevantes en el mundo simulado\n" +
		"2. Los subproblemas deben cubrir distintas dimensiones del problema original\n3. Devuelve un JSON con este formato: {\"sub_queries\": [\"subproblema1\", ...]}"

	contextLine := ""
	if reportContext != "" {
		runes := []rune(reportContext)
		if len(runes) > 500 {
			reportContext = string(runes[:500])
		}
		contextLine = "Contexto del informe: " + reportContext + "\n\n"
	}
	userPrompt := fmt.Sprintf("Contexto de la necesidad de simulación:\n%s\n\n%sDescompón la siguiente pregunta en %d subproblemas:\n%s\n\nDevuelve la lista de subproblemas en formato JSON.",
		simulationRequirement, contextLine, maxQueries, query)

	if client, err := t.llmClient(); err == nil {
		if resp, err := client.ChatJSON([]llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		}, 0.3, nil, 2); err == nil {
			if list, ok := resp["sub_queries"].([]any); ok {
				out := []string{}
				for _, item := range list {
					if s := str(item); s != "" {
						out = append(out, s)
					}
					if len(out) >= maxQueries {
						break
					}
				}
				if len(out) > 0 {
					return out
				}
			}
		}
	}

	fallback := []string{
		query,
		query + ": principales participantes",
		query + ": causas y efectos",
		query + ": proceso de desarrollo",
	}
	if len(fallback) > maxQueries {
		fallback = fallback[:maxQueries]
	}
	return fallback
}

// GraphStatistics replica get_graph_statistics.
func (t *Tools) GraphStatistics(graphID string) map[string]any {
	nodes, _ := t.Client.DB.GetAllNodes(graphID)
	edges, _ := t.Client.DB.GetAllEdges(graphID)

	entityTypes := map[string]int{}
	for _, n := range nodes {
		for _, label := range n.Labels {
			if label != "Entity" && label != "Node" {
				entityTypes[label]++
			}
		}
	}
	relationTypes := map[string]int{}
	for _, e := range edges {
		relationTypes[e.Name]++
	}
	return map[string]any{
		"graph_id":       graphID,
		"total_nodes":    len(nodes),
		"total_edges":    len(edges),
		"entity_types":   entityTypes,
		"relation_types": relationTypes,
	}
}

// SimulationContext replica get_simulation_context.
func (t *Tools) SimulationContext(graphID, simulationRequirement string, limit int) map[string]any {
	if limit <= 0 {
		limit = 30
	}
	search := t.SearchGraph(graphID, simulationRequirement, limit, "both")
	stats := t.GraphStatistics(graphID)
	nodes, _ := t.Client.DB.GetAllNodes(graphID)

	entities := []map[string]any{}
	for _, n := range nodes {
		custom := []string{}
		for _, l := range n.Labels {
			if l != "Entity" && l != "Node" {
				custom = append(custom, l)
			}
		}
		if len(custom) == 0 {
			continue
		}
		entities = append(entities, map[string]any{
			"name":    n.Name,
			"type":    custom[0],
			"summary": n.Summary,
		})
	}
	limited := entities
	if len(limited) > limit {
		limited = limited[:limit]
	}
	return map[string]any{
		"simulation_requirement": simulationRequirement,
		"related_facts":          search.Facts,
		"graph_statistics":       stats,
		"entities":               limited,
		"total_entities":         len(entities),
	}
}

// EntitiesByType replica get_entities_by_type.
func (t *Tools) EntitiesByType(graphID, entityType string) []map[string]any {
	nodes, _ := t.Client.DB.GetAllNodes(graphID)
	out := []map[string]any{}
	for _, n := range nodes {
		for _, label := range n.Labels {
			if label == entityType {
				out = append(out, NodeDict(n))
				break
			}
		}
	}
	return out
}

// EntitySummary replica get_entity_summary (nil si no existe).
func (t *Tools) EntitySummary(graphID, entityName string) map[string]any {
	nodes, _ := t.Client.DB.GetAllNodes(graphID)
	for _, n := range nodes {
		if n.Name != entityName {
			continue
		}
		edges, _ := t.Client.DB.GetEdgesForNode(graphID, n.UUID)
		names := t.nodeNameMap(graphID)
		related := []map[string]any{}
		for _, e := range edges {
			related = append(related, EdgeDict(e, names))
		}
		dict := NodeDict(n)
		dict["related_edges"] = related
		return dict
	}
	return nil
}

// InterviewAgents usa el motor de entrevistas en proceso (oasis.InterviewAgents)
// cuando httpx inyecta la función; si no, informa de que no está disponible.
func (t *Tools) InterviewAgents(simulationID, interviewRequirement string, maxAgents int) string {
	if t.Interview != nil {
		return t.Interview(simulationID, interviewRequirement, maxAgents)
	}
	return "Error al ejecutar la herramienta: interview_agents no está disponible en este contexto. " +
		"Usa insight_forge, panorama_search o quick_search para basar el informe en el grafo de conocimiento."
}
