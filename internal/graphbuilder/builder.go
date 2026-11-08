// Package graphbuilder — port de backend/app/services/graph_builder.py para el
// backend local (MySQL): crea grafos, instala la ontología, ingiere lotes de
// texto y expone los datos del grafo con el mismo shape JSON que el Python.
package graphbuilder

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"mirofish/internal/graph"
	"mirofish/internal/llm"
	"mirofish/internal/locale"
	"mirofish/internal/ontology"
)

// IngestionWaitTimeout replica ZEP_INGESTION_WAIT_TIMEOUT_SECONDS (600s).
const IngestionWaitTimeout = 600

// BatchSubmission replica el dataclass frozen BatchSubmission.
type BatchSubmission struct {
	BatchID      string
	OperationID  string
	EpisodeUUIDs []string
	ItemCount    int
}

// Builder replica GraphBuilderService sobre el backend local.
type Builder struct {
	Client *graph.Client
	Locale string // locale capturado antes de lanzar la goroutine de build (default es)

	// LLMFactory opcional: config LLM delegada del usuario que ejecuta el
	// build; nil = global del .env (comportamiento histórico).
	LLMFactory func() (*llm.Client, error)
}

// New crea el servicio sobre un cliente de grafo local.
func New(client *graph.Client) *Builder { return &Builder{Client: client, Locale: "es"} }

func (b *Builder) t(key string, args map[string]string) string {
	return locale.T(b.Locale, key, args)
}

// CreateGraph replica create_graph: id "mirofish_<16hex>" y callback durable
// antes del POST no idempotente.
func (b *Builder) CreateGraph(name string, graphID string, callback func(string)) (string, error) {
	if graphID == "" {
		graphID = graph.NewMiroFishGraphID()
	}
	if callback != nil {
		callback(graphID)
	}
	if _, err := b.Client.CreateGraph(graphID, name, "MiroFish Social Simulation Graph"); err != nil {
		return "", err
	}
	return graphID, nil
}

// BuildOperationID replica build_operation_id (sha256 del payload).
func BuildOperationID(graphID string, chunks []string) string {
	payloadHash := sha256.Sum256([]byte(strings.Join(chunks, "\x00")))
	combined := sha256.Sum256([]byte(fmt.Sprintf("%s:%s", graphID, hex.EncodeToString(payloadHash[:]))))
	return hex.EncodeToString(combined[:])
}

// ValidateBatchChunks replica validate_batch_chunks.
func ValidateBatchChunks(chunks []string, batchSize int) error {
	if len(chunks) == 0 {
		return fmt.Errorf("At least one text chunk is required")
	}
	if batchSize < 1 || batchSize > 350 {
		return fmt.Errorf("batch_size must be between 1 and 350")
	}
	if len(chunks) > 50000 {
		return fmt.Errorf("A Zep batch cannot contain more than 50,000 items")
	}
	for index, chunk := range chunks {
		if len([]rune(chunk)) > 10000 {
			return fmt.Errorf("Zep batch item exceeds 10,000 characters at chunk %d", index)
		}
	}
	return nil
}

// safeAttrName replica safe_attr_name: prefija reservados con "entity_".
func safeAttrName(attrName string) string {
	if ontology.ReservedAttributeNames[strings.ToLower(attrName)] {
		return "entity_" + attrName
	}
	return attrName
}

