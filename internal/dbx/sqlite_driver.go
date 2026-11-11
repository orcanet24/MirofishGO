//go:build mirofish_sqlite

// Variante personal SQLite: registra el driver "sqlite" (modernc, puro Go,
// sin CGO, genera un exe estático para Windows). Solo se enlaza cuando el
// binario se compila con `-tags mirofish_sqlite`; los binarios MySQL no
// arrastran la dependencia.
package dbx

import (
	// Driver SQLite puro Go (registrado como "sqlite" en database/sql).
	_ "modernc.org/sqlite"
)
