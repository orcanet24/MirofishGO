// LocalGraphClient: cliente compatible con la interfaz del SDK de Zep Cloud
// sobre MySQL local (graph/node/edge/episode/batch APIs del Python).
package graph

// Client replica LocalGraphClient.
type Client struct {
	DB *Database
}

// NewClient crea DB+cliente. dsn vacío → DB_DSN del .env; nunca cae a SQLite.
func NewClient(dsn string) (*Client, error) {
	if dsn == "" {
		dsn = DefaultDSN()
	}
	db, err := NewDatabase(dsn)
	if err != nil {
		return nil, err
	}
	return &Client{DB: db}, nil
}

// CreateGraph replica MockGraphAPI.create.
func (c *Client) CreateGraph(graphID, name, description string) (string, error) {
	if graphID == "" {
		graphID = newGraphID()
	}
	if err := c.DB.CreateGraph(graphID, name, description); err != nil {
		return "", err
	}
	return graphID, nil
}

// GraphInfo replica get(): fila de graphs + ontology_loaded.
type GraphInfo struct {
	Row            GraphRow
	OntologyLoaded map[string]any
}

// GetGraph replica graph.get — NotFoundError si no existe.
func (c *Client) GetGraph(graphID string) (*GraphInfo, error) {
	row, err := c.DB.GetGraph(graphID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, &NotFoundError{"Graph not found"}
	}
	return &GraphInfo{Row: *row, OntologyLoaded: c.DB.GetOntology(graphID)}, nil
}

// DeleteGraph replica graph.delete.
func (c *Client) DeleteGraph(graphID string) error {
	return c.DB.DeleteGraph(graphID)
}

// SetOntologyCanonica guarda la ontología en formato canónico dicts
// (en Go los consumidores siempre pasan dicts; no existen clases pydantic).
func (c *Client) SetOntologyCanonica(graphID string, entities map[string]any, edges map[string]any) error {
	type typeEntry struct {
		Name        string           `json:"name"`
		Description string           `json:"description"`
		Attributes  []map[string]any `json:"attributes"`
	}
	canonical := map[string]any{
		"entity_types": []typeEntry{},
		"edge_types":   []typeEntry{},
	}

	entityTypes := []typeEntry{}
	for name, val := range entities {
		if asDict, ok := val.(map[string]any); ok {
			entityTypes = append(entityTypes, typeEntry{
				Name:        strOrDefault(asDict["name"], name),
				Description: strOr(asDict["description"]),
				Attributes:  attrsAny(asDict["attributes"]),
			})
			continue
		}
		// Sin pydantic en Go: tipo no reconocido → fallback documentado.
		entityTypes = append(entityTypes, typeEntry{
			Name:        name,
			Description: "A " + name + " entity.",
			Attributes:  []map[string]any{},
		})
	}
	canonical["entity_types"] = entityTypes

	edgeTypes := []typeEntry{}
	for name, val := range edges {
		if asDict, ok := val.(map[string]any); ok {
			edgeTypes = append(edgeTypes, typeEntry{
				Name:        strOrDefault(asDict["name"], name),
				Description: strOr(asDict["description"]),
				Attributes:  attrsAny(asDict["attributes"]),
			})
			continue
		}
		edgeTypes = append(edgeTypes, typeEntry{
			Name:        name,
			Description: "A " + name + " relationship.",
			Attributes:  []map[string]any{},
		})
	}
	canonical["edge_types"] = edgeTypes

	return c.DB.SetOntology(graphID, canonical)
}

func attrsAny(v any) []map[string]any {
	if list, ok := v.([]any); ok {
		out := []map[string]any{}
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	if list, ok := v.([]map[string]any); ok {
		return list
	}
	return []map[string]any{}
}

// Search replica graph.search (reranker ignorado como se anuncia).
func (c *Client) Search(graphID, query string, limit int, scope string) *SearchResult {
	return unwrapPtr(c.DB.Search(graphID, query, limit, scope))
}

// AddEpisodeClient replica graph.add: crea episodio y devuelve LocalEpisode.
func (c *Client) AddEpisode(graphID, dataType, data, createdAt, sourceDescription string, metadata map[string]any) (*LocalEpisode, error) {
	epUUID, err := c.DB.AddEpisode(AddEpisodeOptions{
		GraphID:           graphID,
		Data:              data,
		DataType:          dataType,
		SourceDescription: sourceDescription,
		Metadata:          metadata,
		CreatedAt:         createdAt,
	})
	if err != nil {
		return nil, err
	}
	return c.DB.GetEpisode(epUUID)
}

// ===== Node API =====

// NodeGet replica node.get — NotFoundError si no existe.
func (c *Client) NodeGet(nodeUUID string) (*LocalNode, error) {
	node, err := c.DB.GetNode(nodeUUID)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, &NotFoundError{"Node not found"}
	}
	return node, nil
}

// NodeGetByGraphID replica node.with_raw_response.get_by_graph_id.
func (c *Client) NodeGetByGraphID(graphID string, limit int, cursor int) (*RawPage[LocalNode], error) {
	limit = clampPageLimit(limit)
	nodes, err := c.DB.GetAllNodes(graphID)
	if err != nil {
		return nil, err
	}
	return pageOf(nodes, limit, cursor), nil
}

// NodeGetEdges replica B9: node.get_edges exige graph_id.
func (c *Client) NodeGetEdges(nodeUUID, graphID string) (*SearchResult, error) {
	if graphID == "" {
		return nil, fmtError("graph_id is required for node.get_edges")
	}
	edges, err := c.DB.GetEdgesForNode(graphID, nodeUUID)
	if err != nil {
		return nil, err
	}
	if edges == nil {
		edges = []LocalEdge{}
	}
	return &SearchResult{Edges: edges, Nodes: []LocalNode{}}, nil
}

