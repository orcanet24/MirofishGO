// Store de usuarios sobre MySQL. Sin ORM: sql directo como internal/graph.
package users

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Errores expuestos para mapear a HTTP (409 duplicado, 401 credenciales).
var (
	ErrEmailTaken    = errors.New("el email ya está registrado")
	ErrNotFound      = errors.New("usuario no encontrado")
	ErrBadCredentials = errors.New("credenciales inválidas")
	ErrInactive      = errors.New("cuenta desactivada")
	ErrNoCredits     = errors.New("sin créditos disponibles")
)

// Store — acceso a la tabla users.
type Store struct {
	db *sql.DB
}

// schema igual que internal/graph: CREATE TABLE IF NOT EXISTS, collation
// binaria para emails consistentes.
const schema = `CREATE TABLE IF NOT EXISTS users (
	id             CHAR(20) NOT NULL PRIMARY KEY,
	email          VARCHAR(190) NOT NULL UNIQUE,
	password_hash  VARCHAR(100) NOT NULL,
	role           VARCHAR(10) NOT NULL DEFAULT 'user',
	plan           VARCHAR(20) NULL,
	credits        INT NOT NULL DEFAULT 0,
	llm_api_key    TEXT NULL,
	llm_base_url   TEXT NULL,
	llm_model      VARCHAR(120) NULL,
	is_active      TINYINT NOT NULL DEFAULT 1,
	created_at     VARCHAR(32) NOT NULL,
	updated_at     VARCHAR(32) NOT NULL,
	last_login     VARCHAR(32) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`

