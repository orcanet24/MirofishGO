// IDs y episodios del LocalGraphDatabase (port de local_graph.py).
package graph

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"mirofish/internal/timex"
)

// ===== IDs — formato byte-idéntico al Python (uuid4().hex[:16]) =====

func hex16() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func newNodeUUID() string { return "node_" + hex16() }
func newEdgeUUID() string { return "edge_" + hex16() }
func newEpUUID() string   { return "ep_" + hex16() }
func newBatchID() string  { return "batch_" + hex16() }
func newGraphID() string  { return "graph_" + hex16() }

// ===== Episodios =====

// AddEpisodeOptions replica los kwargs de add_episode.
type AddEpisodeOptions struct {
	GraphID           string
	Data              string
	DataType          string // default "text"
	SourceDescription string
	Metadata          map[string]any
	CreatedAt         string // opcional
	NoExtract         bool   // extract=False del Python
}

// AddEpisode inserta el episodio con processed=0 y lanza la extracción
// en background (equivalente del hilo daemon de Python).
func (d *Database) AddEpisode(opts AddEpisodeOptions) (string, error) {
	if opts.DataType == "" {
		opts.DataType = "text"
	}
	epUUID := newEpUUID()
	ts := opts.CreatedAt
	if ts == "" {
		ts = timex.Now()
	}
	metaJSON, _ := json.Marshal(orEmpty(opts.Metadata))

	d.writeMu.Lock()
	_, err := d.db.Exec(
		`INSERT INTO episodes (uuid, graph_id, data, data_type, source_description, episode_metadata, processed, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
		epUUID, opts.GraphID, opts.Data, opts.DataType, opts.SourceDescription, string(metaJSON), ts,
	)
	d.writeMu.Unlock()
	if err != nil {
		return "", err
	}

	if !opts.NoExtract {
		go d.extractEpisodeBackground(opts.GraphID, epUUID, opts.Data)
	}
	return epUUID, nil
}

// extractEpisodeBackground replica _spawn_episode_extraction: extrae entidades
// con la ontología del grafo y marca processed=1 SIEMPRE al terminar (con o
// sin éxito) para no bloquear barreras de espera.
func (d *Database) extractEpisodeBackground(graphID, epUUID, data string) {
	defer func() {
		if r := recover(); r != nil {
			logger.Warn(fmt.Sprintf("Extracción de episodio %s pánico: %v", epUUID[:8], r))
		}
		d.markEpisodeProcessed(epUUID)
	}()

	ontology := d.GetOntology(graphID)
	if len(ontology) == 0 || strings.TrimSpace(data) == "" {
		return
	}
	logger.Info(fmt.Sprintf("train extract episode %s (len=%d)", epUUID[:8], len(data)))
	generated := d.ExtractEntitiesFromText(data, ontology)
	if err := d.PersistExtraction(graphID, generated); err != nil {
		logger.Warn(fmt.Sprintf("Persistencia de extracción de %s falló: %v", epUUID[:8], err))
	}
}

// markEpisodeProcessed pone processed=1.
func (d *Database) markEpisodeProcessed(epUUID string) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	_, _ = d.db.Exec("UPDATE episodes SET processed = 1 WHERE uuid = ?", epUUID)
}

// GetEpisode devuelve el episodio o nil (compatible con el Python).
func (d *Database) GetEpisode(episodeUUID string) (*LocalEpisode, error) {
	var (
		e                                                                LocalEpisode
		epUUID, graphID, data, dataType, sourceDesc, metaJSON, createdAt any
		processed                                                        int
	)
	err := d.db.QueryRow(
		"SELECT uuid, graph_id, data, data_type, source_description, episode_metadata, processed, created_at FROM episodes WHERE uuid = ?",
		episodeUUID,
	).Scan(&epUUID, &graphID, &data, &dataType, &sourceDesc, &metaJSON, &processed, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.UUID = anyString(epUUID)
	e.GraphID = anyString(graphID)
	e.Data = anyString(data)
	e.DataType = anyString(dataType)
	e.SourceDescription = anyString(sourceDesc)
	e.Processed = processed != 0
	e.Metadata = jsonMapOr(metaJSON)
	if s := anyString(createdAt); s != "" {
		e.CreatedAt = &s
	}
	return &e, nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
