// Sesión multi-usuario: middleware que resuelve la identidad de cada petición.
//
// Tres formas de entrar (en orden):
//  1. ACCESS_KEY (clave maestra del .env) → principal admin completo, sin
//     aislamiento: demasiado peligroso para día a día, ideal para el server.
//  2. Token JWT (Bearer/X-Access-Key, dado por /api/auth/login|register).
//  3. Si ACCESS_KEY está VACÍA (modo desarrollo) y no hay token → principal
//     anónimo admin: mantiene la API abierta como antes sin bloquear nada.
//
// El principal viaja en el context; los handlers lo consumen via helpers.
package authx

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"mirofish/internal/users"
)

type ctxKey int

const principalKey ctxKey = 1

// Principal — identidad resuelta de una petición.
type Principal struct {
	User      *users.User // nil si master/anónimo
	Master    bool        // entró con ACCESS_KEY
	Anonymous bool        // modo dev: sin ACCESS_KEY y sin token
}

// Admin — master o un usuario con rol admin. Todos los datos accesibles.
func (p *Principal) Admin() bool {
	if p == nil {
		return false
	}
	if p.Master || p.Anonymous {
		return true
	}
	return p.User != nil && p.User.Role == users.RoleAdmin
}

// UserID — el ID del usuario aislado ("": admin/anónimo ve todo). Un rol admin
// con cuenta también ve todo, el aislamiento solo aplica a RoleUser.
func (p *Principal) UserID() string {
	if p == nil || p.User == nil || p.User.Role != users.RoleUser {
		return ""
	}
	return p.User.ID
}

// LLM — config LLM delegada del usuario (nil si no aplica). La config LLM
// global del .env se aplica a todos los demás casos.
func (p *Principal) LLM() *users.LLMCfg {
	if p == nil || p.User == nil {
		return nil
	}
	return p.User.LLM
}

// WithPrincipal guarda la identidad en el context de la petición.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFrom saca la identidad de una petición (nil si no pasó por el
// middleware: fallo de wiring).
func PrincipalFrom(r *http.Request) *Principal {
	if v, ok := r.Context().Value(principalKey).(*Principal); ok {
		return v
	}
	return nil
}

// SessionMiddleware resuelve principal→context. Escrihttp 401 si es necesario.
//
// store nil → fail-closed cuando ACCESS_KEY está puesta (no se puede verificar
// identidad sin BD); en dev (ACCESS_KEY vacía) sigue permitiendo anónimo.
func SessionMiddleware(store *users.Store, secret, masterKey string, exempt func(*http.Request) bool) func(http.Handler) http.Handler {
	required := masterKey != ""
	master := []byte(masterKey)

	unauthorized := func(w http.ResponseWriter, msg string) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "message": msg})
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if exempt != nil && exempt(r) {
				next.ServeHTTP(w, r)
				return
			}

			var p *Principal

			if required {
				p = principalFromKey(store, secret, master, r)
				if p == nil {
					unauthorized(w, "credenciales inválidas o ausentes (ACCESS_KEY / sesión)")
					return
				}
			} else {
				// Modo desarrollo (ACCESS_KEY vacía): token válido si lo hay;
				// si no, anónimo con poderes completos (compatibilidad con el
				// frontend actual y las pruebas).
				p = principalFromKey(store, secret, nil, r)
				if p == nil {
					p = &Principal{Anonymous: true}
				}
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}

// principalFromKey resuelve ACCESS_KEY → admin master, o token → usuario.
// master == nil ignora la clave maestra (modo dev). nil = falló.
func principalFromKey(store *users.Store, secret string, master []byte, r *http.Request) *Principal {
	provided := ExtractKey(r)

	if master != nil {
		if provided != "" && subtle.ConstantTimeCompare([]byte(provided), master) == 1 {
			return &Principal{Master: true}
		}
	}

	if provided != "" && secret != "" {
		if claims, err := users.ParseToken(secret, provided); err == nil {
			if store != nil {
				if u, err := store.Get(claims.Sub); err == nil && u.IsActive {
					return &Principal{User: u}
				}
			}
		}
	}

	return nil
}

// Logging note: el middleware no loggea (reqLogger lo hace en router).
// El guard de admin por ruta vive en cada blueprint (/api/admin/*, etc.).
