// Extracción de entidades/aristas vía LLM (reemplaza lo que hace Zep Cloud).
// Persistencia con dedup por nombre, igual que _persist_extraction y
// _persist_from_batch del Python.
package graph

import (
	"fmt"
	"strings"

	"mirofish/internal/llm"
)

// LLMFactory crea un cliente LLM nuevo por extracción (cada goroutine lo usa
// de forma independiente). Default: cliente de la configuración global.
var LLMFactory = func() (*llm.Client, error) { return llm.NewDefaultClient() }

// ExtractEntitiesFromText replica extract_entities_from_text: prompt y
// parámetros idénticos (temperature 0.2, max_attempts 2, texto truncado a
// 8000 chars). Devuelve {"entities": [...], "edges": [...]}.
func (d *Database) ExtractEntitiesFromText(text string, ontology map[string]any) map[string]any {
	entityTypes := []string{}
	if list, ok := ontology["entity_types"].([]any); ok {
		for _, raw := range list {
			if m, ok := raw.(map[string]any); ok {
				if name, ok := m["name"].(string); ok && name != "" {
					entityTypes = append(entityTypes, name)
				}
			}
		}
	}
	edgeTypes := []string{}
	if list, ok := ontology["edge_types"].([]any); ok {
		for _, raw := range list {
			if m, ok := raw.(map[string]any); ok {
				if name, ok := m["name"].(string); ok && name != "" {
					edgeTypes = append(edgeTypes, name)
				}
			}
		}
	}
	if len(entityTypes) == 0 {
		entityTypes = []string{"Entity"}
	}

	systemPrompt := fmt.Sprintf(`Eres un extractor de conocimiento. Analiza el texto y extrae entidades y relaciones.

Tipos de entidad permitidos: %s
Tipos de relación permitidos: %s

Devuelve SOLO un JSON valido con este formato:
{
  "entities": [
    {"name": "nombre", "type": "tipo_de_entidad", "summary": "descripcion breve", "attributes": {}}
  ],
  "edges": [
    {"source": "nombre_origen", "target": "nombre_destino", "name": "tipo_relacion", "fact": "descripcion de la relacion"}
  ]
}

Reglas:
- Las entidades deben ser personas, organizaciones, lugares, conceptos importantes
- Las relaciones deben conectar entidades existentes dentro de la lista de entidades
- Se preciso y conciso

Devuelve exclusivamente el JSON.`,
		strings.Join(entityTypes, ", "), strings.Join(edgeTypes, ", "))

	userPrompt := "Extrae entidades y relaciones del siguiente texto:\n\n" + truncRunes(text, 8000)

	client, err := LLMFactory()
	if err != nil {
		logger.Warn(fmt.Sprintf("LLM extraction failed: %v", err))
		return map[string]any{"entities": []any{}, "edges": []any{}}
	}
	result, err := client.ChatJSON([]llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, 0.2, nil, 2)
	if err != nil {
		logger.Warn(fmt.Sprintf("LLM extraction failed: %v", err))
		return map[string]any{"entities": []any{}, "edges": []any{}}
	}
	return result
}

