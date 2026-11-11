// Package dbx — compatibilidad de motor de BD.
//
// El binario go guarda todo en MySQL 8/MariaDB. La variante personal SQLite
// (cmd/mirofish-personal-sqlite) activa el driver sqlite con dbx.Set antes de
// abrir la BD; a partir de ahí los paquetes graph/oasis/httpx preguntan a
// Active() para elegir esquema y sentencias SQL que difieren entre motores.
//
// Diferencias cubiertas aquí:
//   - INSERT IGNORE          → INSERT OR IGNORE
//   - error 1062 (dup entry) → mensaje "UNIQUE constraint failed"
//   - GREATEST(a, b) → max(a, b)
//   - ON DUPLICATE KEY UPDATE→ ON CONFLICT ... DO UPDATE
package dbx

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// Driver identifica el motor de BD activo.
type Driver string

const (
	// MySQL es el motor por defecto (todos los binarios).
	MySQL Driver = "mysql"
	// SQLite es la variante del binario personal sin MySQL.
	SQLite Driver = "sqlite"
)

var (
	activeMu sync.RWMutex
	active   = MySQL
)

// Active devuelve el motor activo. Por defecto MySQL; solo la variante
// SQLite personal lo cambia (y una sola vez, antes de abrir la BD).
func Active() Driver {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return active
}

// Set fija el motor activo. Debe llamarse antes de abrir la BD: el esquema
// se crea una vez con este valor.
func Set(d Driver) {
	activeMu.Lock()
	defer activeMu.Unlock()
	active = d
}

// InsertIgnore prefijo de INSERT blindado contra duplicados.
func InsertIgnore() string {
	if Active() == SQLite {
		return "INSERT OR IGNORE INTO"
	}
	return "INSERT IGNORE INTO"
}

// IsDuplicate reporta si err es una infracción de clave única (duplicado).
// MySQL: error 1062/ER_DUP_ENTRY. SQLite (modernc): "UNIQUE constraint failed".
func IsDuplicate(err error) bool {
	if err == nil {
		return false
	}
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) && (mysqlErr.Number == 1062 || strings.Contains(mysqlErr.Message, "Duplicate entry")) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") ||
		strings.Contains(strings.ToLower(err.Error()), "constraint failed: unique")
}

// Greatest delimita dos expresiones evitando que el resultado baje de 0.
// MySQL usa GREATEST; SQLite solo tiene la función escalar max(a, b).
func Greatest(expr1, expr2 string) string {
	if Active() == SQLite {
		return fmt.Sprintf("max(%s, %s)", expr1, expr2)
	}
	return fmt.Sprintf("GREATEST(%s, %s)", expr1, expr2)
}
