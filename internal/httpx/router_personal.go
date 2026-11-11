// PersonalRouter — binario personal: acceso por ACCESS_KEY (como el binario
// original), sin cuentas de usuario ni panel admin. Los blueprints de datos
// son los mismos; al no haber principal de usuario, todo el scope es "" (ve
// todos los proyectos) y el LLM es el global del .env.
package httpx

import (
	"log/slog"
	"net/http"
	"strings"

	"mirofish/internal/authx"
	"mirofish/internal/config"
	"mirofish/internal/dbx"
	"mirofish/internal/graph"
	"mirofish/internal/oasis"
	"mirofish/internal/webui"
)

func PersonalRouter(c *config.Config, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"service": "MiroFish Personal (Go binary)",
		})
	})

	client, dbErr := graph.NewClient("")
	if dbErr != nil {
		logger.Error("no se pudo conectar a la BD; los blueprints responderán 500", "error", dbErr)
	}
	mux.HandleFunc("GET /ready", newReadinessHandler(client, dbErr, logger))

	if dbx.Active() == dbx.SQLite {
		// Variante SQLite personal: ajustes LLM editables desde la app
		// (botón ⚙ →/api/config) y esquema social disponible desde el
		// arranque para que /ready esté verde.
		if client != nil && client.DB != nil {
			if _, err := oasis.NewStore(client.DB.SQL()); err != nil {
				logger.Error("no se pudo inicializar el esquema OASIS", "error", err)
			}
		}
		registerSQLiteSettingsRoutes(mux, client, c, logger)
		activateSQLiteFrontend()
	}

	registerGraphRoutes(mux, c, logger, client, dbErr, nil)
	registerSimulationRoutes(mux, c, logger, client, dbErr)
	registerReportRoutes(mux, c, logger, client, dbErr)

	// Sonda de modo personal para el frontend: 200 informativo. La SPA
	// decide por la presencia de mirofish_access_key en localStorage (la
	// siembra el script inyectado); la clave real se valida vía /api/auth/validate.
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"data":   map[string]any{"authenticated": false, "personal": true},
		})
	})

	// Validación real de la ACCESS_KEY: queda bajo /api/*, así que el
	// middleware la exige. 200 = clave válida; 401 = inválida.
	mux.HandleFunc("GET /api/auth/validate", func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, map[string]any{"status": "success", "data": map[string]any{"valid": true}})
	})

	mux.Handle("/", webui.Handler())

	var root http.Handler = mux
	root = reqLogger(logger, root)
	root = cors(root)
	root = authx.Middleware(c.AccessKey, func(r *http.Request) bool {
		return personalPublicPath(r.URL.Path)
	})(root)
	return root
}

// personalPublicPath — el sonda /api/auth/me queda pública (responde
// personal:true con o sin clave; la clave se valida con una petición con
// X-Access-Key, que el middleware deja pasar por el handler).
func personalPublicPath(p string) bool {
	switch p {
	case "/health", "/ready", "/api/auth/me":
		return true
	}
	return p != "/api" && !strings.HasPrefix(p, "/api/")
}
