// Port de _validate_and_process y generate_python_code de
// backend/app/services/ontology_generator.py.
package ontology

import (
	"fmt"
	"strings"
)

// ValidateAndProcess replica _validate_and_process: normaliza nombres de
// entidad a PascalCase (con dedup), atributos (máx 10, fallback details),
// añade Person/Organization si faltan y recorta a MaxTypes; después
// normaliza aristas a UPPER_SNAKE_CASE, descarta las de solo nombre o con
// extremos no resolubles (antes no durante el cap), y las recorta a 10.
func ValidateAndProcess(result map[string]any) (map[string]any, error) {
	if result == nil {
		return nil, fmt.Errorf("Ontology result must be an object")
	}

	rawEntities, _ := result["entity_types"].([]any)
	rawEdges, _ := result["edge_types"].([]any)
	if _, ok := result["entity_types"].([]any); !ok {
		rawEntities = nil
	}
	if _, ok := result["edge_types"].([]any); !ok {
		rawEdges = nil
	}
	if _, ok := result["analysis_summary"].(string); !ok {
		result["analysis_summary"] = ""
	}

	// entryEntity replica el dict(entity) del Python: base para el dict nuevo.
	entryEntity := func(v any) (map[string]any, bool) {
		s, isStr := v.(string)
		if isStr {
			return map[string]any{"name": s}, true
		}
		if m, isMap := v.(map[string]any); isMap {
			cp := map[string]any{}
			for k, val := range m {
				cp[k] = val
			}
			return cp, true
		}
		return nil, false // logger.warning("Ignoring non-object ontology entity entry")
	}

	entityNameMap := map[string]string{}
	var processedEntities []map[string]any
	seenEntityNames := map[string]bool{}

	for _, rawEntity := range rawEntities {
		entity, ok := entryEntity(rawEntity)
		if !ok {
			continue
		}

		originalNameAny, _ := entity["name"]
		originalName, isStr := originalNameAny.(string)
		if !isStr || strings.TrimSpace(originalName) == "" {
			// logger.warning("Ignoring ontology entity without a usable name")
			continue
		}
		normalized := ToPascalCase(originalName)
		if normalized == "Unknown" {
			continue
		}
		if seenEntityNames[normalized] {
			// logger.warning("Duplicate entity type '%s' removed...")
			entityNameMap[originalName] = normalized
			entityNameMap[strings.ToLower(originalName)] = normalized
			continue
		}

		entity["name"] = normalized
		entity["attributes"] = attrsToAny(NormalizeAttributes(entity["attributes"]))
		if _, ok := entity["examples"].([]any); !ok {
			entity["examples"] = []any{}
		}
		description, isStr := entity["description"].(string)
		if !isStr || description == "" {
			description = fmt.Sprintf("A %s entity.", normalized)
		}
		if runes := []rune(description); len(runes) > 100 {
			description = string(runes[:97]) + "..."
		}
		entity["description"] = description

		seenEntityNames[normalized] = true
		processedEntities = append(processedEntities, entity)
		entityNameMap[originalName] = normalized
		entityNameMap[strings.ToLower(originalName)] = normalized
		entityNameMap[normalized] = normalized
		entityNameMap[strings.ToLower(normalized)] = normalized
	}
	if processedEntities == nil {
		processedEntities = []map[string]any{}
	}
	result["entity_types"] = entitiesToAny(processedEntities)

	// ===== Tipos de respaldo =====
	personFallback := map[string]any{
		"name":        "Person",
		"description": "Any individual person not fitting other specific person types.",
		"attributes": []any{
			map[string]any{"name": "full_name", "type": "text", "description": "Full name of the person"},
			map[string]any{"name": "role", "type": "text", "description": "Role or occupation"},
		},
		"examples": []any{"ordinary citizen", "anonymous netizen"},
	}
	organizationFallback := map[string]any{
		"name":        "Organization",
		"description": "Any organization not fitting other specific organization types.",
		"attributes": []any{
			map[string]any{"name": "org_name", "type": "text", "description": "Name of the organization"},
			map[string]any{"name": "org_type", "type": "text", "description": "Type of organization"},
		},
		"examples": []any{"small business", "community group"},
	}

	hasPerson := false
	hasOrganization := false
	for _, e := range processedEntities {
		switch n := e["name"].(type) {
		case string:
			switch n {
			case "Person":
				hasPerson = true
			case "Organization":
				hasOrganization = true
			}
		}
	}

	var fallbacksToAdd []map[string]any
	if !hasPerson {
		fallbacksToAdd = append(fallbacksToAdd, personFallback)
	}
	if !hasOrganization {
		fallbacksToAdd = append(fallbacksToAdd, organizationFallback)
	}

	if len(fallbacksToAdd) > 0 {
		currentCount := len(processedEntities)
		neededSlots := len(fallbacksToAdd)
		if currentCount+neededSlots > MaxTypes {
			toRemove := currentCount + neededSlots - MaxTypes
			processedEntities = processedEntities[:currentCount-toRemove]
		}
		for _, fb := range fallbacksToAdd {
			processedEntities = append(processedEntities, fb)
		}
	}
	// Asegurar finalmente que no se supere el límite (programación defensiva)
	if len(processedEntities) > MaxTypes {
		processedEntities = processedEntities[:MaxTypes]
	}
	result["entity_types"] = entitiesToAny(processedEntities)

	// Resolve edge endpoints only after entity fallback/capping.
	validEntityNames := map[string]bool{}
	for _, e := range processedEntities {
		if n, ok := e["name"].(string); ok {
			validEntityNames[n] = true
		}
	}
	for name := range validEntityNames {
		entityNameMap[name] = name
		entityNameMap[strings.ToLower(name)] = name
	}

	resolveEntityName := func(value string) *string {
		stripped := strings.TrimSpace(value)
		if stripped == "Entity" {
			return &stripped
		}
		mapped := entityNameMap[stripped]
		if mapped == "" {
			mapped = entityNameMap[strings.ToLower(stripped)]
		}
		if mapped != "" && validEntityNames[mapped] {
			return &mapped
		}
		pascalName := ToPascalCase(stripped)
		if validEntityNames[pascalName] {
			return &pascalName
		}
		return nil
	}

	var processedEdges []map[string]any
	seenEdgeNames := map[string]bool{}

edgeLoop:
	for _, rawEdge := range rawEdges {
		var edge map[string]any
		switch rawEdge.(type) {
		case string:
			// A bare edge name has no endpoints and cannot be installed in
			// Zep safely. Ignore it instead of inventing a relationship.
			// logger.warning(...)
			continue
		case map[string]any:
			origin := rawEdge.(map[string]any)
			edge = map[string]any{}
			for k, val := range origin {
				edge[k] = val
			}
		default:
			// logger.warning("Ignoring non-object ontology edge entry")
			continue
		}

		originalAny, _ := edge["name"]
		original, isStr := originalAny.(string)
		if !isStr || strings.TrimSpace(original) == "" {
			// logger.warning("Ignoring ontology edge without a usable name")
			continue
		}
		normalized := ToUpperSnakeCase(original)
		if normalized == "UNKNOWN" || seenEdgeNames[normalized] {
			// logger.warning(...) para duplicados
			continue
		}
		edge["name"] = normalized

		var normalizedTargets []any
		for _, st := range NormalizeSourceTargets(edge["source_targets"], 0) {
			source := resolveEntityName(st["source"])
			target := resolveEntityName(st["target"])
			if source != nil && target != nil {
				normalizedTargets = append(normalizedTargets, map[string]any{
					"source": *source, "target": *target,
				})
			}
		}
		edge["source_targets"] = anyListOrEmpty(normalizedTargets)
		edge["attributes"] = attrsToAny(NormalizeAttributes(edge["attributes"]))
		description, isStr := edge["description"].(string)
		if !isStr || description == "" {
			description = fmt.Sprintf("A %s relationship.", normalized)
		}
		if runes := []rune(description); len(runes) > 100 {
			description = string(runes[:97]) + "..."
		}
		edge["description"] = description

		seenEdgeNames[normalized] = true
		processedEdges = append(processedEdges, edge)
		if len(processedEdges) == MaxTypes {
			break edgeLoop
		}
	}
	if processedEdges == nil {
		processedEdges = []map[string]any{}
	}
	result["edge_types"] = edgesToAny(processedEdges)

	return result, nil
}

