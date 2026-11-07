// Package graph — port de backend/app/utils/local_graph.py (tipos).
//
// Backend de grafo local MySQL compatible con la interfaz del SDK de Zep
// Cloud. Diferencias admitidas (idénticas al original Python): búsqueda
// léxica FULLTEXT/LIKE sin embeddings, reranker ignorado, extracción de
// entidades vía LLM, sin invalidación temporal automática.
package graph

// LocalNode — nodo compatible con la interfaz de Zep.
type LocalNode struct {
	UUID       string         `json:"-"`
	Name       string         `json:"-"`
	Labels     []string       `json:"-"`
	Summary    string         `json:"-"`
	Attributes map[string]any `json:"-"`
	CreatedAt  *string        `json:"-"`
	GraphID    *string        `json:"-"`
}

// LocalEdge — arista compatible con la interfaz de Zep.
type LocalEdge struct {
	UUID           string         `json:"-"`
	Name           string         `json:"-"`
	Fact           string         `json:"-"`
	SourceNodeUUID string         `json:"-"`
	TargetNodeUUID string         `json:"-"`
	Attributes     map[string]any `json:"-"`
	CreatedAt      *string        `json:"-"`
	ValidAt        *string        `json:"-"`
	InvalidAt      *string        `json:"-"`
	ExpiredAt      *string        `json:"-"`
	GraphID        *string        `json:"-"`
}

// LocalEpisode — episodio compatible con la interfaz de Zep.
type LocalEpisode struct {
	UUID              string         `json:"-"`
	GraphID           string         `json:"-"`
	Data              string         `json:"-"`
	DataType          string         `json:"-"`
	Processed         bool           `json:"-"`
	SourceDescription string         `json:"-"`
	Metadata          map[string]any `json:"-"`
	CreatedAt         *string        `json:"-"`
}

// RawPage replica _MockRawResponse: .data + headers de cursor de paginación.
// NextCursor vacío = no hay header zep-next-cursor.
type RawPage[T any] struct {
	Data       []T
	NextCursor string
}

// SearchResult replica _MockSearchResponse.
type SearchResult struct {
	Edges []LocalEdge
	Nodes []LocalNode
}

// NotFoundError — error "no encontrado" que los servicios atrapan por tipo
// (equivalente a zep_cloud.NotFoundError).
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string { return e.Msg }

// BatchItemRecord replica cada item de list_batch_items (SimpleNamespace).
type BatchItemRecord struct {
	EpisodeUUID   string
	SourceUUID    string
	SequenceIndex int
	Status        string
	Error         *string
	GraphID       string
	Data          string
}

// BatchItemsPage replica _MockListResponse (.items / .next_cursor).
type BatchItemsPage struct {
	Items      []BatchItemRecord
	NextCursor *string
}

// BatchProgress replica SimpleNamespace(percent_complete, succeeded_items).
type BatchProgress struct {
	PercentComplete float64
	SucceededItems  int
}

// BatchSummary replica el resumen de get_batch (A3).
type BatchSummary struct {
	BatchID   string
	Status    string
	Metadata  map[string]any
	ItemCount int
	Progress  BatchProgress
}

// BatchListItem replica los items de list_batches (B2).
type BatchListItem struct {
	BatchID  string
	GraphID  string
	Metadata map[string]any
	Status   string
}

// BatchesPage replica _MockBatchesPage (.batches / .next_cursor).
type BatchesPage struct {
	Batches    []BatchListItem
	NextCursor *string
}

// GraphRow es la fila cruda de la tabla graphs.
type GraphRow struct {
	GraphID     string
	Name        string
	Description string
	Ontology    string // JSON en crudo dentro de la columna
	CreatedAt   string
}

// maxSearchLimit replica MAX_SEARCH_LIMIT.
const maxSearchLimit = 50

// defaultPageLimit replica _DEFAULT_PAGE_LIMIT.
const defaultPageLimit = 100