// SetOntology replica set_ontology: normaliza y guarda la ontología canónica
// (MockGraphAPI.set_ontology acepta dicts).
func (b *Builder) SetOntology(graphID string, ontologyDef map[string]any) error {
	entities := map[string]any{}
	if list, ok := ontologyDef["entity_types"].([]any); ok {
		for i, raw := range list {
			if i >= ontology.MaxTypes {
				break
			}
			def, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := def["name"].(string)
			desc, _ := def["description"].(string)
			if desc == "" {
				desc = fmt.Sprintf("A %s entity.", name)
			}
			attrs := map[string]any{}
			for _, normalized := range ontology.NormalizeAttributes(def["attributes"]) {
				attrName := safeAttrName(strOr(normalized["name"]))
				attrDesc := strOr(normalized["description"])
				attrs[attrName] = map[string]any{"name": attrName, "description": attrDesc}
			}
			entities[name] = map[string]any{
				"name":        name,
				"description": desc,
				"attributes":  attrs,
			}
		}
	}

	edges := map[string]any{}
	if list, ok := ontologyDef["edge_types"].([]any); ok {
		for i, raw := range list {
			if i >= ontology.MaxTypes {
				break
			}
			def, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := def["name"].(string)
			desc, _ := def["description"].(string)
			if desc == "" {
				desc = fmt.Sprintf("A %s relationship.", name)
			}
			attrs := map[string]any{}
			for _, normalized := range ontology.NormalizeAttributes(def["attributes"]) {
				attrName := safeAttrName(strOr(normalized["name"]))
				attrDesc := strOr(normalized["description"])
				attrs[attrName] = map[string]any{"name": attrName, "description": attrDesc}
			}
			edges[name] = map[string]any{
				"name":        name,
				"description": desc,
				"attributes":  attrs,
			}
		}
	}

	if len(entities) == 0 && len(edges) == 0 {
		return nil
	}
	return b.Client.SetOntologyCanonica(graphID, entities, edges)
}

// AddTextBatches replica add_text_batches: crea el batch con metadata de
// operación, encola los chunks por lotes y dispara el procesado.
func (b *Builder) AddTextBatches(
	graphID string,
	chunks []string,
	batchSize int,
	progressCallback func(msg string, ratio float64),
	batchCreatedCallback func(batchID *string, operationID string),
) (BatchSubmission, error) {
	if graphID == "" {
		return BatchSubmission{}, fmt.Errorf("graph_id is required")
	}
	if err := ValidateBatchChunks(chunks, batchSize); err != nil {
		return BatchSubmission{}, err
	}

	totalChunks := len(chunks)
	operationID := BuildOperationID(graphID, chunks)
	if batchCreatedCallback != nil {
		batchCreatedCallback(nil, operationID)
	}

	batch, err := b.Client.BatchCreate(map[string]any{
		"mirofish_operation_id": operationID,
		"graph_id":              graphID,
		"chunk_count":           totalChunks,
	})
	if err != nil {
		return BatchSubmission{}, err
	}
	batchID := batch.BatchID
	if batchID == "" {
		return BatchSubmission{}, fmt.Errorf("Zep Batch API returned no batch_id")
	}
	if batchCreatedCallback != nil {
		batchCreatedCallback(&batchID, operationID)
	}

	episodeUUIDs := []string{}
	for i := 0; i < totalChunks; i += batchSize {
		batchChunks := chunks[i:minInt(i+batchSize, totalChunks)]
		batchNum := i/batchSize + 1
		totalBatches := (totalChunks + batchSize - 1) / batchSize

		if progressCallback != nil {
			progress := float64(i+len(batchChunks)) / float64(totalChunks)
			progressCallback(b.t("progress.sendingBatch", map[string]string{
				"current": fmt.Sprintf("%d", batchNum),
				"total":   fmt.Sprintf("%d", totalBatches),
				"chunks":  fmt.Sprintf("%d", len(batchChunks)),
			}), progress)
		}

		items := make([]graph.BatchItemInput, len(batchChunks))
		for offset, chunk := range batchChunks {
			sum := sha256.Sum256([]byte(chunk))
			items[offset] = graph.BatchItemInput{
				GraphID:           graphID,
				Data:              chunk,
				DataType:          "text",
				SourceDescription: "MiroFish source document chunk",
				Metadata: map[string]any{
					"mirofish_operation_id": operationID,
					"chunk_index":           float64(i + offset),
					"chunk_sha256":          hex.EncodeToString(sum[:]),
				},
			}
		}

		added, err := b.Client.BatchAdd(batchID, items)
		if err != nil {
			return BatchSubmission{}, fmt.Errorf("Zep batch %s item submission failed", batchID)
		}
		for _, a := range added {
			if a.EpisodeUUID != "" {
				episodeUUIDs = append(episodeUUIDs, a.EpisodeUUID)
			}
		}
	}

	if err := b.Client.BatchProcess(batchID, b.LLMFactory); err != nil {
		summary, getErr := b.Client.BatchGet(batchID)
		if getErr != nil || summary == nil || summary.Status == "" || summary.Status == "draft" {
			return BatchSubmission{}, fmt.Errorf("Zep batch %s processing is unconfirmed", batchID)
		}
	}

	return BatchSubmission{
		BatchID:      batchID,
		OperationID:  operationID,
		EpisodeUUIDs: episodeUUIDs,
		ItemCount:    totalChunks,
	}, nil
}

