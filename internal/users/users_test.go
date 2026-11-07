package users_test

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"mirofish/internal/testdb"
	"mirofish/internal/users"
)

// newStore abre el store. Por defecto crea una base efímera (testdb.New); con
// MIROFISH_TEST_DIRECT=<dsn> usa esa base directamente (entornos donde el
// usuario no tiene CREATE DATABASE) y limpia las filas @test.com al terminar.
func newStore(t *testing.T) *users.Store {
	t.Helper()
	direct := os.Getenv("MIROFISH_TEST_DIRECT")
	dsn := direct
	if dsn == "" {
		dsn = testdb.New(t)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if direct != "" {
		t.Cleanup(func() {
			_, _ = db.Exec(`DELETE FROM users WHERE email LIKE '%@test.com'`)
		})
	}
	store, err := users.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestFirstUserIsAdmin(t *testing.T) {
	s := newStore(t)
	u, err := s.Create("admin@test.com", "secreto123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.Role != users.RoleAdmin {
		t.Fatalf("primer usuario debe ser admin; role=%s", u.Role)
	}
	u2, err := s.Create("user2@test.com", "secreto123")
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}
	if u2.Role != users.RoleUser {
		t.Fatalf("segundo usuario debe ser user; role=%s", u2.Role)
	}
}

func TestAuthenticate(t *testing.T) {
	s := newStore(t)
	_, err := s.Create("a@test.com", "secreto123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Authenticate("a@test.com", "secreto123"); err != nil {
		t.Fatalf("credenciales buenas deben autenticar: %v", err)
	}
	if _, err := s.Authenticate("a@test.com", "mala-clave"); err != users.ErrBadCredentials {
		t.Fatalf("clave mala debe dar ErrBadCredentials; got %v", err)
	}
	if _, err := s.Authenticate("noexiste@test.com", "secreto123"); err != users.ErrBadCredentials {
		t.Fatalf("email inexistente debe dar ErrBadCredentials; got %v", err)
	}
}

func TestTokenRoundtrip(t *testing.T) {
	s := newStore(t)
	u, err := s.Create("tok@test.com", "secreto123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tok, err := users.NewToken("secreto-firma", u)
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	claims, err := users.ParseToken("secreto-firma", tok)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.Sub != u.ID || claims.Role != u.Role {
		t.Fatalf("claims inesperadas: %+v", claims)
	}
	if _, err := users.ParseToken("otra-firma", tok); err == nil {
		t.Fatal("token firmado con otra clave debe fallar")
	}
	if _, err := users.ParseToken("secreto-firma", "a.b.c"); err == nil {
		t.Fatal("token malformado debe fallar")
	}
}

func TestPlansAndCredits(t *testing.T) {
	s := newStore(t)
	u, err := s.Create("plan@test.com", "secreto123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	u, err = s.UpdatePlan(u.ID, "beginner")
	if err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}
	if u.Credits != 4 || u.Plan == nil || *u.Plan != "beginner" {
		t.Fatalf("plan mal aplicado: %+v", u)
	}
	if err := s.ConsumeCredit(u.ID); err != nil {
		t.Fatalf("ConsumeCredit: %v", err)
	}
	u, _ = s.Get(u.ID)
	if u.Credits != 3 {
		t.Fatalf("créditos tras consumir: %d (want 3)", u.Credits)
	}
	u, _ = s.AddCredits(u.ID, -10)
	if u.Credits != 0 {
		t.Fatalf("AddCredits no clamp a 0: %d", u.Credits)
	}
	if err := s.ConsumeCredit(u.ID); err != users.ErrNoCredits {
		t.Fatalf("ConsumeCredit sin crédito debe fallar; got %v", err)
	}
	if _, err := s.UpdatePlan(u.ID, "inexistente"); err == nil {
		t.Fatal("plan desconocido debe fallar")
	}
	u, _ = s.SetCredits(u.ID, 20)
	if u.Credits != 20 {
		t.Fatalf("SetCredits: %d (want 20)", u.Credits)
	}
}

func TestLLMConfig(t *testing.T) {
	s := newStore(t)
	u, err := s.Create("llm@test.com", "secreto123")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	k, b, m := "ak_test", "https://api.example.com/v1", "model-x"
	u, err = s.UpdateLLM(u.ID, &users.LLMCfg{APIKey: &k, BaseURL: &b, Model: &m})
	if err != nil {
		t.Fatalf("UpdateLLM: %v", err)
	}
	if u.LLM == nil || u.LLM.APIKey == nil || *u.LLM.APIKey != k || !u.LLM.Has() {
		t.Fatalf("config LLM no guardada: %+v", u.LLM)
	}
	u, err = s.UpdateLLM(u.ID, &users.LLMCfg{}) // vacío = limpiar → global
	if err != nil {
		t.Fatalf("UpdateLLM clear: %v", err)
	}
	if u.LLM != nil {
		t.Fatalf("config LLM no limpiada: %+v", u.LLM)
	}
}

func TestDuplicateEmail(t *testing.T) {
	s := newStore(t)
	if _, err := s.Create("dup@test.com", "secreto123"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create("dup@test.com", "otra-clave"); err != users.ErrEmailTaken {
		t.Fatalf("email duplicado debe dar ErrEmailTaken; got %v", err)
	}
}
