package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func request(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestSPAIndexAndFallback(t *testing.T) {
	h := Handler()

	rec := request(t, h, http.MethodGet, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `<div id="app">`) {
		t.Error("el index no contiene el mount point de Vue")
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control index = %q", rec.Header().Get("Cache-Control"))
	}

	rec = request(t, h, http.MethodGet, "/proyecto/abc/detalle")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<div id="app">`) {
		t.Fatalf("fallback SPA = %d", rec.Code)
	}
}

func TestAssetsAndAPI404(t *testing.T) {
	h := Handler()

	rec := request(t, h, http.MethodGet, "/icon.png")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("icon.png = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	entries, err := fs.ReadDir(embedded, "dist/assets")
	if err != nil {
		t.Fatal(err)
	}
	var js string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			js = entry.Name()
			break
		}
	}
	if js == "" {
		t.Fatal("el build no contiene JS")
	}
	rec = request(t, h, http.MethodGet, "/assets/"+js)
	if rec.Code != http.StatusOK {
		t.Fatalf("asset %s = %d", js, rec.Code)
	}
	if cache := rec.Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Errorf("Cache-Control asset = %q", cache)
	}

	rec = request(t, h, http.MethodGet, "/api/ruta-inexistente")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"not found"`) {
		t.Fatalf("API inexistente = %d %s", rec.Code, rec.Body.String())
	}
	rec = request(t, h, http.MethodGet, "/assets/no-existe.js")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("asset inexistente = %d", rec.Code)
	}
	rec = request(t, h, http.MethodPost, "/")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / = %d", rec.Code)
	}
}
