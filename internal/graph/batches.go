// Batches del LocalGraphDatabase: creación, items, proceso en background,
// liberación progresiva de barreras y progreso (port de local_graph.py).
package graph

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"mirofish/internal/timex"
)

// BatchItemInput replica el item del Python (dict con data/metadata/...).
type BatchItemInput struct {
	GraphID           string
	Data              string
	DataType          string
	SourceDescription string
	Metadata          map[string]any
}

// CreateBatch replica create_batch.
func (d *Database) CreateBatch(graphID string, metadata map[string]any) (string, error) {
	metaJSON, _ := json.Marshal(orEmpty(metadata))
	batchID := newBatchID()
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	_, err := d.db.Exec(
		"INSERT INTO batches (batch_id, graph_id, batch_metadata, status, created_at) VALUES (?, ?, ?, 'draft', ?)",
		batchID, graphID, string(metaJSON), timex.Now(),
	)
	if err != nil {
		return "", err
	}
	return batchID, nil
}

// AddBatchItems replica add_batch_items: crea episodios (sin extracción) y
// registros en batch_items.
func (d *Database) AddBatchItems(batchID string, items []BatchItemInput) ([]string, error) {
	var graphID string
	if err := d.db.QueryRow("SELECT graph_id FROM batches WHERE batch_id = ?", batchID).Scan(&graphID); err != nil && graphID == "" {
		graphID = ""
	}

	episodeUUIDs := []string{}
	for i, item := range items {
		gID := item.GraphID
		if gID == "" {
			gID = graphID
		}
		meta := orEmpty(item.Metadata)
		epUUID, err := d.AddEpisode(AddEpisodeOptions{
			GraphID:           gID,
			Data:              item.Data,
			DataType:          item.DataType,
			SourceDescription: item.SourceDescription,
			Metadata:          meta,
			NoExtract:         true, // la extracción del batch ocurre en process/finalize
			CreatedAt:         "",
		})
		if err != nil {
			return nil, err
		}

		chunkIndex := 0
		if v, ok := meta["chunk_index"]; ok {
			if n, ok := v.(float64); ok {
				chunkIndex = int(n)
			}
		}
		chunkSHA := ""
		if v, ok := meta["chunk_sha256"]; ok {
			if s, ok := v.(string); ok {
				chunkSHA = s
			}
		}
		d.writeMu.Lock()
		_, err = d.db.Exec(
			`INSERT INTO batch_items (batch_id, episode_uuid, sequence_index, status, graph_id, data, chunk_index, chunk_sha256)
			 VALUES (?, ?, ?, 'succeeded', ?, ?, ?, ?)`,
			batchID, epUUID, i, gID, item.Data, chunkIndex, chunkSHA,
		)
		d.writeMu.Unlock()
		if err != nil {
			return nil, err
		}
		episodeUUIDs = append(episodeUUIDs, epUUID)
	}
	return episodeUUIDs, nil
}

// ProcessBatch marca 'processing' y dispara el finalizador en background.
func (d *Database) ProcessBatch(batchID string) error {
	d.writeMu.Lock()
	_, err := d.db.Exec("UPDATE batches SET status = 'processing' WHERE batch_id = ?", batchID)
	d.writeMu.Unlock()
	if err != nil {
		return err
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Warn(fmt.Sprintf("Finalización de batch %s pánico: %v", batchID[:8], r))
			}
		}()
		d.FinalizeBatch(batchID)
	}()
	return nil
}

// FinalizeBatch replica _finalize_batch: extrae, fija status y libera barrera.
func (d *Database) FinalizeBatch(batchID string) {
	status := "succeeded"
	if err := d.ExtractFromBatch(batchID); err != nil {
		logger.Warn(fmt.Sprintf("Extracción de batch %s falló: %v", batchID[:8], err))
		status = "partial"
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if _, err := d.db.Exec("UPDATE batches SET status = ? WHERE batch_id = ?", status, batchID); err != nil {
		return
	}
	// liberar la barrera de episodios del batch (semántica zep: process complete)
	_, _ = d.db.Exec(
		`UPDATE episodes SET processed = 1
		 WHERE processed = 0 AND uuid IN
		   (SELECT episode_uuid FROM batch_items WHERE batch_id = ?)`,
		batchID,
	)
}

// ExtractFromBatch replica _extract_from_batch: sub-lotes de CHUNK_SIZE textos
// por llamada; cada sub-lote libera sus episodios al terminar.
func (d *Database) ExtractFromBatch(batchID string) error {
	batch, err := d.getBatchRow(batchID)
	if err != nil {
		return err
	}
	if batch == nil {
		return nil
	}
	graphID := batch.GraphID

	ontology := d.GetOntology(graphID)
	hasTypes := false
	if list, ok := ontology["entity_types"].([]any); ok && len(list) > 0 {
		hasTypes = true
	}
	if list, ok := ontology["edge_types"].([]any); ok && len(list) > 0 {
		hasTypes = true
	}
	if !hasTypes {
		logger.Info(fmt.Sprintf("Batch %s sin ontología; extracción omitida", batchID[:8]))
		return nil
	}

	rows, err := d.db.Query(
		"SELECT episode_uuid, data FROM batch_items WHERE batch_id = ?", batchID,
	)
	if err != nil {
		return err
	}
	type item struct {
		ep   string
		data string
	}
	items := []item{}
	for rows.Next() {
		var ep, data any
		if err := rows.Scan(&ep, &data); err != nil {
			rows.Close()
			return err
		}
		if s := anyString(data); s != "" {
			items = append(items, item{ep: anyString(ep), data: s})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	const chunkSize = 6
	totalNodes, totalEdges := 0, 0
	chunkIndex := 0
	for start := 0; start < len(items); start += chunkSize {
		chunkIndex++
		end := start + chunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]
		texts := make([]string, len(chunk))
		for i, it := range chunk {
			texts[i] = it.data
		}
		nodes, edges, extrErr := d.ExtractFromEpisodeBatch(graphID, texts, ontology, 6)
		if extrErr != nil {
			logger.Warn(fmt.Sprintf(
				"Chunk %d del batch %s falló (sus episodios se liberan sin extracción): %v",
				chunkIndex, batchID[:8], extrErr))
		} else {
			totalNodes += nodes
			totalEdges += edges
		}
		// liberar barrera del sub-lote (con o sin extracción)
		d.writeMu.Lock()
		for _, it := range chunk {
			_, _ = d.db.Exec(
				"UPDATE episodes SET processed = 1 WHERE processed = 0 AND uuid = ?", it.ep,
			)
		}
		d.writeMu.Unlock()
		logger.Info(fmt.Sprintf("Batch %s: sub-lote %d listo (%d/%d episodios)",
			batchID[:8], chunkIndex, minInt(end, len(items)), len(items)))
	}

	if totalNodes > 0 || totalEdges > 0 {
		logger.Info(fmt.Sprintf("Local extraction: %d nodes, %d edges from batch %s",
			totalNodes, totalEdges, batchID[:8]))
	}
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// batchRow es fila cruda de la tabla batches para uso interno.
type batchRow struct {
	BatchID   string
	GraphID   string
	Status    string
	CreatedAt string
}

func (d *Database) getBatchRow(batchID string) (*batchRow, error) {
	var b batchRow
	var graphID, status, createdAt any
	err := d.db.QueryRow(
		"SELECT batch_id, graph_id, status, created_at FROM batches WHERE batch_id = ?", batchID,
	).Scan(&b.BatchID, &graphID, &status, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.GraphID = anyString(graphID)
	b.Status = anyString(status)
	b.CreatedAt = anyString(createdAt)
	return &b, nil
}
