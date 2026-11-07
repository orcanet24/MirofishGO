// Progreso y lecturas de batches: get_batch/list_batch_items/list_batches,
// con el avance progresivo de sub-lotes reflejado en percent_complete.
package graph

import (
	"database/sql"
)

// getBatchProgress replica _get_batch_progress.
func (d *Database) getBatchProgress(status string, itemCount int, batchID string) BatchProgress {
	succeeded := 0
	if status == "succeeded" {
		succeeded = itemCount
	}
	if batchID != "" && status == "processing" {
		// Los sub-lotes liberan episodios (processed=1) de forma progresiva.
		var c int
		if err := d.db.QueryRow(
			`SELECT COUNT(*) FROM episodes
			 WHERE processed = 1 AND uuid IN
			   (SELECT episode_uuid FROM batch_items WHERE batch_id = ?)`,
			batchID,
		).Scan(&c); err == nil {
			succeeded = c
		}
	}
	var percent float64
	switch status {
	case "succeeded":
		percent = 100.0
	case "processing":
		if itemCount > 0 {
			percent = 50.0 * float64(succeeded) / float64(itemCount)
		} else {
			percent = 50.0
		}
	default:
		percent = 0.0
	}
	return BatchProgress{PercentComplete: percent, SucceededItems: succeeded}
}

// GetBatch replica get_batch → BatchSummary (A3).
func (d *Database) GetBatch(batchID string) (*BatchSummary, error) {
	var (
		batchIDCol, graphID, metadata, status, createdAt any
	)
	row := d.db.QueryRow(
		"SELECT batch_id, graph_id, batch_metadata, status, created_at FROM batches WHERE batch_id = ?",
		batchID,
	)
	if err := row.Scan(&batchIDCol, &graphID, &metadata, &status, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var itemCount int
	if err := d.db.QueryRow(
		"SELECT COUNT(*) FROM batch_items WHERE batch_id = ?", batchID,
	).Scan(&itemCount); err != nil {
		return nil, err
	}

	summary := &BatchSummary{
		BatchID:   anyString(batchIDCol),
		Status:    anyString(status),
		Metadata:  jsonMapOr(metadata),
		ItemCount: itemCount,
	}
	summary.Progress = d.getBatchProgress(summary.Status, itemCount, summary.BatchID)
	return summary, nil
}

// ListBatchItems replica list_batch_items.
func (d *Database) ListBatchItems(batchID string) ([]BatchItemRecord, error) {
	rows, err := d.db.Query(
		"SELECT episode_uuid, sequence_index, status, error, graph_id, data FROM batch_items WHERE batch_id = ? ORDER BY sequence_index",
		batchID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BatchItemRecord{}
	for rows.Next() {
		var (
			epUUID, status, graphID, data any
			sequence                      int
			errCol                        any
		)
		if err := rows.Scan(&epUUID, &sequence, &status, &errCol, &graphID, &data); err != nil {
			return nil, err
		}
		rec := BatchItemRecord{
			EpisodeUUID:   anyString(epUUID),
			SourceUUID:    anyString(epUUID),
			SequenceIndex: sequence,
			Status:        anyString(status),
			GraphID:       anyString(graphID),
			Data:          anyString(data),
		}
		if s := anyString(errCol); s != "" {
			v := s
			rec.Error = &v
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ListBatches replica list_batches (limit alto: el paginado externo corta).
func (d *Database) ListBatches(limit int) ([]BatchListItem, error) {
	rows, err := d.db.Query(
		"SELECT batch_id, graph_id, batch_metadata, status FROM batches ORDER BY created_at DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BatchListItem{}
	for rows.Next() {
		var batchIDCol, graphID, metadata, status any
		if err := rows.Scan(&batchIDCol, &graphID, &metadata, &status); err != nil {
			return nil, err
		}
		out = append(out, BatchListItem{
			BatchID:  anyString(batchIDCol),
			GraphID:  anyString(graphID),
			Metadata: jsonMapOr(metadata),
			Status:   anyString(status),
		})
	}
	return out, rows.Err()
}
