// Blueprint de autenticación: registro público, login, perfil y cambio de
// contraseña. Sin sesiones de servidor: JWT HS256 (users.NewToken) firmado con
// SECRET_KEY; el frontend lo envía como Authorization: Bearer / X-Access-Key.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"mirofish/internal/locale"
	"mirofish/internal/models"
	"mirofish/internal/users"
)

type AuthHandler struct {
	Store  *users.Store
	Secret string
	Logger *slog.Logger

	// PM — para impedir borrar la cuenta si quedan estudios sin purgar.
	PM *models.ProjectManager
}

func (h *AuthHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/register", h.register)
	mux.HandleFunc("POST /api/auth/login", h.login)
	mux.HandleFunc("GET /api/auth/me", h.me)
	mux.HandleFunc("POST /api/auth/password", h.changePassword)
	mux.HandleFunc("DELETE /api/auth/account", h.deleteAccount)
}

// tokenResponse — shape común de register/login/me.
func tokenResponse(token string, u *users.User) map[string]any {
	return map[string]any{
		"token": token,
		"user":  u.PublicDict(),
	}
}

func (h *AuthHandler) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email         string `json:"email"`
		Password      string `json:"password"`
		LegalAccepted bool   `json:"legal_accepted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		HTTPError(w, http.StatusBadRequest, "cuerpo JSON inválido")
		return
	}
	if !body.LegalAccepted {
		HTTPError(w, http.StatusBadRequest, locale.T(locale.Normalize(r.Header.Get("Accept-Language")), "api.legalRequired", nil))
		return
	}
	u, err := h.Store.Create(body.Email, body.Password)
	if err == nil {
		if _, err := h.Store.MarkLegalAccepted(u.ID); err != nil {
			h.Logger.Error("no se pudo registrar la aceptación legal", "user", u.ID, "error", err)
		}
	}
	h.respond(w, err, u)
}

func (h *AuthHandler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		HTTPError(w, http.StatusBadRequest, "cuerpo JSON inválido")
		return
	}
	u, err := h.Store.Authenticate(body.Email, body.Password)
	h.respond(w, err, u)
}

func (h *AuthHandler) respond(w http.ResponseWriter, err error, u *users.User) {
	switch {
	case err == nil:
	case err == users.ErrEmailTaken:
		HTTPError(w, http.StatusConflict, err.Error())
		return
	case err == users.ErrBadCredentials:
		HTTPError(w, http.StatusUnauthorized, "email o contraseña incorrectos")
		return
	case err == users.ErrInactive:
		HTTPError(w, http.StatusForbidden, err.Error())
		return
	default:
		h.Logger.Error("auth error", "error", err)
		HTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	token, err := users.NewToken(h.Secret, u)
	if err != nil {
		h.Logger.Error("no se pudo firmar el token", "error", err)
		HTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"data":   tokenResponse(token, u),
	})
}

// me — datos del usuario del token. Sin usuario real (master/anónimo dev)
// responde authenticated:false de forma informativa (200): el frontend puede
// preguntar "quién soy" sin token. Con dev:true, el backend está en modo
// desarrollo (ACCESS_KEY vacía) y la API no exige sesión.
func (h *AuthHandler) me(w http.ResponseWriter, r *http.Request) {
	// Clave maestra (ACCESS_KEY) en el binario multi-usuario: acceso completo
	// de admin sin cuenta. El frontend lo trata como sesión completa.
	if p := principal(r); p != nil && p.Master {
		JSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"data":   map[string]any{"authenticated": true, "master": true},
		})
		return
	}
	u := currentUser(r)
	if u == nil {
		data := map[string]any{"authenticated": false}
		if p := principal(r); p != nil && p.Anonymous {
			data["dev"] = true
		}
		JSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"data":   data,
		})
		return
	}
	if u.PasswordHash != "" && scopeID(r) != u.ID {
		h.Logger.Debug("me consultado por principal admin", "target", u.ID)
	}
	JSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"data":   withAuthenticated(tokenResponse("", u)),
	})
}

func withAuthenticated(inner map[string]any) map[string]any {
	copy := map[string]any{
		"token":         inner["token"],
		"user":          inner["user"],
		"authenticated": true,
	}
	if copy["token"] == "" {
		copy["token"] = nil // /me no re-entrega el token del reqLogger
	}
	return copy
}

func (h *AuthHandler) changePassword(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		HTTPError(w, http.StatusBadRequest, "solo para usuarios con sesión")
		return
	}
	var body struct {
		Current string `json:"current"`
		New     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		HTTPError(w, http.StatusBadRequest, "cuerpo JSON inválido")
		return
	}
	if u.PasswordHash != "" && !storeCheck(h.Store, u, body.Current) {
		HTTPError(w, http.StatusUnauthorized, "contraseña actual incorrecta")
		return
	}
	if _, err := h.Store.SetPassword(u.ID, body.New); err != nil {
		h.Logger.Error("cambio de contraseña falló", "user", u.ID, "error", err)
		HTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	JSON(w, http.StatusOK, map[string]any{"status": "success"})
}

// deleteAccount — borra la cuenta del usuario autenticado (y sus datos).
// Exige que no queden estudios: primero hay que purgar con purge-all.
func (h *AuthHandler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		HTTPError(w, http.StatusBadRequest, "solo para usuarios con sesión")
		return
	}
	if h.PM != nil && len(h.PM.ListProjects(1000, u.ID)) > 0 {
		HTTPError(w, http.StatusConflict, locale.T(locale.Normalize(r.Header.Get("Accept-Language")), "ui.deleteAccountNeedPurge", nil))
		return
	}
	if err := h.Store.Delete(u.ID); err != nil {
		h.Logger.Error("no se pudo borrar la cuenta", "user", u.ID, "error", err)
		HTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Logger.Info("cuenta eliminada por el usuario", "user", u.ID)
	JSON(w, http.StatusOK, map[string]any{"status": "success"})
}

func storeCheck(s *users.Store, u *users.User, current string) bool {
	// verificar explícitamente la contraseña actual contra la BD (por si
	// el hash del principal hubiera cambiado).
	srv, err := s.Authenticate(u.Email, current)
	return err == nil && srv.ID == u.ID
}
