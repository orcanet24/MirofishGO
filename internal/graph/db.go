// Port del LocalGraphDatabase de local_graph.py sobre MySQL 8 / MariaDB:
// esquema, migraciones, nodos/aristas. Episodios/batches en db2.go.
package graph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"mirofish/internal/config"
	"mirofish/internal/logx"
	"mirofish/internal/timex"
)

var logger = logx.Get("mirofish.local_graph")

// Database es la base MySQL de grafos de conocimiento.
type Database struct {
	db *sql.DB

	// writeMu serializa las escrituras compuestas (episodio+item, extracción)
	// para que dos peticiones no intercalen filas a medias.
	writeMu sync.Mutex
}

// NewDatabase abre la BD y aplica el esquema+migraciones. dsn vacío es error:
// el binario no cae de vuelta a SQLite.
func NewDatabase(dsn string) (*Database, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("DB_DSN requerido: configura MySQL en .env (DB_DSN o DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME)")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	cfg := config.Load()
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(5 * time.Minute)
	d := &Database{db: db}
	if err := d.initDB(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return d, nil
}

// Close libera el pool.
func (d *Database) Close() error { return d.db.Close() }

// SQL expone el pool para que otros paquetes (OASIS) compartan la misma
// conexión MySQL en vez de abrir un pool paralelo por subsistema.
func (d *Database) SQL() *sql.DB { return d.db }

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS graphs (
		graph_id VARCHAR(64) NOT NULL,
		name TEXT,
		description TEXT,
		ontology LONGTEXT,
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		PRIMARY KEY (graph_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS nodes (
		uuid VARCHAR(64) NOT NULL,
		graph_id VARCHAR(64) NOT NULL,
		name VARCHAR(512) NOT NULL,
		labels TEXT,
		summary TEXT,
		attributes LONGTEXT,
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		PRIMARY KEY (uuid),
		UNIQUE KEY idx_nodes_graph_name (graph_id, name),
		KEY idx_nodes_graph (graph_id),
		FULLTEXT KEY ft_nodes (name, summary),
		CONSTRAINT fk_nodes_graph FOREIGN KEY (graph_id)
			REFERENCES graphs(graph_id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS edges (
		uuid VARCHAR(64) NOT NULL,
		graph_id VARCHAR(64) NOT NULL,
		name TEXT,
		fact TEXT,
		source_node_uuid VARCHAR(64),
		target_node_uuid VARCHAR(64),
		attributes LONGTEXT,
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		valid_at VARCHAR(40),
		invalid_at VARCHAR(40),
		expired_at VARCHAR(40),
		PRIMARY KEY (uuid),
		KEY idx_edges_graph (graph_id),
		KEY idx_edges_source (source_node_uuid),
		KEY idx_edges_target (target_node_uuid),
		FULLTEXT KEY ft_edges (fact, name),
		CONSTRAINT fk_edges_graph FOREIGN KEY (graph_id)
			REFERENCES graphs(graph_id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS episodes (
		uuid VARCHAR(64) NOT NULL,
		graph_id VARCHAR(64) NOT NULL,
		data LONGTEXT NOT NULL,
		data_type VARCHAR(32),
		source_description TEXT,
		episode_metadata LONGTEXT,
		processed TINYINT NOT NULL DEFAULT 1,
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		PRIMARY KEY (uuid),
		KEY idx_episodes_graph (graph_id),
		KEY idx_episodes_processed (processed),
		CONSTRAINT fk_episodes_graph FOREIGN KEY (graph_id)
			REFERENCES graphs(graph_id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS batches (
		batch_id VARCHAR(64) NOT NULL,
		graph_id VARCHAR(64),
		batch_metadata LONGTEXT,
		status VARCHAR(32) DEFAULT 'draft',
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		PRIMARY KEY (batch_id),
		KEY idx_batches_graph (graph_id),
		KEY idx_batches_created (created_at),
		CONSTRAINT fk_batches_graph FOREIGN KEY (graph_id)
			REFERENCES graphs(graph_id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS batch_items (
		id BIGINT NOT NULL AUTO_INCREMENT,
		batch_id VARCHAR(64) NOT NULL,
		episode_uuid VARCHAR(64),
		sequence_index INT,
		status VARCHAR(32) DEFAULT 'succeeded',
		error TEXT,
		graph_id VARCHAR(64),
		data LONGTEXT,
		chunk_index INT,
		chunk_sha256 CHAR(64),
		PRIMARY KEY (id),
		KEY idx_batch_items_batch (batch_id),
		CONSTRAINT fk_items_batch FOREIGN KEY (batch_id)
			REFERENCES batches(batch_id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
}

func (d *Database) initDB() error {
	for _, stmt := range schemaStatements {
		if _, err := d.db.Exec(stmt); err != nil {
			return fmt.Errorf("esquema MySQL: %w", err)
		}
	}
	return d.migrate()
}

// migrate añade columnas nuevas sobre BD creadas con versiones anteriores.
// information_schema sustituye al PRAGMA table_info de SQLite.
func (d *Database) migrate() error {
	var count int
	err := d.db.QueryRow(
		`SELECT COUNT(*) FROM information_schema.columns
		 WHERE table_schema = DATABASE() AND table_name = 'batch_items' AND column_name = 'error'`,
	).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		if _, err := d.db.Exec("ALTER TABLE batch_items ADD COLUMN error TEXT"); err != nil {
			return err
		}
	}
	return nil
}

// =====.ID builders — formato byte-idéntico al Python =====

// ===== Grafos =====

func (d *Database) CreateGraph(graphID, name, description string) error {
	if graphID == "" {
		graphID = newGraphID()
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	_, err := d.db.Exec(
		"INSERT IGNORE INTO graphs (graph_id, name, description, created_at) VALUES (?, ?, ?, ?)",
		graphID, name, description, timex.Now(),
	)
	return err
}

func (d *Database) GetGraph(graphID string) (*GraphRow, error) {
	var g GraphRow
	var name, desc, onto, createdAt any
	err := d.db.QueryRow(
		"SELECT graph_id, name, description, ontology, created_at FROM graphs WHERE graph_id = ?", graphID,
	).Scan(&g.GraphID, &name, &desc, &onto, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g.Name = anyString(name)
	g.Description = anyString(desc)
	g.Ontology = anyString(onto)
	g.CreatedAt = anyString(createdAt)
	return &g, nil
}

func (d *Database) DeleteGraph(graphID string) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	_, err := d.db.Exec("DELETE FROM graphs WHERE graph_id = ?", graphID)
	return err
}

func (d *Database) SetOntology(graphID string, ontology map[string]any) error {
	b, err := json.Marshal(ontology)
	if err != nil {
		return err
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	_, err = d.db.Exec("UPDATE graphs SET ontology = ? WHERE graph_id = ?", string(b), graphID)
	return err
}

func (d *Database) GetOntology(graphID string) map[string]any {
	var raw any
	if err := d.db.QueryRow("SELECT ontology FROM graphs WHERE graph_id = ?", graphID).Scan(&raw); err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if s := anyString(raw); s != "" {
		if json.Unmarshal([]byte(s), &out) == nil {
			return out
		}
	}
	return map[string]any{}
}

// ===== Nodos =====

// AddNode inserta; si (graph_id, name) existe devuelve el uuid existente y
// enriquece un summary vacío (comportamiento B8 del Python).
func (d *Database) AddNode(graphID, name string, labels []string, summary string, attributes map[string]any) (string, error) {
	if attributes == nil {
		attributes = map[string]any{}
	}
	labelsJSON, _ := json.Marshal(orDefaultLabels(labels))
	attrsJSON, _ := json.Marshal(attributes)
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	nodeUUID := newNodeUUID()
	_, err := d.db.Exec(
		`INSERT INTO nodes (uuid, graph_id, name, labels, summary, attributes, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		nodeUUID, graphID, name, string(labelsJSON), summary, string(attrsJSON), timex.Now(),
	)
	if err == nil {
		return nodeUUID, nil
	}
	// 1062 = ER_DUP_ENTRY (el UNIQUE de (graph_id, name)); MySQL no nombra la
	// restricción en el mensaje, así que se comprueba el código.
	var mysqlErr *mysqldriver.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1062 {
		return "", err
	}
	var existingUUID, existingSummary string
	if err := d.db.QueryRow(
		"SELECT uuid, summary FROM nodes WHERE graph_id = ? AND name = ?", graphID, name,
	).Scan(&existingUUID, &existingSummary); err != nil {
		return "", err
	}
	if strings.TrimSpace(existingSummary) == "" && strings.TrimSpace(summary) != "" {
		if _, err := d.db.Exec("UPDATE nodes SET summary = ? WHERE uuid = ?", summary, existingUUID); err != nil {
			return "", err
		}
	}
	return existingUUID, nil
}

// GetNode devuelve (nil, nil) si el nodo no existe, igual que GetEpisode.
// Antes propagaba sql.ErrNoRows como error, así que /entities/<g>/<uuid>
// respondía 500 en vez de 404 para un uuid inexistente.
func (d *Database) GetNode(nodeUUID string) (*LocalNode, error) {
	row := d.db.QueryRow(nodeSelect+" WHERE uuid = ?", nodeUUID)
	n, err := scanNodeRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return n, nil
}

func (d *Database) GetAllNodes(graphID string) ([]LocalNode, error) {
	rows, err := d.db.Query(nodeSelect+" WHERE graph_id = ? ORDER BY created_at, uuid", graphID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LocalNode
	for rows.Next() {
		n, err := scanNodeRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

const nodeSelect = "SELECT uuid, graph_id, name, labels, summary, attributes, created_at FROM nodes"

func scanNodeRow(scan func(dest ...any) error) (*LocalNode, error) {
	var (
		n                                                         LocalNode
		uuidStr, name, labels, summary, attrs, createdAt, graphID any
	)
	if err := scan(&uuidStr, &graphID, &name, &labels, &summary, &attrs, &createdAt); err != nil {
		return nil, err
	}
	n.UUID = anyString(uuidStr)
	n.Name = anyString(name)
	n.Summary = anyString(summary)
	n.Labels = jsonSliceOr(labels, []string{"Entity"})
	n.Attributes = jsonMapOr(attrs)
	if s := anyString(createdAt); s != "" {
		n.CreatedAt = &s
	}
	if s := anyString(graphID); s != "" {
		n.GraphID = &s
	}
	return &n, nil
}

// ===== Aristas =====

// AddEdge deduplica aristas idénticas (mismo fact entre mismos nodos).
func (d *Database) AddEdge(graphID, sourceUUID, targetUUID, name, fact string, attributes map[string]any) (string, error) {
	if attributes == nil {
		attributes = map[string]any{}
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	var existing string
	err := d.db.QueryRow(
		`SELECT uuid FROM edges WHERE graph_id = ? AND source_node_uuid = ?
		 AND target_node_uuid = ? AND fact = ?`,
		graphID, sourceUUID, targetUUID, fact,
	).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}

	edgeUUID := newEdgeUUID()
	attrsJSON, _ := json.Marshal(attributes)
	_, err = d.db.Exec(
		`INSERT INTO edges (uuid, graph_id, name, fact, source_node_uuid, target_node_uuid, attributes, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		edgeUUID, graphID, name, fact, sourceUUID, targetUUID, string(attrsJSON), timex.Now(),
	)
	if err != nil {
		return "", err
	}
	return edgeUUID, nil
}

const edgeSelect = `SELECT uuid, graph_id, name, fact, source_node_uuid, target_node_uuid,
	attributes, created_at, valid_at, invalid_at, expired_at FROM edges`

func scanEdgeRow(scan func(dest ...any) error) (*LocalEdge, error) {
	var (
		e                                                                                       LocalEdge
		uuidStr, graphID, name, fact, src, tgt, attrs, createdAt, validAt, invalidAt, expiredAt any
	)
	if err := scan(&uuidStr, &graphID, &name, &fact, &src, &tgt, &attrs, &createdAt, &validAt, &invalidAt, &expiredAt); err != nil {
		return nil, err
	}
	e.UUID = anyString(uuidStr)
	e.Name = anyString(name)
	e.Fact = anyString(fact)
	e.SourceNodeUUID = anyString(src)
	e.TargetNodeUUID = anyString(tgt)
	e.Attributes = jsonMapOr(attrs)
	if s := anyString(createdAt); s != "" {
		e.CreatedAt = &s
	}
	if s := anyString(validAt); s != "" {
		e.ValidAt = &s
	}
	if s := anyString(invalidAt); s != "" {
		e.InvalidAt = &s
	}
	if s := anyString(expiredAt); s != "" {
		e.ExpiredAt = &s
	}
	if s := anyString(graphID); s != "" {
		e.GraphID = &s
	}
	return &e, nil
}

func (d *Database) GetEdgesForNode(graphID, nodeUUID string) ([]LocalEdge, error) {
	rows, err := d.db.Query(
		edgeSelect+" WHERE graph_id = ? AND (source_node_uuid = ? OR target_node_uuid = ?)",
		graphID, nodeUUID, nodeUUID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectEdgeRows(rows)
}

func (d *Database) GetAllEdges(graphID string) ([]LocalEdge, error) {
	rows, err := d.db.Query(edgeSelect+" WHERE graph_id = ? ORDER BY created_at, uuid", graphID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectEdgeRows(rows)
}

func collectEdgeRows(rows *sql.Rows) ([]LocalEdge, error) {
	var out []LocalEdge
	for rows.Next() {
		e, err := scanEdgeRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// FindNodeByName replica _find_node_by_name.
func (d *Database) FindNodeByName(graphID, name string) (*LocalNode, error) {
	if name == "" {
		return nil, nil
	}
	row := d.db.QueryRow(nodeSelect+" WHERE graph_id = ? AND name = ?", graphID, name)
	n, err := scanNodeRow(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return n, nil
}

// ===== helpers JSON/sql =====

func anyString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v != nil {
		if b, ok := v.([]byte); ok {
			return string(b)
		}
	}
	return ""
}

func jsonSliceOr(raw any, def []string) []string {
	if s := anyString(raw); s != "" {
		var out []string
		if json.Unmarshal([]byte(s), &out) == nil {
			return out
		}
	}
	return def
}

func jsonMapOr(raw any) map[string]any {
	if s := anyString(raw); s != "" {
		var out map[string]any
		if json.Unmarshal([]byte(s), &out) == nil {
			return out
		}
	}
	return map[string]any{}
}

func orDefaultLabels(labels []string) []string {
	if len(labels) == 0 {
		return []string{"Entity"}
	}
	return labels
}
