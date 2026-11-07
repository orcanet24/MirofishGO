// Package authx — única condición de acceso del binario: una clave maestra.
//
// La clave se define en el .env (ACCESS_KEY=...). Si está vacía, la
// autenticación queda desactivada (modo desarrollo). Todas las rutas /api/*
// la exigen; /health y /ready se publican sin clave para monitorización.
//
// La clave se acepta por tres vías, en orden de preferencia:
//   - header "X-Access-Key: <clave>"
//   - header "Authorization: Bearer <clave>"
//   - query  "?access_key=<clave>"  (para EventSource/descargas directas)
//
// La comparación es constant-time (crypto/subtle) para evitar timing attacks.
package authx

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// ExtractKey obtiene la clave de la petición desde header, Authorization o query.
func ExtractKey(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("X-Access-Key")); k != "" {
		return k
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); auth != "" {
		if k, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return strings.TrimSpace(k)
		}
	}
	if k := strings.TrimSpace(r.URL.Query().Get("access_key")); k != "" {
		return k
	}
	return ""
}

// Middleware exige la clave en cada ruta protegida. El frontend embebido
// (sin clave) sirve la UI, que pide la clave una vez y la guarda en
// localStorage; /health y /ready quedan públicos para healthchecks.
func Middleware(accessKey string, exemptFn func(*http.Request) bool) func(http.Handler) http.Handler {
	required := accessKey != ""
	password := []byte(accessKey)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !required || (exemptFn != nil && exemptFn(r)) {
				next.ServeHTTP(w, r)
				return
			}
			provided := ExtractKey(r)
			if provided == "" || subtle.ConstantTimeCompare([]byte(provided), password) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="mirofish"`)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":  "error",
					"message": "clave de acceso inválida o ausente (X-Access-Key / Authorization: Bearer / ?access_key=)",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