// NewStore abre el store y garantiza el esquema.
func NewStore(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "usr_" + hex.EncodeToString(b)
}

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Create registra una cuenta. El primer usuario del sistema se convierte en
// admin (bootstrap sin .env); password mínimo en el handler HTTP.
func (s *Store) Create(email, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, errors.New("email inválido")
	}
	if len(password) < 6 {
		return nil, errors.New("la contraseña debe tener al menos 6 caracteres")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	count := 0
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return nil, err
	}
	role := RoleUser
	if count == 0 {
		role = RoleAdmin // bootstrap: primera cuenta = administrador
	}
	u := &User{
		ID: newID(), Email: email, PasswordHash: hash, Role: role,
		IsActive: true, CreatedAt: now(), UpdatedAt: now(),
	}
	_, err = s.db.Exec(
		`INSERT INTO users (id, email, password_hash, role, is_active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.PasswordHash, u.Role, u.IsActive, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "Duplicate entry") {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	return u, nil
}

const userCols = `id, email, password_hash, role, plan, credits,
	llm_api_key, llm_base_url, llm_model, is_active, created_at, updated_at, last_login`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var hash string
	var apiKey, baseURL, model, plan, lastLogin sql.NullString
	var active int
	err := row.Scan(&u.ID, &u.Email, &hash, &u.Role, &plan, &u.Credits,
		&apiKey, &baseURL, &model, &active, &u.CreatedAt, &u.UpdatedAt, &lastLogin)
	if err != nil {
		return nil, err
	}
	u.PasswordHash = hash
	u.IsActive = active == 1
	if plan.Valid {
		p := plan.String
		u.Plan = &p
	}
	if lastLogin.Valid {
		l := lastLogin.String
		u.LastLogin = &l
	}
	if apiKey.Valid || baseURL.Valid || model.Valid {
		u.LLM = &LLMCfg{APIKey: nullable(apiKey), BaseURL: nullable(baseURL), Model: nullable(model)}
	}
	return &u, nil
}

func nullable(v sql.NullString) *string {
	if v.Valid {
		s := v.String
		return &s
	}
	return nil
}

// Get por ID.
func (s *Store) Get(id string) (*User, error) {
	row := s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// GetByEmail normaliza el email igual que Create.
func (s *Store) GetByEmail(email string) (*User, error) {
	row := s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE email = ?`, strings.ToLower(strings.TrimSpace(email)))
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// Authenticate verifica email+password y devuelve el usuario activo.
func (s *Store) Authenticate(email, password string) (*User, error) {
	u, err := s.GetByEmail(email)
	if errors.Is(err, ErrNotFound) {
		// Igual de costoso que un bcrypt real para no filtrar existencia de emails.
		_ = checkPassword(
			"$2a$10$7EqJtq98hPqEX7fNZaFWoOhi5B0fT1RlqC1Z0cmbGkJ8S1jvementsISPne",
			password)
		return nil, ErrBadCredentials
	}
	if err != nil {
		return nil, err
	}
	if !u.IsActive {
		return nil, ErrInactive
	}
	if !checkPassword(u.PasswordHash, password) {
		return nil, ErrBadCredentials
	}
	_, _ = s.db.Exec(`UPDATE users SET last_login = ? WHERE id = ?`, now(), u.ID)
	return u, nil
}
func (s *Store) List() ([]*User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdatePlan fija el plan y borra los créditos a los que da derecho.
func (s *Store) UpdatePlan(id, planID string) (*User, error) {
	plan, ok := GetPlan(planID)
	if !ok {
		return nil, fmt.Errorf("plan desconocido: %s", planID)
	}
	res, err := s.db.Exec(`UPDATE users SET plan = ?, credits = ?, updated_at = ? WHERE id = ?`,
		plan.ID, plan.Studies, now(), id)
	return s.updated(res, err, id)
}

// AddCredits suma (o resta con negativo) créditos manualmente, sin bajar de 0.
// (El clamp se hace en Go: MAX(0, credits + ?) con parámetro no es portable
// entre MySQL/MariaDB.)
func (s *Store) AddCredits(id string, delta int) (*User, error) {
	u, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	next := u.Credits + delta
	if next < 0 {
		next = 0
	}
	return s.SetCredits(id, next)
}

// SetCredits fija los créditos a un valor absoluto (clamp a >= 0).
func (s *Store) SetCredits(id string, credits int) (*User, error) {
	if credits < 0 {
		credits = 0
	}
	res, err := s.db.Exec(`UPDATE users SET credits = ?, updated_at = ? WHERE id = ?`,
		credits, now(), id)
	return s.updated(res, err, id)
}

// ConsumeCredit descuenta 1 crédito atómicamente. Falla sin carácter negativo.
func (s *Store) ConsumeCredit(id string) error {
	res, err := s.db.Exec(
		`UPDATE users SET credits = credits - 1, updated_at = ? WHERE id = ? AND credits > 0`,
		now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoCredits
	}
	return nil
}

// UpdateLLM — asigna la config LLM del usuario. Vacía = delegar en la global
// (el valor nil en la BD significa "usar la del .env").
func (s *Store) UpdateLLM(id string, cfg *LLMCfg) (*User, error) {
	if _, err := s.Get(id); err != nil {
		return nil, err
	}
	val := func(src *string) any {
		if src == nil || strings.TrimSpace(*src) == "" {
			return nil
		}
		return strings.TrimSpace(*src)
	}
	_, err := s.db.Exec(
		`UPDATE users SET llm_api_key = ?, llm_base_url = ?, llm_model = ?, updated_at = ? WHERE id = ?`,
		val(cfg.APIKey), val(cfg.BaseURL), val(cfg.Model), now(), id)
	if err != nil {
		return nil, err
	}
	return s.Get(id)
}

// SetActive activa/desactiva la cuenta sin borrar datos.
func (s *Store) SetActive(id string, active bool) (*User, error) {
	res, err := s.db.Exec(`UPDATE users SET is_active = ?, updated_at = ? WHERE id = ?`,
		active, now(), id)
	return s.updated(res, err, id)
}

// SetRole — solo valido en admin/user.
func (s *Store) SetRole(id, role string) (*User, error) {
	if role != RoleAdmin && role != RoleUser {
		return nil, fmt.Errorf("rol desconocido: %s", role)
	}
	res, err := s.db.Exec(`UPDATE users SET role = ?, updated_at = ? WHERE id = ?`,
		role, now(), id)
	return s.updated(res, err, id)
}

// SetPassword — cambio de contraseña (el handler HTTP verifica la actual).
func (s *Store) SetPassword(id, password string) (*User, error) {
	if len(password) < 6 {
		return nil, errors.New("la contraseña debe tener al menos 6 caracteres")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	res, err := s.db.Exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		hash, now(), id)
	return s.updated(res, err, id)
}

// ResetPassword — cambio administrativo (sin conocer la anterior).
func (s *Store) ResetPassword(id, password string) (*User, error) {
	return s.SetPassword(id, password)
}

// Delete borra la cuenta. Las cuentas con proyectos en disco se protegen
// (409 Conflict en el handler) para no dejar datos huérfanos apuntando a nada.
func (s *Store) Delete(id string) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Count — helper reservado: el conteo de proyectos por usuario lo hace el
// handler HTTP (los proyectos viven en disco, no en esta BD).

func (s *Store) updated(res sql.Result, err error, id string) (*User, error) {
	if err != nil || res == nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(id)
}