// PersistExtraction replica _persist_extraction: guarda nodos/aristas con
// dedup por nombre resolviendo los ya existentes.
func (d *Database) PersistExtraction(graphID string, result map[string]any) error {
	entityIndex := map[string]string{}

	if ents, ok := orList(result["entities"]); ok {
		for _, raw := range ents {
			ent, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name := strings.TrimSpace(strOr(ent["name"]))
			if name == "" {
				continue
			}
			nodeUUID, err := d.AddNode(graphID, name, []string{"Entity", strOrDefault(ent["type"], "Entity")},
				strings.TrimSpace(strOr(ent["summary"])), anyMapOr(ent["attributes"]))
			if err != nil {
				return err
			}
			entityIndex[name] = nodeUUID
		}
	}

	if edges, ok := orList(result["edges"]); ok {
		for _, raw := range edges {
			edge, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			srcName := strings.TrimSpace(strOr(edge["source"]))
			tgtName := strings.TrimSpace(strOr(edge["target"]))

			srcUUID := entityIndex[srcName]
			if srcUUID == "" {
				if node, err := d.FindNodeByName(graphID, srcName); err == nil && node != nil {
					srcUUID = node.UUID
				}
			}
			tgtUUID := entityIndex[tgtName]
			if tgtUUID == "" {
				if node, err := d.FindNodeByName(graphID, tgtName); err == nil && node != nil {
					tgtUUID = node.UUID
				}
			}
			if srcUUID != "" && tgtUUID != "" {
				if _, err := d.AddEdge(graphID, srcUUID, tgtUUID,
					strOrDefault(edge["name"], "related_to"), strOr(edge["fact"]), nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ExtractFromEpisodeBatch replica extract_from_episode_batch: los textos se
// extraen en paralelo (ThreadPool de 6 en Python → goroutines acotadas aquí),
// se fusionan con dedup por nombre y se persisten de una vez.
func (d *Database) ExtractFromEpisodeBatch(graphID string, texts []string, ontology map[string]any, maxWorkers int) (int, int, error) {
	type extraction struct {
		entities []any
		edges    []any
		err      error
	}
	results := make([]extraction, len(texts))
	sem := make(chan struct{}, maxWorkers)
	done := make(chan int, len(texts))
	for i, t := range texts {
		go func(idx int, text string) {
			sem <- struct{}{}
			defer func() { <-sem; done <- idx }()
			r := d.ExtractEntitiesFromText(text, ontology)
			e := extraction{}
			if list, ok := orList(r["entities"]); ok {
				e.entities = list
			}
			if list, ok := orList(r["edges"]); ok {
				e.edges = list
			}
			results[idx] = e
		}(i, t)
	}
	for range texts {
		_ = <-done
	}

	allEntities := map[string]map[string]any{}
	var allEdges []map[string]any
	for _, res := range results {
		if res.err != nil {
			continue
		}
		for _, raw := range res.entities {
			ent, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name := strings.TrimSpace(strOr(ent["name"]))
			if name == "" {
				continue
			}
			if _, exists := allEntities[name]; !exists {
				allEntities[name] = ent
			}
		}
		for _, raw := range res.edges {
			edge, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if strOr(edge["source"]) != "" && strOr(edge["target"]) != "" {
				allEdges = append(allEdges, edge)
			}
		}
	}

	if err := d.PersistFromBatch(graphID, allEntities, allEdges); err != nil {
		return 0, 0, err
	}
	return len(allEntities), len(allEdges), nil
}

// PersistFromBatch replica _persist_from_batch.
func (d *Database) PersistFromBatch(graphID string, allEntities map[string]map[string]any, allEdges []map[string]any) error {
	nodeNameToUUID := map[string]string{}
	for name, ent := range allEntities {
		labels := []string{"Entity", strOrDefault(ent["type"], "Entity")}
		nodeUUID, err := d.AddNode(graphID, name, labels,
			strings.TrimSpace(strOr(ent["summary"])), anyMapOr(ent["attributes"]))
		if err != nil {
			return err
		}
		nodeNameToUUID[name] = nodeUUID
	}

	for _, edge := range allEdges {
		srcUUID := nodeNameToUUID[strOr(edge["source"])]
		if srcUUID == "" {
			if node, err := d.FindNodeByName(graphID, strOr(edge["source"])); err == nil && node != nil {
				srcUUID = node.UUID
			}
		}
		tgtUUID := nodeNameToUUID[strOr(edge["target"])]
		if tgtUUID == "" {
			if node, err := d.FindNodeByName(graphID, strOr(edge["target"])); err == nil && node != nil {
				tgtUUID = node.UUID
			}
		}
		if srcUUID != "" && tgtUUID != "" {
			if _, err := d.AddEdge(graphID, srcUUID, tgtUUID,
				strOrDefault(edge["name"], "related_to"), strOr(edge["fact"]), nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// ===== helpers =====

func strOr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func strOrDefault(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

func anyMapOr(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func orList(v any) ([]any, bool) {
	if list, ok := v.([]any); ok {
		return list, true
	}
	return nil, false
}

func truncRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
