// Package simx — port de los servicios de simulación de backend/app/services
// (entity reader, profile generator, config generator, manager, ipc, runner).
package simx

import (
	"errors"
	"fmt"

	"mirofish/internal/graph"
)

// EntityNode replica el dataclass EntityNode de zep_entity_reader.py.
type EntityNode struct {
	UUID       string         `json:"uuid"`
	Name       string         `json:"name"`
	Labels     []string       `json:"labels"`
	Summary    string         `json:"summary"`
	Attributes map[string]any `json:"attributes"`

	RelatedEdges []map[string]any `json:"related_edges"`
	RelatedNodes []map[string]any `json:"related_nodes"`
}

// GetEntityType replica get_entity_type (primer label que no es Entity/Node).
func (e *EntityNode) GetEntityType() *string {
	for _, label := range e.Labels {
		if label != "Entity" && label != "Node" {
			l := label
			return &l
		}
	}
	return nil
}

// FilteredEntities replica el dataclass FilteredEntities.
type FilteredEntities struct {
	Entities      []EntityNode
	EntityTypes   []string
	TotalCount    int
	FilteredCount int
}

// EntityReader replica ZepEntityReader sobre el backend local.
type EntityReader struct {
	Client *graph.Client
}

// NewEntityReader crea el lector sobre un cliente de grafo local.
func NewEntityReader(client *graph.Client) *EntityReader { return &EntityReader{Client: client} }

