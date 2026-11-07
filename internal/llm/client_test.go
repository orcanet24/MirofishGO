// Port de backend/tests/test_llm_json_responses.py: un proveedor fake
// (httptest) con cola de respuestas/errores que además graba cada petición.
package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeResponse struct {
	status int
	body   string
}

type fakeProvider struct {
	server *httptest.Server
	queue  []fakeResponse
	calls  []map[string]any
}

func newFakeProvider(t *testing.T, queue ...fakeResponse) *fakeProvider {
	t.Helper()
	f := &fakeProvider{queue: queue}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.calls = append(f.calls, body)
		resp := f.queue[0]
		f.queue = f.queue[1:]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

// response construye un cuerpo válido de /chat/completions.
func response(content, finishReason string) fakeResponse {
	finish := finishReason
	if finish == "" {
		finish = "stop"
	}
	msg := fmt.Sprintf(`{"content": %s}`, mustJSON(content))
	body := fmt.Sprintf(`{"choices":[{"finish_reason":%q,"message":%s}]}`, finish, msg)
	return fakeResponse{status: 200, body: body}
}

func mustJSON(s string) string {
	if s == "<none>" {
		return "null"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func providerError(status int, param, code, message string) fakeResponse {
	body := fmt.Sprintf(`{"error":{"param":%q,"code":%q,"message":%q}}`, param, code, message)
	return fakeResponse{status: status, body: body}
}

func newClientFor(f *fakeProvider) *Client {
	return &Client{
		APIKey:  "test-key",
		BaseURL: f.server.URL,
		Model:   "compatible-model",
		HTTP:    f.server.Client(),
	}
}

func chatJSON(t *testing.T, c *Client, maxTokens *int, attempts int) (map[string]any, error) {
	t.Helper()
	return c.ChatJSON([]Message{{Role: "user", Content: "Return JSON"}}, 0.3, maxTokens, attempts)
}

func TestChatJSONRetriesTruncatedCompletionWithoutTokenCap(t *testing.T) {
	f := newFakeProvider(t,
		response(`{"items": [`, "length"),
		response(`{"items": [1, 2]}`, ""),
	)
	c := newClientFor(f)
	result, err := chatJSON(t, c, iptr(4096), 2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result["items"] == nil {
		t.Errorf("result = %v", result)
	}
	if f.calls[0]["max_tokens"] != float64(4096) {
		t.Errorf("call0 max_tokens = %v", f.calls[0]["max_tokens"])
	}
	if _, present := f.calls[1]["max_tokens"]; present {
		t.Error("el reintento NO debe llevar cap de tokens")
	}
}

func TestChatJSONOmitsTokenCapWhenRequested(t *testing.T) {
	f := newFakeProvider(t, response(`{"ok": true}`, ""))
	c := newClientFor(f)
	result, err := chatJSON(t, c, nil, 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result["ok"] != true {
		t.Errorf("result = %v", result)
	}
	if _, present := f.calls[0]["max_tokens"]; present {
		t.Error("max_tokens enviándose con nil")
	}
}

func TestChatJSONRetriesEmptyContentOnce(t *testing.T) {
	f := newFakeProvider(t,
		response("<none>", ""),
		response(`{"ok": true}`, ""),
	)
	c := newClientFor(f)
	result, err := chatJSON(t, c, iptr(4096), 2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result["ok"] != true {
		t.Errorf("result = %v", result)
	}
	if len(f.calls) != 2 {
		t.Errorf("calls = %d", len(f.calls))
	}
}

func TestChatJSONAcceptsCompleteObjectBeforeTrailingText(t *testing.T) {
	f := newFakeProvider(t, response("{\"ok\": true}\nThis object is ready to use.", ""))
	c := newClientFor(f)
	result, err := chatJSON(t, c, iptr(4096), 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result["ok"] != true {
		t.Errorf("result = %v", result)
	}
}

func TestChatJSONRejectsASecondJSONDocument(t *testing.T) {
	contents := []string{
		"{\"status\": \"draft\"}\n{\"status\": \"complete\"}",
		"{\"status\": \"draft\"}\n```json\n{\"status\": \"complete\"}\n```",
		"{\"status\": \"draft\"}\nExplanation first.\n{\"status\": \"complete\"}",
	}
	for _, content := range contents {
		c := newClientFor(newFakeProvider(t, response(content, "")))
		_, err := chatJSON(t, c, iptr(4096), 1)
		if err == nil {
			t.Fatalf("esperado error para %q", content)
		}
		var re *ResponseError
		if !asResponseError(err, &re) || !contains(re.Msg, "multiple JSON") {
			t.Fatalf("error = %v; se esperaba 'multiple JSON'", err)
		}
	}
}

func TestChatJSONRejectsTopLevelArray(t *testing.T) {
	f := newFakeProvider(t, response(`[{"ok": true}]`, ""))
	c := newClientFor(f)
	_, err := chatJSON(t, c, iptr(4096), 1)
	if err == nil {
		t.Fatal("esperado error por array raíz")
	}
	var re *ResponseError
	if !asResponseError(err, &re) || !contains(re.Msg, "JSON object") {
		t.Fatalf("error = %v", err)
	}
}

func TestChatJSONErrorDoesNotEchoPartialModelOutput(t *testing.T) {
	partial := `{"private_source_text": "SENTINEL-SHOULD-NOT-LEAK"`
	f := newFakeProvider(t, response(partial, "length"), response(partial, "length"))
	c := newClientFor(f)
	_, err := chatJSON(t, c, iptr(4096), 2)
	if err == nil {
		t.Fatal("esperado error")
	}
	if contains(err.Error(), "SENTINEL-SHOULD-NOT-LEAK") {
		t.Error("el error filtra salida parcial del modelo")
	}
	var re *ResponseError
	if !asResponseError(err, &re) || re.FinishReason != "length" {
		t.Errorf("error = %v (%T)", err, err)
	}
}

func TestChatJSONFallsBackOnlyForExplicitResponseFormatRejection(t *testing.T) {
	f := newFakeProvider(t,
		providerError(400, "response_format", "unsupported_parameter", "response_format is not supported by this model"),
		response(`{"ok": true}`, ""),
	)
	c := newClientFor(f)
	result, err := chatJSON(t, c, iptr(4096), 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result["ok"] != true {
		t.Errorf("result = %v", result)
	}
	if fmt.Sprint(f.calls[0]["response_format"]) != "map[type:json_object]" {
		t.Errorf("call0 response_format = %v", f.calls[0]["response_format"])
	}
	if _, present := f.calls[1]["response_format"]; present {
		t.Error("call1 aún envía response_format tras el rechazo")
	}
}

func TestResponseFormatFallbackKeepsContentRetryAvailable(t *testing.T) {
	f := newFakeProvider(t,
		providerError(400, "response_format", "unsupported_parameter", "response_format is not supported by this model"),
		response(`{"items": [`, "length"),
		response(`{"items": [1]}`, ""),
	)
	c := newClientFor(f)
	result, err := chatJSON(t, c, iptr(4096), 2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result["items"] == nil {
		t.Errorf("result = %v", result)
	}
	if _, present := f.calls[2]["max_tokens"]; present {
		t.Error("call2 aún lleva cap de tokens")
	}
}

func TestChatJSONDefaultsToOneContentAttempt(t *testing.T) {
	f := newFakeProvider(t,
		response("<none>", ""),
		response(`{"ok": true}`, ""),
	)
	c := newClientFor(f)
	_, err := chatJSON(t, c, iptr(4096), 1)
	if err == nil || !contains(err.Error(), "empty JSON") {
		t.Fatalf("error = %v", err)
	}
	// 1 llamada: la segunda respuesta del fake NUNCA se consume.
	if got := len(f.calls); got != 1 {
		t.Errorf("calls = %d", got)
	}
}

func TestChatJSONDoesNotRetryUnrelatedProviderErrors(t *testing.T) {
	for _, status := range []int{400, 401, 429, 500} {
		f := newFakeProvider(t,
			providerError(status, "messages", "invalid_request", "request failed for an unrelated reason"),
			response(`{"ok": true}`, ""),
		)
		c := newClientFor(f)
		_, err := chatJSON(t, c, iptr(4096), 2)
		if err == nil {
			t.Fatalf("status %d: esperado error", status)
		}
		var pe *ProviderError
		if !asProviderError(err, &pe) || pe.Status != status {
			t.Fatalf("status %d: error = %v (%T)", status, err, err)
		}
		if len(f.calls) != 1 {
			t.Errorf("status %d: calls = %d", status, len(f.calls))
		}
	}
}

func TestChatJSONReportsMissingChoicesWithoutRetryingForever(t *testing.T) {
	f := newFakeProvider(t,
		response("<none>", ""),
		response("<none>", ""),
	)
	// respuestas sin choices: body custom
	f.queue[0].body = `{"choices": []}`
	f.queue[1].body = `{"choices": []}`
	c := newClientFor(f)
	_, err := chatJSON(t, c, iptr(4096), 2)
	if err == nil || !contains(err.Error(), "no choices") {
		t.Fatalf("error = %v", err)
	}
	if len(f.calls) != 2 {
		t.Errorf("calls = %d", len(f.calls))
	}
}

func asResponseError(err error, target **ResponseError) bool {
	re, ok := err.(*ResponseError)
	if ok {
		*target = re
	}
	return ok
}

func asProviderError(err error, target **ProviderError) bool {
	pe, ok := err.(*ProviderError)
	if ok {
		*target = pe
	}
	return ok
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