// GeneratePythonCode replica generate_python_code: serializa la ontología a
// un módulo Python (pydantic + zep_cloud) cacheable por consumers Python.
// En el port Go se mantiene el formato byte-idéntico para no romper tooling.
func (g *Generator) GeneratePythonCode(ontology map[string]any) string {
	codeLines := []string{
		`"""`,
		`Definición de tipos de entidad personalizados`,
		`Generado automáticamente por MiroFish para simulación de opinión pública en redes sociales`,
		`"""`,
		``,
		`from pydantic import Field`,
		`from zep_cloud.external_clients.ontology import EntityModel, EntityText, EdgeModel`,
		``,
		``,
		`# ============== Definición de tipos de entidad ==============`,
		``,
	}

	for _, rawEntity := range anyList(ontology["entity_types"]) {
		entity, ok := rawEntity.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entity["name"].(string)
		desc := ""
		if s, ok := entity["description"].(string); ok {
			desc = s
		} else {
			desc = fmt.Sprintf("A %s entity.", name)
		}
		codeLines = append(codeLines, fmt.Sprintf("class %s(EntityModel):", name))
		codeLines = append(codeLines, fmt.Sprintf("    %q", desc))

		attrs, _ := entity["attributes"].([]any)
		if len(attrs) > 0 {
			for _, rawAttr := range attrs {
				attr, ok := rawAttr.(map[string]any)
				if !ok {
					continue
				}
				attrName, _ := attr["name"].(string)
				attrDesc, ok := attr["description"].(string)
				if !ok {
					attrDesc = attrName
				}
				codeLines = append(codeLines, fmt.Sprintf("    %s: EntityText = Field(", attrName))
				codeLines = append(codeLines, fmt.Sprintf("        description=\"%s\",", attrDesc))
				codeLines = append(codeLines, fmt.Sprintf("        default=None"))
				codeLines = append(codeLines, fmt.Sprintf("    )"))
			}
		} else {
			codeLines = append(codeLines, "    pass")
		}
		codeLines = append(codeLines, "", "")
	}

	codeLines = append(codeLines, "# ============== Definición de tipos de relación ==============")
	codeLines = append(codeLines, "")

	for _, rawEdge := range anyList(ontology["edge_types"]) {
		edge, ok := rawEdge.(map[string]any)
		if !ok {
			continue
		}
		name, _ := edge["name"].(string)
		className := edgeClassName(name)
		desc := ""
		if s, ok := edge["description"].(string); ok {
			desc = s
		} else {
			desc = fmt.Sprintf("A %s relationship.", name)
		}
		codeLines = append(codeLines, fmt.Sprintf("class %s(EdgeModel):", className))
		codeLines = append(codeLines, fmt.Sprintf("    %q", desc))

		attrs, _ := edge["attributes"].([]any)
		if len(attrs) > 0 {
			for _, rawAttr := range attrs {
				attr, ok := rawAttr.(map[string]any)
				if !ok {
					continue
				}
				attrName, _ := attr["name"].(string)
				attrDesc, ok := attr["description"].(string)
				if !ok {
					attrDesc = attrName
				}
				codeLines = append(codeLines, fmt.Sprintf("    %s: EntityText = Field(", attrName))
				codeLines = append(codeLines, fmt.Sprintf("        description=\"%s\",", attrDesc))
				codeLines = append(codeLines, fmt.Sprintf("        default=None"))
				codeLines = append(codeLines, fmt.Sprintf("    )"))
			}
		} else {
			codeLines = append(codeLines, "    pass")
		}
		codeLines = append(codeLines, "", "")
	}

	// Generar diccionario de tipos
	codeLines = append(codeLines, "# ============== Configuración de tipos ==============")
	codeLines = append(codeLines, "")
	codeLines = append(codeLines, "ENTITY_TYPES = {")
	for _, rawEntity := range anyList(ontology["entity_types"]) {
		entity, ok := rawEntity.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entity["name"].(string)
		codeLines = append(codeLines, fmt.Sprintf("    \"%s\": %s,", name, name))
	}
	codeLines = append(codeLines, "}")
	codeLines = append(codeLines, "")
	codeLines = append(codeLines, "EDGE_TYPES = {")
	for _, rawEdge := range anyList(ontology["edge_types"]) {
		edge, ok := rawEdge.(map[string]any)
		if !ok {
			continue
		}
		name, _ := edge["name"].(string)
		codeLines = append(codeLines, fmt.Sprintf("    \"%s\": %s,", name, edgeClassName(name)))
	}
	codeLines = append(codeLines, "}")
	codeLines = append(codeLines, "")

	// Mapeo de source_targets
	codeLines = append(codeLines, "EDGE_SOURCE_TARGETS = {")
	for _, rawEdge := range anyList(ontology["edge_types"]) {
		edge, ok := rawEdge.(map[string]any)
		if !ok {
			continue
		}
		name, _ := edge["name"].(string)
		sourceTargets, _ := edge["source_targets"].([]any)
		if len(sourceTargets) > 0 {
			stParts := []string{}
			for _, rawSt := range sourceTargets {
				st, ok := rawSt.(map[string]any)
				if !ok {
					continue
				}
				source := "Entity"
				if s, ok := st["source"].(string); ok && s != "" {
					source = s
				}
				target := "Entity"
				if s, ok := st["target"].(string); ok && s != "" {
					target = s
				}
				stParts = append(stParts,
					fmt.Sprintf("{\"source\": \"%s\", \"target\": \"%s\"}", source, target))
			}
			codeLines = append(codeLines,
				fmt.Sprintf("    \"%s\": [%s],", name, strings.Join(stParts, ", ")))
		}
	}
	codeLines = append(codeLines, "}")

	return strings.Join(codeLines, "\n")
}

// edgeClassName replica ''.join(word.capitalize() for word in name.split('_')).
func edgeClassName(name string) string {
	words := strings.Split(name, "_")
	out := ""
	for _, w := range words {
		if w == "" {
			continue
		}
		out += pythonCapitalize(w)
	}
	return out
}

// pythonCapitalize replica str.capitalize(): primera mayúscula, resto en
// minúscula.
func pythonCapitalize(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + strings.ToLower(string(r[1:]))
}

// ===== helpers de conversión entre []map[string]any y []any =====

func entitiesToAny(entities []map[string]any) []any {
	out := []any{}
	for _, e := range entities {
		out = append(out, e)
	}
	return out
}

func edgesToAny(edges []map[string]any) []any {
	out := []any{}
	for _, e := range edges {
		out = append(out, e)
	}
	return out
}

func attrsToAny(attrs []map[string]any) []any {
	out := []any{}
	for _, a := range attrs {
		out = append(out, a)
	}
	return out
}

func anyList(v any) []any {
	list, _ := v.([]any)
	return list
}

func anyListOrEmpty(list []any) []any {
	if list == nil {
		return []any{}
	}
	return list
}

var _ = anyList // helper directo del paquete
