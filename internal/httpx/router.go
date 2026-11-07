// Package httpx — servidor HTTP equivalente a create_app() de Flask.
//
// Enruta los tres blueprints porteados (/api/graph, /api/simulation,
// /api/report), aplica CORS para /api/*, registra cada petición y aplica la
// única condición de acceso: la clave maestra (ACCESS_KEY).
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"mirofish/internal/authx"
	"mirofish/internal/config"
	"mirofish/internal/graph"
	"mirofish/internal/webui"
)

// JSON escribe un objeto JSON con el código indicado (ensure_ascii=False,
// como Flask lo configura: JSON directo UTF-8).
func JSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

// HTTPError replica el patrón Flask de error JSON.
func HTTPError(w http.ResponseWriter, code int, message string) {
	JSON(w, code, map[string]any{"status": "error", "message": message})
}

// NotFound replica el 404 JSON de Flask cuando no hay ruta (Flask devuelve HTML,
// pero consumimos el backend desde el frontend con fetch: JSON es más útil).
func NotFound(w http.ResponseWriter, _ *http.Request) {
	HTTPError(w, http.StatusNotFound, "not found")
}

// reqLogger replica el middleware before_request/after_request de Flask.
func reqLogger(l *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		// Info, no Debug: cada operación HTTP debe verse en consola (la consola
		// filtra por LOG_LEVEL, que por defecto es info).
		l.Info("petición",
			"method", r.Method, "path", r.URL.Path,
			"status", sw.status, "dur", time.Since(start).Round(time.Millisecond))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// cors replica flask_cors con resources={r"/api/*": {"origins": "*"}}.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			origin := r.Header.Get("Origin")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Vary", "Origin")
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Access-Key")
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Router monta el árbol de rutas completo del backend portado.
// Los blueprints se registran por paquete; los aún no porteados devuelven
// 501 con un mensaje claro hasta implementarse (hito a hito).
func Router(c *config.Config, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	// Health público (unauthenticated) — equivale a @app.route('/health').
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"service": "MiroFish Backend (Go binary)",
		})
	})

	// Un único pool MySQL para los tres blueprints: abrir uno por handler
	// multiplicaría las conexiones (20 por pool) sin necesidad.
	client, dbErr := graph.NewClient("")
	if dbErr != nil {
		logger.Error("no se pudo conectar a MySQL; los blueprints responderán 500", "error", dbErr)
	}

	// Readiness público: proceso vivo + MySQL + esquema completo.
	mux.HandleFunc("GET /ready", newReadinessHandler(client, dbErr, logger))

	// Blueprints (registrar progresivamente en darle a cada paquete su API):
	registerGraphRoutes(mux, c, logger, client, dbErr)
	registerSimulationRoutes(mux, c, logger, client, dbErr)
	registerReportRoutes(mux, c, logger, client, dbErr)

	// La SPA embebida es el fallback: las rutas /api/* más específicas siguen
	// ganando y un /api/* desconocido devuelve 404 JSON, no HTML.
	mux.Handle("/", webui.Handler())

	var root http.Handler = mux
	root = reqLogger(logger, root)
	root = cors(root)
	// Última capa: la clave única de acceso sobre todo lo protegido. La UI y
	// sus assets son públicos; la clave solo se exige a /api/*.
	root = authx.Middleware(c.AccessKey, func(r *http.Request) bool {
		return publicPath(r.URL.Path)
	})(root)
	return root
}

// publicPath exime healthchecks y la SPA; todo /api/* sigue protegido.
func publicPath(p string) bool {
	if p == "/health" || p == "/ready" {
		return true
	}
	return p != "/api" && !strings.HasPrefix(p, "/api/")
}

// registerUnavailable deja el blueprint en 500 con la causa real (BD caída).
func registerUnavailable(mux *http.ServeMux, prefix string, err error) {
	mux.HandleFunc(prefix, func(w http.ResponseWriter, _ *http.Request) {
		HTTPError(w, http.StatusInternalServerError, "blueprint no disponible: "+err.Error())
	})
}

// registerGraphRoutes monta /api/graph/... (port de api/graph.py).
func registerGraphRoutes(mux *http.ServeMux, c *config.Config, logger *slog.Logger, client *graph.Client, dbErr error) {
	if dbErr != nil {
		registerUnavailable(mux, "/api/graph/", dbErr)
		return
	}
	h, err := NewGraphHandler(c, logger, WithGraphClient(client))
	if err != nil {
		logger.Error("no se pudo iniciar el blueprint /api/graph", "error", err)
		mux.HandleFunc("/api/graph/", func(w http.ResponseWriter, r *http.Request) {
			HTTPError(w, http.StatusInternalServerError,
				"blueprint /api/graph no disponible: "+err.Error())
		})
		return
	}
	h.Register(mux)
}

func registerSimulationRoutes(mux *http.ServeMux, c *config.Config, logger *slog.Logger, client *graph.Client, dbErr error) {
	if dbErr != nil {
		registerUnavailable(mux, "/api/simulation/", dbErr)
		return
	}
	h, err := NewSimulationHandler(c, logger, WithSimulationGraphClient(client))
	if err != nil {
		logger.Error("no se pudo iniciar el blueprint /api/simulation", "error", err)
		mux.HandleFunc("/api/simulation/", func(w http.ResponseWriter, _ *http.Request) {
			HTTPError(w, http.StatusInternalServerError,
				"blueprint /api/simulation no disponible: "+err.Error())
		})
		return
	}
	h.Register(mux)
}

func registerReportRoutes(mux *http.ServeMux, c *config.Config, logger *slog.Logger, client *graph.Client, dbErr error) {
	if dbErr != nil {
		registerUnavailable(mux, "/api/report/", dbErr)
		return
	}
	h, err := NewReportHandler(c, logger, WithReportGraphClient(client))
	if err != nil {
		logger.Error("no se pudo iniciar el blueprint /api/report", "error", err)
		mux.HandleFunc("/api/report/", func(w http.ResponseWriter, _ *http.Request) {
			HTTPError(w, http.StatusInternalServerError,
				"blueprint /api/report no disponible: "+err.Error())
		})
		return
	}
	h.Register(mux)
}
