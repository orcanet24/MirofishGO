// Package testdb crea bases MySQL efímeras para las pruebas de integración.
//
// Cada prueba recibe un DSN con su propia base (creada y borrada al terminar),
// de modo que los paquetes pueden ejecutarse en paralelo sin pisarse.
//
// Sin servidor MySQL las pruebas se saltan con un mensaje claro: la suite no
// finge pasar contra SQLite. Para apuntar a otro servidor:
//
//	MIROFISH_TEST_DSN='user:pass@tcp(127.0.0.1:3307)/mirofish_test?charset=utf8mb4&parseTime=false'
package testdb

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

const defaultDSN = "mirofish:mirofish@tcp(127.0.0.1:3307)/mirofish_test?charset=utf8mb4&collation=utf8mb4_bin&parseTime=false"

// BaseDSN devuelve el DSN configurado para pruebas.
func BaseDSN() string {
	if v := strings.TrimSpace(os.Getenv("MIROFISH_TEST_DSN")); v != "" {
		return v
	}
	return defaultDSN
}

// New crea una base única y devuelve su DSN. La base se elimina con t.Cleanup.
func New(t testing.TB) string {
	t.Helper()
	base := BaseDSN()
	name := fmt.Sprintf("mirofish_test_%d", time.Now().UnixNano())

	admin, err := sql.Open("mysql", base)
	if err != nil {
		t.Fatalf("testdb: DSN inválido: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		t.Skipf("testdb: MySQL no disponible en %s (%v); arranca la instancia de pruebas o define MIROFISH_TEST_DSN", base, err)
	}

	dsn := withDatabase(base, name)
	t.Cleanup(func() {
		conn, err := sql.Open("mysql", base)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Exec("DROP DATABASE IF EXISTS `" + name + "`")
	})
	return dsn
}

// withDatabase sustituye el nombre de base del DSN conservando los parámetros.
func withDatabase(dsn, name string) string {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return dsn
	}
	if q := strings.Index(dsn, "?"); q > slash {
		return dsn[:slash+1] + name + dsn[q:]
	}
	return dsn[:slash+1] + name
}
