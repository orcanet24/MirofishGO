// Utilidades de paginación/ruta del paquete graph.
package graph

import (
	"fmt"
	"strconv"

	"mirofish/internal/config"
)

// DefaultDSN devuelve la conexión MySQL configurada en .env (DB_DSN o las
// variables DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME).
func DefaultDSN() string {
	return config.Load().DBDSN
}

// unwrapPtr devuelve puntero a v.
func unwrapPtr[T any](v T) *T { return &v }

// fmtError construye un error simple.
func fmtError(format string, args ...any) error { return fmt.Errorf(format, args...) }

// pageOf replica el corte con cursor y el header zep-next-cursor:
// data = items[offset:offset+limit]; header solo si total > offset+limit.
func pageOf[T any](items []T, limit, offset int) *RawPage[T] {
	if offset < 0 {
		offset = 0
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	if offset > len(items) {
		offset = len(items)
	}
	page := items[offset:end]
	if page == nil {
		page = []T{}
	}
	var next string
	if len(items) > offset+limit {
		next = strconv.Itoa(offset + limit)
	}
	return &RawPage[T]{Data: page, NextCursor: next}
}

func itoa(i int) string { return strconv.Itoa(i) }

// NewMiroFishGraphID replica el formato de GraphBuilderService.create_graph:
// "mirofish_" + uuid4().hex[:16].
func NewMiroFishGraphID() string { return "mirofish_" + hex16() }