// GetBatchSummary replica get_batch_summary.
func (b *Builder) GetBatchSummary(batchID string) (*graph.BatchSummary, error) {
	return b.Client.BatchGet(batchID)
}

// WaitForBatch replica _wait_for_batch: espera estado terminal con deadline
// que se reanima ante progreso; valida items y devuelve los episode uuids.
func (b *Builder) WaitForBatch(
	submission BatchSubmission,
	progressCallback func(msg string, ratio float64),
	timeout int,
) ([]string, error) {
	if timeout <= 0 {
		timeout = IngestionWaitTimeout
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	startTime := time.Now()
	lastCompleted := 0
	terminalStates := map[string]bool{
		"succeeded": true, "partial": true, "failed": true,
		"invalid": true, "canceled": true,
	}

	var status string
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"Zep batch %s did not finish within %ds without progress",
				submission.BatchID, timeout)
		}

		summary, err := b.Client.BatchGet(submission.BatchID)
		if err != nil {
			return nil, err
		}
		if summary == nil {
			return nil, fmt.Errorf("Zep batch %s not found", submission.BatchID)
		}
		status = summary.Status
		percent := summary.Progress.PercentComplete / 100.0
		completed := summary.Progress.SucceededItems
		if completed > lastCompleted {
			lastCompleted = completed
			deadline = time.Now().Add(time.Duration(timeout) * time.Second)
		}
		if progressCallback != nil {
			progressCallback(b.t("progress.zepProcessing", map[string]string{
				"completed": fmt.Sprintf("%d", completed),
				"total":     fmt.Sprintf("%d", submission.ItemCount),
				"pending":   fmt.Sprintf("%d", maxInt(submission.ItemCount-completed, 0)),
				"elapsed":   fmt.Sprintf("%d", int(time.Since(startTime).Seconds())),
			}), clampRatio(percent))
		}

		if terminalStates[status] {
			break
		}
		time.Sleep(3 * time.Second)
	}

	items, err := b.Client.DB.ListBatchItems(submission.BatchID)
	if err != nil {
		return nil, err
	}
	if status != "succeeded" {
		failedItems := []graph.BatchItemRecord{}
		for _, item := range items {
			if item.Status != "succeeded" && item.Status != "skipped" {
				failedItems = append(failedItems, item)
			}
		}
		var firstError any
		if len(failedItems) > 0 && failedItems[0].Error != nil {
			firstError = *failedItems[0].Error
		}
		return nil, fmt.Errorf(
			"Zep batch %s ended as %s; failed_items=%d; first_error=%v",
			submission.BatchID, status, len(failedItems), firstError)
	}
	if len(items) != submission.ItemCount {
		return nil, fmt.Errorf(
			"Zep batch %s contains %d items, expected %d",
			submission.BatchID, len(items), submission.ItemCount)
	}

	ordered := make([]graph.BatchItemRecord, len(items))
	copy(ordered, items)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].SequenceIndex < ordered[j].SequenceIndex
	})
	episodeUUIDs := []string{}
	for _, item := range ordered {
		if item.Status != "succeeded" || item.EpisodeUUID == "" {
			return nil, fmt.Errorf("Zep batch %s returned an incomplete item", submission.BatchID)
		}
		if item.SourceUUID != "" && item.SourceUUID != item.EpisodeUUID {
			return nil, fmt.Errorf("Zep batch %s returned mismatched episode UUIDs", submission.BatchID)
		}
		episodeUUIDs = append(episodeUUIDs, item.EpisodeUUID)
	}

	if progressCallback != nil {
		progressCallback(b.t("progress.processingComplete", map[string]string{
			"completed": fmt.Sprintf("%d", len(episodeUUIDs)),
			"total":     fmt.Sprintf("%d", submission.ItemCount),
		}), 1.0)
	}
	return episodeUUIDs, nil
}