// ===== Edge API =====

// EdgeGetByGraphID replica edge.with_raw_response.get_by_graph_id.
func (c *Client) EdgeGetByGraphID(graphID string, limit int, cursor int) (*RawPage[LocalEdge], error) {
	limit = clampPageLimit(limit)
	edges, err := c.DB.GetAllEdges(graphID)
	if err != nil {
		return nil, err
	}
	return pageOf(edges, limit, cursor), nil
}

// ===== Episode API =====

// EpisodeGet replica episode.get — NotFoundError si no existe.
func (c *Client) EpisodeGet(uuid string) (*LocalEpisode, error) {
	ep, err := c.DB.GetEpisode(uuid)
	if err != nil {
		return nil, err
	}
	if ep == nil {
		return nil, &NotFoundError{"Episode not found"}
	}
	return ep, nil
}

// EpisodeSummaryResume replica el SimpleNamespace de episode.get_by_graph_id.
type EpisodeSummaryResume struct {
	UUID      string
	GraphID   string
	Data      string
	Processed bool
	CreatedAt string
}

// EpisodeGetByGraphID replica episode.get_by_graph_id (lastn desc).
func (c *Client) EpisodeGetByGraphID(graphID string, lastn int) (*RawPage[EpisodeSummaryResume], error) {
	if lastn <= 0 {
		lastn = 50
	}
	rows, err := c.DB.db.Query(
		"SELECT uuid, graph_id, data, processed, created_at FROM episodes WHERE graph_id = ? ORDER BY created_at DESC LIMIT ?",
		graphID, lastn,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EpisodeSummaryResume{}
	for rows.Next() {
		var ep, gID, data, createdAt any
		var processed int
		if err := rows.Scan(&ep, &gID, &data, &processed, &createdAt); err != nil {
			return nil, err
		}
		out = append(out, EpisodeSummaryResume{
			UUID:      anyString(ep),
			GraphID:   anyString(gID),
			Data:      anyString(data),
			Processed: processed != 0,
			CreatedAt: anyString(createdAt),
		})
	}
	return &RawPage[EpisodeSummaryResume]{Data: out}, rows.Err()
}

// ===== Batch API =====

// BatchCreate replica batch.create.
func (c *Client) BatchCreate(metadata map[string]any) (*BatchListItem, error) {
	graphID := strOr(metadata["graph_id"])
	batchID, err := c.DB.CreateBatch(graphID, metadata)
	if err != nil {
		return nil, err
	}
	return &BatchListItem{BatchID: batchID, GraphID: graphID, Metadata: orEmpty(metadata)}, nil
}

// BatchAdded replica un item añadido con éxito.
type BatchAdded struct {
	EpisodeUUID   string
	SequenceIndex int
	Status        string
}

// BatchAdd replica batch.add.
func (c *Client) BatchAdd(batchID string, items []BatchItemInput) ([]BatchAdded, error) {
	uuids, err := c.DB.AddBatchItems(batchID, items)
	if err != nil {
		return nil, err
	}
	out := make([]BatchAdded, len(uuids))
	for i, u := range uuids {
		out[i] = BatchAdded{EpisodeUUID: u, SequenceIndex: i, Status: "succeeded"}
	}
	return out, nil
}

// BatchProcess replica batch.process.
func (c *Client) BatchProcess(batchID string) error {
	return c.DB.ProcessBatch(batchID)
}

// BatchGet replica _MockBatch.get — NotFoundError si no existe.
func (c *Client) BatchGet(batchID string) (*BatchSummary, error) {
	summary, err := c.DB.GetBatch(batchID)
	if err != nil {
		return nil, err
	}
	if summary == nil {
		return nil, &NotFoundError{"Batch not found"}
	}
	return summary, nil
}

// BatchList replica batch.list con paginación por cursor (B2).
func (c *Client) BatchList(limit, cursor int) (*BatchesPage, error) {
	limit = clampPageLimit(limit)
	items, err := c.DB.ListBatches(10000)
	if err != nil {
		return nil, err
	}
	page, next := paginateSlice(items, limit, cursor)
	return &BatchesPage{Batches: page, NextCursor: next}, nil
}

// BatchListItems replica batch.list_items (A4: .items/.next_cursor).
func (c *Client) BatchListItems(batchID string, limit, cursor int) (*BatchItemsPage, error) {
	limit = clampPageLimit(limit)
	items, err := c.DB.ListBatchItems(batchID)
	if err != nil {
		return nil, err
	}
	page, next := paginateSlice(items, limit, cursor)
	return &BatchItemsPage{Items: page, NextCursor: next}, nil
}

// ===== helpers de paginación =====

func clampPageLimit(limit int) int {
	if limit < 1 {
		limit = 1
	}
	if limit > defaultPageLimit {
		limit = defaultPageLimit
	}
	return limit
}

func clampIndex(i, max int) int {
	if i > max {
		return max
	}
	if i < 0 {
		return 0
	}
	return i
}

// paginateSlice corta la página [cursor, cursor+limit) sin salirse del slice.
// Antes, un cursor mayor que len(items) hacía items[cursor:] y provocaba un
// pánico (panic: slice bounds out of range) en cualquier petición paginada
// fuera de rango, por ejemplo list_items?cursor=500 de un batch pequeño.
func paginateSlice[T any](items []T, limit, cursor int) ([]T, *string) {
	start := clampIndex(cursor, len(items))
	end := clampIndex(start+limit, len(items))
	page := items[start:end]
	if page == nil {
		page = []T{}
	}
	if len(items) > end {
		s := itoa(end)
		return page, &s
	}
	return page, nil
}
