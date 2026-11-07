// Package users — cuentas de usuario, planes y créditos ("estudios").
//
// Cada usuario queda aislado de los demás: los proyectos, simulaciones e
// informes se filtran por su ID, la clave LLM que usa el backend para atenderle
// la asigna el admin, y los créditos se consumen al generar cada estudio.
package users

// Roles de cuenta.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// Plan del catálogo.
type Plan struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Studies int     `json:"studies"`
	PriceUSD float64 `json:"price_usd"`
}

// Catalogue — planes disponibles. Admin asigna uno y sus créditos quedan
// cargados; el consumo descuenta de Credits.
var Catalogue = []Plan{
	{ID: "basic", Name: "Básico", Studies: 1, PriceUSD: 10},
	{ID: "beginner", Name: "Emprendedor", Studies: 4, PriceUSD: 29},
	{ID: "company", Name: "Empresa", Studies: 20, PriceUSD: 99},
}

// GetPlan devuelve el plan del catálogo por ID.
func GetPlan(id string) (Plan, bool) {
	for _, p := range Catalogue {
		if p.ID == id {
			return p, true
		}
	}
	return Plan{}, false
}

// LLMScheme — dónde se guarda la configuración LLM por usuario. nil = sin
// delegar; el backend usa la global del .env.
type LLMCfg struct {
	APIKey  *string `json:"api_key"`
	BaseURL *string `json:"base_url"`
	Model   *string `json:"model"`
}

// HasLLMConfig informa si hay configuración LLM delegada (no global).
func (l *LLMCfg) Has() bool { return l != nil && (l.APIKey != nil || l.BaseURL != nil || l.Model != nil) }

// LLMLcfgAny — config LLM tal como llega por JSON del panel admin.
// nil o cadena vacía = limpiar el campo (el backend cae a la config global).
type LLMCfgAny struct {
	APIKey  *string `json:"api_key"`
	BaseURL *string `json:"base_url"`
	Model   *string `json:"model"`
}

// Cfg convierte la forma admin a la forma interna.
func (a *LLMCfgAny) Cfg() *LLMCfg {
	if a == nil {
		return &LLMCfg{}
	}
	return &LLMCfg{APIKey: a.APIKey, BaseURL: a.BaseURL, Model: a.Model}
}

// User — cuenta persistida en MySQL. PasswordHash nunca sale en JSON.
type User struct {
	ID           string  `json:"id"`
	Email        string  `json:"email"`
	PasswordHash string  `json:"-"`
	Role         string  `json:"role"`
	Plan         *string `json:"plan,omitempty"`
	Credits      int     `json:"credits"`
	LLM          *LLMCfg `json:"-"`
	IsActive     bool    `json:"is_active"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	LastLogin    *string `json:"last_login,omitempty"`
}

// PublicDict — lo que ve el propio usuario de sí mismo (sin hash ni clave LLM).
func (u *User) PublicDict() map[string]any {
	return map[string]any{
		"id":         u.ID,
		"email":      u.Email,
		"role":       u.Role,
		"plan":       u.Plan,
		"credits":    u.Credits,
		"is_active":  u.IsActive,
		"created_at": u.CreatedAt,
		"last_login": u.LastLogin,
	}
}

// AdminDict — vista de administración: incluye la config LLM (la asigna el
// admin desde este panel).
func (u *User) AdminDict() map[string]any {
	m := u.PublicDict()
	if u.LLM != nil {
		m["llm"] = u.LLM
	} else {
		m["llm"] = nil
	}
	m["llm_configured"] = u.LLM.Has()
	m["updated_at"] = u.UpdatedAt
	return m
}
