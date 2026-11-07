// Port de backend/tests/test_ontology_generator.py: contexto de documentos
// con límites sobrescritos y la llamada LLM sin cap de tokens.
package ontology

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"mirofish/internal/llm"
)

// generatorForTest replica _generator_for_test: límites de instancia
// sobrescritos para mensajes cortos.
func generatorForTest() *Generator {
	g := NewGenerator()
	g.MaxTextLengthForLLM = 2000
	g.LongTextChunkSize = 500
	g.LongTextChunkOverlap = 0
	g.MaxLongTextChunks = 3
	g.MinLongTextExcerpt = 120
	return g
}

func TestShortOntologyContextKeepsOriginalText(t *testing.T) {
	g := generatorForTest()
	context := g.BuildDocumentContext([]string{"short document body"})
	if context != "short document body" {
		t.Errorf("context = %q", context)
	}
	if strings.Contains(context, "长文本自动分块摘要") {
		t.Error("contexto corto no debe tener cabecera de分块")
	}
}

func TestLongOntologyContextSamplesAcrossDocument(t *testing.T) {
	g := generatorForTest()
	longText := "BEGIN" + strings.Repeat("a", 1050) + "MIDDLE" + strings.Repeat("b", 1050) + "END"

	context := g.BuildDocumentContext([]string{longText})

	if runes := len([]rune(context)); runes > g.MaxTextLengthForLLM {
		t.Fatalf("len(context) = %d > %d", runes, g.MaxTextLengthForLLM)
	}
	if !strings.Contains(context, "长文本自动分块摘要") {
		t.Error("falta cabecera de摘要")
	}
	for _, need := range []string{"BEGIN", "MIDDLE", "END", "分块 1/", "分块 3/", "分块 5/"} {
		if !strings.Contains(context, need) {
			t.Errorf("contexto sin %q", need)
		}
	}
}

func TestVeryLongOntologyContextSelectsRepresentativeChunks(t *testing.T) {
	g := generatorForTest()
	parts := []string{"BEGIN"}
	for i := 0; i < 12; i++ {
		parts = append(parts, "CHUNK"+itoa2(i)+"-"+strings.Repeat(itoa2(i), 490))
	}
	parts = append(parts, "FINALEND")
	longText := strings.Join(parts, "")

	context := g.BuildDocumentContext([]string{longText})

	if runes := len([]rune(context)); runes > g.MaxTextLengthForLLM {
		t.Fatalf("len(context) = %d > %d", runes, g.MaxTextLengthForLLM)
	}
	if !strings.Contains(context, "BEGIN") || !strings.Contains(context, "FINALEND") {
		t.Error("deben conservarse inicio y fin")
	}
	if got := strings.Count(context, "--- 文档 1 / 分块"); got != g.MaxLongTextChunks {
		t.Errorf("headers = %d, want %d", got, g.MaxLongTextChunks)
	}
}

func itoa2(i int) string { return fmt.Sprintf("%02d", i) }

// recordingLLM — proveedor OpenAI-compat que graba el cuerpo del request y
// devuelve una ontología fija (equivalente a RecordingLLMClient).
type recordingLLM struct {
	mu     sync.Mutex
	bodies []string
	server *httptest.Server
}

func newRecordingLLM(t *testing.T) *recordingLLM {
	rec := &recordingLLM{}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, string(body))
		rec.mu.Unlock()
		inner := `{"entity_types": [], "edge_types": [], "analysis_summary": "ok"}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"fake","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":` + jsonEncode(inner) + `}}]}`))
	})
	rec.server = httptest.NewServer(mux)
	t.Cleanup(rec.server.Close)
	return rec
}

func jsonEncode(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestOntologyGenerationDoesNotCapStructuredOutputTokens(t *testing.T) {
	rec := newRecordingLLM(t)
	generator := NewGenerator()
	generator.LLM = func() (*llm.Client, error) {
		return &llm.Client{APIKey: "k", BaseURL: rec.server.URL, Model: "fake", HTTP: rec.server.Client()}, nil
	}

	result, err := generator.Generate(
		[]string{"A short source document."},
		"Simulate the public discussion.",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}

	if s, _ := result["analysis_summary"].(string); s != "ok" {
		t.Errorf("analysis_summary = %v", result["analysis_summary"])
	}
	rec.mu.Lock()
	bodies := rec.bodies
	rec.mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("sin llamadas al LLM")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil {
		t.Fatalf("body no JSON: %v", err)
	}
	if _, exists := payload["max_tokens"]; exists {
		t.Error("la generación de ontología no debe limitar max_tokens")
	}
	if temp, _ := payload["temperature"].(float64); temp != 0.3 {
		t.Errorf("temperature = %v", payload["temperature"])
	}
}
