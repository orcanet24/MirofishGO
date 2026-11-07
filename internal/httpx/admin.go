// Blueprint de administración: gestionar usuarios, planes/créditos y la
// configuración LLM delegada de cada cuenta. Todo exige rol admin.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"mirofish/internal/models"
	"mirofish/internal/users"
)

type AdminHandler struct {
	Store  *users.Store
	PM     *models.ProjectManager // para proteger borrados con proyectos vivos
	Logger *slog.Logger
}

func (h *AdminHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/plans", h.plans)
	mux.HandleFunc("GET /api/admin/users", h.list)
	mux.HandleFunc("POST /api/admin/users", h.create)
	mux.HandleFunc("PATCH /api/admin/users/{id}", h.update)
	mux.HandleFunc("DELETE /api/admin/users/{id}", h.delete)
}

func decodeInto(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		HTTPError(w, http.StatusBadRequest, "cuerpo JSON inválido")
		return false
	}
	return true
}

// plans — catálogo para el frontend (panel y precios).
func (h *AdminHandler) plans(w http.ResponseWriter, r *http.Request) {
	if !isAdminGuard(w, r) {
		return
	}
	JSON(w, http.StatusOK, map[string]any{"status": "success", "data": users.Catalogue})
}

func (h *AdminHandler) list(w http.ResponseWriter, r *http.Request) {
	if !isAdminGuard(w, r) {
		return
	}
	list, err := h.Store.List()
	if err != nil {
		h.Logger.Error("admin list users", "error", err)
		HTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := []map[string]any{}
	for _, u := range list {
		data = append(data, u.AdminDict())
	}
	JSON(w, http.StatusOK, map[string]any{"status": "success", "data": data, "count": len(data)})
}

func (h *AdminHandler) create(w http.ResponseWriter, r *http.Request) {
	if !isAdminGuard(w, r) {
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decodeInto(w, r, &body) {
		return
	}
	u, err := h.Store.Create(body.Email, body.Password)
	hute := strings.TrimSpace(body.Role)
	fail := err
	if err == nil && hute != "" {
		u, fail = h.Store.SetRole(u.ID, hute)
	}
	if fail != nil {
		if fail == users.ErrEmailTaken {
			HTTPError(w, http.StatusConflict, fail.Error())
			return
		}
		h.Logger.Error("admin create user", "error", fail)
		HTTPError(w, http.StatusBadRequest, fail.Error())
		return
	}
	JSON(w, http.StatusOK, map[string]any{"status": "success", "data": u.AdminDict()})
}

// patchBody — PATCH parcial: solo los campos presentes se aplican.
type patchBody struct {
	Plan       *string          `json:"plan"`
	AddCredits *int             `json:"add_credits"`
	SetCredits *int             `json:"set_credits"`
	LLM        *users.LLMCfgAny `json:"llm"`
	IsActive   *bool            `json:"is_active"`
	Role       *string          `json:"role"`
	Password   *string          `json:"password"`
}

func (h *AdminHandler) update(w http.ResponseWriter, r *http.Request) {
	if !isAdminGuard(w, r) {
		return
	}
	id := r.PathValue("id")
	var body patchBody
	if !decodeInto(w, r, &body) {
		return
	}
	u, err := h.Store.Get(id)
	if err != nil {
		HTTPError(w, http.StatusNotFound, "usuario no encontrado")
		return
	}

	if body.Plan != nil {
		if u, err = h.Store.UpdatePlan(id, *body.Plan); err != nil {
			h.fail(w, err)
			return
		}
	}
	if body.AddCredits != nil {
		if u, err = h.Store.AddCredits(id, *body.AddCredits); err != nil {
			h.fail(w, err)
			return
		}
	}
	if body.SetCredits != nil {
		if u, err = h.Store.SetCredits(id, *body.SetCredits); err != nil {
			h.fail(w, err)
			return
		}
	}
	if body.LLM != nil {
		if u, err = h.Store.UpdateLLM(id, body.LLM.Cfg()); err != nil {
			h.fail(w, err)
			return
		}
	}
	if body.IsActive != nil {
		if u, err = h.Store.SetActive(id, *body.IsActive); err != nil {
			h.fail(w, err)
			return
		}
	}
	if body.Role != nil {
		if u, err = h.Store.SetRole(id, *body.Role); err != nil {
			h.fail(w, err)
			return
		}
	}
	if body.Password != nil {
		u, err = h.Store.ResetPassword(id, *body.Password)
		if err != nil {
			h.fail(w, err)
			return
		}
	}
	h.Logger.Info("admin: usuario actualizado", "admin", scopeIDOrMaster(r), "user", id)
	JSON(w, http.StatusOK, map[string]any{"status": "success", "data": u.AdminDict()})
}

func (h *AdminHandler) fail(w http.ResponseWriter, err error) {
	h.Logger.Error("admin update user", "error", err)
	HTTPError(w, http.StatusBadRequest, err.Error())
}

func (h *AdminHandler) delete(w http.ResponseWriter, r *http.Request) {
	if !isAdminGuard(w, r) {
		return
	}
	id := r.PathValue("id")
	u, err := h.Store.Get(id)
	if err != nil {
		HTTPError(w, http.StatusNotFound, "usuario no encontrado")
		return
	}
	// Protecciones: cuentas admin no se borran (evita quedarse sin panel) y
	// usuarios con proyectos quedan desactivados mejor que borrados.
	if u.Role == users.RoleAdmin {
		HTTPError(w, http.StatusConflict, "una cuenta admin no se puede borrar; desactívela")
		return
	}
	if owned := h.PM.ListProjects(1, id); len(owned) > 0 {
		HTTPError(w, http.StatusConflict,
			"el usuario tiene proyectos; desactive la cuenta en lugar de borrarla")
		return
	}
	if err := h.Store.Delete(id); err != nil {
		h.Logger.Error("admin delete user", "error", err)
		HTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Logger.Info("admin: usuario borrado", "user", id)
	JSON(w, http.StatusOK, map[string]any{"status": "success"})
}

// scopeIDOrMaster — etiqueta de log del principal (id de usuario, "master" o "anon").
func scopeIDOrMaster(r *http.Request) string {
	p := principal(r)
	switch {
	case p.Master:
		return "master"
	case p.Anonymous:
		return "anon"
	case p.User != nil:
		return p.User.ID
	default:
		return ""
	}
}
