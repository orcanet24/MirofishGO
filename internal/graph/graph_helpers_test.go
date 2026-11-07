// Helpers compartidos de los tests del paquete graph.
package graph

import (
	"errors"
	"testing"

	"mirofish/internal/llm"
	"mirofish/internal/testdb"
)

func testClient(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(testdb.New(t))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.DB.Close() })
	if _, err := c.CreateGraph("g1", "Test Graph", "test"); err != nil {
		t.Fatalf("CreateGraph: %v", err)
	}
	return c
}

// Simula entorno sin LLM: la factory falla al instante y la extracción
// devuelve vacío (mismo efecto que el try/except del Python).
func disableLLM(t *testing.T) {
	t.Helper()
	orig := LLMFactory
	LLMFactory = func() (*llm.Client, error) { return nil, errors.New("no llm in tests") }
	t.Cleanup(func() { LLMFactory = orig })
}

var _ = itoa // ya definida en util.go para el paquete
