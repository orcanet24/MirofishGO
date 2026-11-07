// Package webui incrusta el build de Vite en el binario Go y lo sirve como
// SPA. Las rutas /api/* nunca caen en el fallback: si no las atiende un
// blueprint, devuelven 404 JSON en vez de HTML.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

//go:embed dist
var embedded embed.FS

// mimeFix fuerza los MIME de los assets de la SPA. En Windows mime.TypeByExtension
// consulta el registro (HKCR\.ext "Content Type"): si una instalación lo dejó
// mal (p. ej. .js → application/x-js), el navegador rechaza el script/stylesheet
// por strict MIME checking y la SPA queda en blanco.
func mimeFix() {
	for _, m := range []struct{ ext, typ string }{
		{".js", "text/javascript; charset=utf-8"},
		{".mjs", "text/javascript; charset=utf-8"},
		{".css", "text/css; charset=utf-8"},
		{".json", "application/json"},
		{".png", "image/png"},
		{".jpg", "image/jpeg"},
		{".jpeg", "image/jpeg"},
		{".gif", "image/gif"},
		{".svg", "image/svg+xml"},
		{".webp", "image/webp"},
		{".woff", "font/woff"},
		{".woff2", "font/woff2"},
		{".ttf", "font/ttf"},
		{".ico", "image/x-icon"},
		{".html", "text/html; charset=utf-8"},
	} {
		_ = mime.AddExtensionType(m.ext, m.typ)
	}
}

// Handler sirve los assets y el index.html de la SPA.
func Handler() http.Handler {
	mimeFix()
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		// El embed garantiza que dist exista; un fallo aquí es un build roto.
		panic("webui: build del frontend ausente: " + err.Error())
	}
	files := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		cleaned := path.Clean(r.URL.Path)
		if cleaned == "/api" || strings.HasPrefix(cleaned, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":"error","message":"not found"}`))
			return
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")

		if cleaned == "/" || cleaned == "/index.html" {
			serveIndex(w, r, dist)
			return
		}

		rel := strings.TrimPrefix(cleaned, "/")
		if info, err := fs.Stat(dist, rel); err == nil && !info.IsDir() {
			if strings.HasPrefix(rel, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=86400")
			}
			files.ServeHTTP(w, r)
			return
		}

		// Un asset inexistente es 404, no la SPA: evita que el navegador
		// interprete HTML como JavaScript.
		if path.Ext(cleaned) != "" {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, r, dist)
	})
}

// assetVersion identifica el build: al cambiar, invalida las entradas de caché
// del navegador. Útil p. ej. cuando la caché guardó responses con MIME roto
// (servidas antes de mimeFix): sin esto, un Google Chrome con max-age+immutable
// seguiría mostrando la página en blanco hasta un Ctrl+F5 o limpiar caché.
const assetVersion = "mimefix-1"

var assetRef = regexp.MustCompile(`(?i)(src|href)=("|')(/assets/[^"']+\.(?:js|css))("|')`)

func serveIndex(w http.ResponseWriter, r *http.Request, dist fs.FS) {
	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Añade ?v=<assetVersion> a las referencias de assets: nueva clave de
	// caché, se pide la respuesta fresca del servidor.
	data = assetRef.ReplaceAll(data, []byte(`$1=$2$3?v=`+assetVersion+`$4`))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(data))
}