// GraphInfo replica el dataclass GraphInfo.
type GraphInfo struct {
	GraphID      string
	NodeCount    int
	EdgeCount    int
	EntityTypes  []string
}

// GetGraphInfo replica _get_graph_info.
func (b *Builder) GetGraphInfo(graphID string) (*GraphInfo, error) {
	nodes, err := b.Client.DB.GetAllNodes(graphID)
	if err != nil {
		return nil, err
	}
	edges, err := b.Client.DB.GetAllEdges(graphID)
	if err != nil {
		return nil, err
	}
	entityTypes := map[string]bool{}
	for _, node := range nodes {
		for _, label := range node.Labels {
			if label != "Entity" && label != "Node" {
				entityTypes[label] = true
			}
		}
	}
	names := make([]string, 0, len(entityTypes))
	for name := range entityTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return &GraphInfo{
		GraphID:     graphID,
		NodeCount:   len(nodes),
		EdgeCount:   len(edges),
		EntityTypes: names,
	}, nil
}

// GetGraphData replica get_graph_data (mismo shape JSON que el Python).
func (b *Builder) GetGraphData(graphID string) (map[string]any, error) {
	nodes, err := b.Client.DB.GetAllNodes(graphID)
	if err != nil {
		return nil, err
	}
	edges, err := b.Client.DB.GetAllEdges(graphID)
	if err != nil {
		return nil, err
	}

	nodeNames := map[string]string{}
	for _, node := range nodes {
		name := node.Name
		if name == "" {
			name = ""
		}
		nodeNames[node.UUID] = name
	}

	nodesData := []map[string]any{}
	for _, node := range nodes {
		created := ""
		if node.CreatedAt != nil {
			created = *node.CreatedAt
		}
		labels := node.Labels
		if labels == nil {
			labels = []string{}
		}
		nodesData = append(nodesData, map[string]any{
			"uuid":       node.UUID,
			"name":       node.Name,
			"labels":     labels,
			"summary":    node.Summary,
			"attributes": node.Attributes,
			"created_at": created,
		})
	}

	edgesData := []map[string]any{}
	for _, edge := range edges {
		factType := edge.Name
		if factType == "" {
			factType = ""
		}
		edgesData = append(edgesData, map[string]any{
			"uuid":              edge.UUID,
			"name":              edge.Name,
			"fact":              edge.Fact,
			"fact_type":         factType,
			"source_node_uuid":  edge.SourceNodeUUID,
			"target_node_uuid":  edge.TargetNodeUUID,
			"source_node_name":  nodeNames[edge.SourceNodeUUID],
			"target_node_name":  nodeNames[edge.TargetNodeUUID],
			"attributes":        edge.Attributes,
			"created_at":        nullableString(edge.CreatedAt),
			"valid_at":          nullableString(edge.ValidAt),
			"invalid_at":        nullableString(edge.InvalidAt),
			"expired_at":        nullableString(edge.ExpiredAt),
			"episodes":          []any{},
		})
	}

	return map[string]any{
		"graph_id":   graphID,
		"nodes":      nodesData,
		"edges":      edgesData,
		"node_count": len(nodesData),
		"edge_count": len(edgesData),
	}, nil
}

// DeleteGraph replica delete_graph.
func (b *Builder) DeleteGraph(graphID string) error {
	return b.Client.DeleteGraph(graphID)
}

func strOr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func nullableString(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func clampRatio(r float64) float64 {
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
