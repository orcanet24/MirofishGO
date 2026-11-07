// Helpers de principal para los blueprints HTTP. Toda la capa API usa:
//  - scopeUserID(r): ID del usuario aislado ("": ve todo)
//  - isAdmin(r): acceso completo
//  - llmFactory(r): factory LLM delegada del usuario, si la hay
package httpx

import (
	"mirofish/internal/authx"
	"mirofish/internal/llm"
	"mirofish/internal/users"
	"net/http"
)

func principal(r *http.Request) *authx.Principal { return authx.PrincipalFrom(r) }

// scopeID — ID del usuario por el que se filtra todo lo persistente.
func scopeID(r *http.Request) string { return authx.PrincipalFrom(r).UserID() }

// isAdmin — principal con acceso completo (master, anónimo o rol admin).
func isAdmin(r *http.Request) bool { return authx.PrincipalFrom(r).Admin() }

// isAdminGuard escribe 403 y devuelve false si el principal no es admin.
func isAdminGuard(w http.ResponseWriter, r *http.Request) bool {
	if isAdmin(r) {
		return true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"status":"error","message":"requiere rol de administrador"}`))
	return false
}

// llmFactory — factory LLM con la config delegada del usuario de la petición;
// nil si el usuario no tiene config propia (el handler usará la global).
func llmFactory(r *http.Request) func() (*llm.Client, error) {
	p := principal(r)
	if p == nil || p.LLM() == nil {
		return nil
	}
	cfg := p.LLM()
	key, base, model := "", "", ""
	if cfg.APIKey != nil {
		key = *cfg.APIKey
	}
	if cfg.BaseURL != nil {
		base = *cfg.BaseURL
	}
	if cfg.Model != nil {
		model = *cfg.Model
	}
	return func() (*llm.Client, error) { return llm.NewClient(key, base, model) }
}

// currentUser — usuario autenticado real de esta petición (nil si anon/master).
func currentUser(r *http.Request) *users.User {
	return principal(r).User
}
