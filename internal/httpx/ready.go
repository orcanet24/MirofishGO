// Readiness de arranque: MySQL accesible y esquema completo.
//
// A diferencia de /health (proceso vivo), /ready solo responde 200 cuando la
// base responde y existen todas las tablas que el binario necesita. Un
// orquestador puede usarlo para no enviar tráfico a una instancia que arrancó
// pero no puede atender /api/*.
package httpx

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"mirofish/internal/graph"
)

// requiredTables son las tablas que crean el grafo, OASIS y usuarios al arrancar.
var requiredTables = []string{
	"graphs", "nodes", "edges", "episodes", "batches", "batch_items",
	"oasis_users", "oasis_posts", "oasis_comments", "oasis_follows", "oasis_likes",
	"oasis_traces", "oasis_rec", "oasis_actions", "oasis_runs",
	"users",
}

// newReadinessHandler devuelve el handler de /ready. dbErr es el fallo de
// conexión detectado al montar el router; si existe, no hay client.
func newReadinessHandler(client *graph.Client, dbErr error, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")

		if client == nil || client.DB == nil {
			if dbErr != nil {
				logger.Error("readiness: sin conexión MySQL", "error", dbErr)
			}
			JSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "not_ready",
				"checks": map[string]any{
					"database": "error",
					"schema":   "unknown",
				},
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		db := client.DB.SQL()
		if err := db.PingContext(ctx); err != nil {
			logger.Error("readiness: MySQL no responde", "error", err)
			JSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "not_ready",
				"checks": map[string]any{
					"database": "error",
					"schema":   "unknown",
				},
			})
			return
		}

		missing, err := missingTables(ctx, db)
		if err != nil {
			logger.Error("readiness: no se pudo leer el esquema", "error", err)
			JSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "not_ready",
				"checks": map[string]any{
					"database": "ok",
					"schema":   "error",
				},
			})
			return
		}
		if len(missing) > 0 {
			logger.Error("readiness: faltan tablas", "missing", missing)
			JSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "not_ready",
				"checks": map[string]any{
					"database":       "ok",
					"schema":         "missing_tables",
					"missing_tables": missing,
				},
			})
			return
		}

		JSON(w, http.StatusOK, map[string]any{
			"status": "ready",
			"checks": map[string]any{
				"database": "ok",
				"schema":   "ok",
			},
		})
	}
}

// missingTables compara las tablas esperadas con information_schema.
func missingTables(ctx context.Context, db *sql.DB) ([]string, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(requiredTables)), ",")
	args := make([]any, 0, len(requiredTables))
	for _, table := range requiredTables {
		args = append(args, table)
	}
	rows, err := db.QueryContext(ctx,
		"SELECT table_name FROM information_schema.tables "+
			"WHERE table_schema = DATABASE() AND table_name IN ("+placeholders+")",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	present := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	missing := []string{}
	for _, table := range requiredTables {
		if !present[table] {
			missing = append(missing, table)
		}
	}
	return missing, nil
}
