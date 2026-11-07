// Tests de /ready: MySQL accesible y esquema completo.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"mirofish/internal/graph"
	"mirofish/internal/oasis"
	"mirofish/internal/testdb"
)

func TestReadinessOKAndMissingTables(t *testing.T) {
	client, err := graph.NewClient(testdb.New(t))
	if err != nil {
		t.Fatalf("graph client: %v", err)
	}
	t.Cleanup(func() { _ = client.DB.Close() })
	if _, err := oasis.NewStore(client.DB.SQL()); err != nil {
		t.Fatalf("oasis store: %v", err)
	}

	handler := newReadinessHandler(client, nil, slog.Default())
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	checks, _ := body["checks"].(map[string]any)
	if body["status"] != "ready" || checks["database"] != "ok" || checks["schema"] != "ok" {
		t.Fatalf("ready body = %v", body)
	}

	// Quitar una tabla debe degradar a 503 y nombrarla, sin filtrar el DSN.
	if _, err := client.DB.SQL().Exec("DROP TABLE oasis_runs"); err != nil {
		t.Fatalf("drop oasis_runs: %v", err)
	}
	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready sin tabla: %d %s", rec.Code, rec.Body.String())
	}
	body = map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	checks, _ = body["checks"].(map[string]any)
	missing, _ := checks["missing_tables"].([]any)
	if len(missing) != 1 || missing[0] != "oasis_runs" {
		t.Fatalf("missing_tables = %v", checks["missing_tables"])
	}
}

func TestReadinessWithoutDatabase(t *testing.T) {
	handler := newReadinessHandler(nil, errors.New("connection refused"), slog.Default())
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready sin BD: %d", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
}