// GetAllNodes replica get_all_nodes.
func (r *EntityReader) GetAllNodes(graphID string) ([]map[string]any, error) {
	nodes, err := r.Client.DB.GetAllNodes(graphID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, n := range nodes {
		out = append(out, map[string]any{
			"uuid":       n.UUID,
			"name":       n.Name,
			"labels":     n.Labels,
			"summary":    n.Summary,
			"attributes": n.Attributes,
		})
	}
	return out, nil
}

// GetAllEdges replica get_all_edges.
func (r *EntityReader) GetAllEdges(graphID string) ([]map[string]any, error) {
	edges, err := r.Client.DB.GetAllEdges(graphID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, e := range edges {
		out = append(out, map[string]any{
			"uuid":              e.UUID,
			"name":              e.Name,
			"fact":              e.Fact,
			"source_node_uuid":  e.SourceNodeUUID,
			"target_node_uuid":  e.TargetNodeUUID,
			"attributes":        e.Attributes,
		})
	}
	return out, nil
}

// GetNodeEdges replica get_node_edges: con graph_id filtra el grafo completo
// (bidireccional, como el Python).
func (r *EntityReader) GetNodeEdges(nodeUUID, graphID string) ([]map[string]any, error) {
	if graphID != "" {
		all, err := r.GetAllEdges(graphID)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, edge := range all {
			if edge["source_node_uuid"] == nodeUUID || edge["target_node_uuid"] == nodeUUID {
				out = append(out, edge)
			}
		}
		return out, nil
	}
	edges, err := r.Client.DB.GetEdgesForNode("", nodeUUID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, e := range edges {
		out = append(out, map[string]any{
			"uuid":              e.UUID,
			"name":              e.Name,
			"fact":              e.Fact,
			"source_node_uuid":  e.SourceNodeUUID,
			"target_node_uuid":  e.TargetNodeUUID,
			"attributes":        e.Attributes,
		})
	}
	return out, nil
}

// FilterDefinedEntities replica filter_defined_entities.
func (r *EntityReader) FilterDefinedEntities(graphID string, definedEntityTypes []string, enrichWithEdges bool) (*FilteredEntities, error) {
	allNodes, err := r.GetAllNodes(graphID)
	if err != nil {
		return nil, err
	}
	totalCount := len(allNodes)

	allEdges := []map[string]any{}
	if enrichWithEdges {
		if allEdges, err = r.GetAllEdges(graphID); err != nil {
			return nil, err
		}
	}

	nodeMap := map[string]map[string]any{}
	for _, n := range allNodes {
		nodeMap[strOr(n["uuid"])] = n
	}

	defined := map[string]bool{}
	for _, t := range definedEntityTypes {
		defined[t] = true
	}

	filtered := []EntityNode{}
	entityTypesFound := map[string]bool{}

	for _, node := range allNodes {
		labels := stringList(node["labels"])
		customLabels := []string{}
		for _, l := range labels {
			if l != "Entity" && l != "Node" {
				customLabels = append(customLabels, l)
			}
		}
		if len(customLabels) == 0 {
			continue
		}

		var entityType string
		if len(defined) > 0 {
			for _, l := range customLabels {
				if defined[l] {
					entityType = l
					break
				}
			}
			if entityType == "" {
				continue
			}
		} else {
			entityType = customLabels[0]
		}
		entityTypesFound[entityType] = true

		entity := EntityNode{
			UUID:         strOr(node["uuid"]),
			Name:         strOr(node["name"]),
			Labels:       labels,
			Summary:      strOr(node["summary"]),
			Attributes:   anyMap(node["attributes"]),
			RelatedEdges: []map[string]any{},
			RelatedNodes: []map[string]any{},
		}

		if enrichWithEdges {
			relatedEdges := []map[string]any{}
			relatedUUIDs := map[string]bool{}
			for _, edge := range allEdges {
				src := strOr(edge["source_node_uuid"])
				tgt := strOr(edge["target_node_uuid"])
				if src == entity.UUID {
					relatedEdges = append(relatedEdges, map[string]any{
						"direction":        "outgoing",
						"edge_name":        strOr(edge["name"]),
						"fact":             strOr(edge["fact"]),
						"target_node_uuid": tgt,
					})
					relatedUUIDs[tgt] = true
				} else if tgt == entity.UUID {
					relatedEdges = append(relatedEdges, map[string]any{
						"direction":        "incoming",
						"edge_name":        strOr(edge["name"]),
						"fact":             strOr(edge["fact"]),
						"source_node_uuid": src,
					})
					relatedUUIDs[src] = true
				}
			}
			entity.RelatedEdges = relatedEdges

			relatedNodes := []map[string]any{}
			for uuid := range relatedUUIDs {
				if n, ok := nodeMap[uuid]; ok {
					relatedNodes = append(relatedNodes, map[string]any{
						"uuid":    strOr(n["uuid"]),
						"name":    strOr(n["name"]),
						"labels":  stringList(n["labels"]),
						"summary": strOr(n["summary"]),
					})
				}
			}
			entity.RelatedNodes = relatedNodes
		}

		filtered = append(filtered, entity)
	}

	types := []string{}
	for t := range entityTypesFound {
		types = append(types, t)
	}
	return &FilteredEntities{
		Entities:      filtered,
		EntityTypes:   types,
		TotalCount:    totalCount,
		FilteredCount: len(filtered),
	}, nil
}

// GetEntityWithContext replica get_entity_with_context.
func (r *EntityReader) GetEntityWithContext(graphID, entityUUID string) (*EntityNode, error) {
	node, err := r.Client.NodeGet(entityUUID)
	if err != nil {
		var notFound *graph.NotFoundError
		if errors.As(err, &notFound) {
			return nil, nil
		}
		return nil, err
	}
	if node == nil {
		return nil, nil
	}

	edges, err := r.GetNodeEdges(entityUUID, graphID)
	if err != nil {
		return nil, err
	}
	allNodes, err := r.GetAllNodes(graphID)
	if err != nil {
		return nil, err
	}
	nodeMap := map[string]map[string]any{}
	for _, n := range allNodes {
		nodeMap[strOr(n["uuid"])] = n
	}

	relatedEdges := []map[string]any{}
	relatedUUIDs := map[string]bool{}
	for _, edge := range edges {
		src := strOr(edge["source_node_uuid"])
		tgt := strOr(edge["target_node_uuid"])
		if src == entityUUID {
			relatedEdges = append(relatedEdges, map[string]any{
				"direction":        "outgoing",
				"edge_name":        strOr(edge["name"]),
				"fact":             strOr(edge["fact"]),
				"target_node_uuid": tgt,
			})
			relatedUUIDs[tgt] = true
		} else {
			relatedEdges = append(relatedEdges, map[string]any{
				"direction":        "incoming",
				"edge_name":        strOr(edge["name"]),
				"fact":             strOr(edge["fact"]),
				"source_node_uuid": src,
			})
			relatedUUIDs[src] = true
		}
	}

	relatedNodes := []map[string]any{}
	for uuid := range relatedUUIDs {
		if n, ok := nodeMap[uuid]; ok {
			relatedNodes = append(relatedNodes, map[string]any{
				"uuid":    strOr(n["uuid"]),
				"name":    strOr(n["name"]),
				"labels":  stringList(n["labels"]),
				"summary": strOr(n["summary"]),
			})
		}
	}

	return &EntityNode{
		UUID:         node.UUID,
		Name:         node.Name,
		Labels:       node.Labels,
		Summary:      node.Summary,
		Attributes:   node.Attributes,
		RelatedEdges: relatedEdges,
		RelatedNodes: relatedNodes,
	}, nil
}

// GetEntitiesByType replica get_entities_by_type.
func (r *EntityReader) GetEntitiesByType(graphID, entityType string, enrichWithEdges bool) ([]EntityNode, error) {
	filtered, err := r.FilterDefinedEntities(graphID, []string{entityType}, enrichWithEdges)
	if err != nil {
		return nil, err
	}
	return filtered.Entities, nil
}

// ===== helpers =====

func strOr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func stringList(v any) []string {
	list, ok := v.([]string)
	if ok {
		return list
	}
	anyList, ok := v.([]any)
	if !ok {
		return []string{}
	}
	out := []string{}
	for _, item := range anyList {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func anyMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

var _ = fmt.Sprintf
