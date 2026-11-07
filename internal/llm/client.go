// Port de backend/app/utils/llm_client.py.
//
// Cliente LLM OpenAI-compat sobre HTTP puro (sin SDK). Conserva la lógica de:
//   - limpieza de wrappers de razonamiento/JSON fence
//   - negociación de response_format (degradación a prompt-only una sola vez)
//   - reintentos de contenido con cap de tokens omitido para el reintento
//   - parser JSON tolerante: solo objeto decodificado desde el inicio,
//     nunca reparar JSON truncado
package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"mirofish/internal/config"
)

// ResponseError es el error estructurado de respuesta inutilizable
// (LLMResponseError de Python).
type ResponseError struct {
	Msg          string
	FinishReason string
}

func (e *ResponseError) Error() string {
	return e.Msg
}

// httpTimeout: los LLM razonadores tardan; 300s como configuración base.
const httpTimeout = 300 * time.Second

// Client replica LLMClient.
type Client struct {
	APIKey  string
	BaseURL string
	Model   string
	HTTP    *http.Client
}

// NewClient valida api_key (como el __init__ de Python) y usa la config global
// cuando no se pasa override.
func NewClient(apiKey, baseURL, model string) (*Client, error) {
	c := config.Load()
	if apiKey == "" {
		apiKey = c.LLMAPIKey
	}
	if apiKey == "" {
		return nil, fmt.Errorf("LLM_API_KEY 未配置")
	}
	if baseURL == "" {
		baseURL = c.LLMBaseURL
	}
	if model == "" {
		model = c.LLMModel
	}
	return &Client{
		APIKey:  apiKey,
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		HTTP:    &http.Client{Timeout: httpTimeout},
	}, nil
}

// NewDefaultClient — constructor rápido con la configuración global.
func NewDefaultClient() (*Client, error) {
	return NewClient("", "", "")
}

// CreateCompletion replica _create_completion: UNA petición cruda.
func (c *Client) CreateCompletion(messages []Message, temperature *float64, maxTokens *int, responseFormat *ResponseFormat) (*RawResponse, error) {
	body := BuildCompletionBody(&RequestBody{
		Model:          c.Model,
		Messages:       messages,
		Temperature:    temperature,
		MaxTokens:      maxTokens,
		ResponseFormat: responseFormat,
	})

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var raw RawResponse
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return nil, fmt.Errorf("LLM devolvió HTTP %d sin JSON válido: %s", resp.StatusCode, truncate(string(respBytes), 300))
	}
	// Errores de proveedor se propagan como error normal (la detección de
	// response_format no soportados los inspecciona desde aquí).
	if raw.Error != nil || (resp.StatusCode >= 400 && len(raw.Choices) == 0) {
		return nil, &ProviderError{Status: resp.StatusCode, Body: string(respBytes), Raw: &raw}
	}
	return &raw, nil
}

// ProviderError guarda el status/HTTP body del proveedor para detectar
// rechazos explícitos de response_format (status_code/body de la excepción).
type ProviderError struct {
	Status int
	Body   string
	Raw    *RawResponse
}

func (e *ProviderError) Error() string {
	if e.Raw != nil && e.Raw.Error != nil && e.Raw.Error.Message != "" {
		return fmt.Sprintf("LLM HTTP %d: %s", e.Status, e.Raw.Error.Message)
	}
	return fmt.Sprintf("LLM HTTP %d: %s", e.Status, truncate(e.Body, 300))
}

// IsResponseFormatUnsupported usa el body dict del error de Python:
// 400/422 con param=response_format* o mensaje/código conocidos.
func IsResponseFormatUnsupported(err error) bool {
	var pe *ProviderError
	if !errors.As(err, &pe) {
		return false
	}
	if pe.Status != 400 && pe.Status != 422 {
		return false
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(pe.Body), &body); err != nil {
		return false
	}
	details, ok := body["error"].(map[string]any)
	if !ok {
		return false
	}
	param, _ := details["param"].(string)
	param = strings.ToLower(strings.TrimSpace(param))
	if param == "response_format" || strings.HasPrefix(param, "response_format.") {
		return true
	}
	message, _ := details["message"].(string)
	message = strings.ToLower(message)
	if !strings.Contains(message, "response_format") {
		return false
	}
	code := strings.ToLower(anyToString(details["code"]))
	unsupportedCodes := map[string]bool{
		"unsupported_parameter": true, "unsupported_value": true,
		"unknown_parameter": true, "invalid_parameter": true,
	}
	return unsupportedCodes[code] || strings.Contains(message, "not support") ||
		strings.Contains(message, "unsupported") ||
		strings.Contains(message, "unknown parameter") ||
		strings.Contains(message, "unrecognized parameter")
}

func anyToString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Chat envía mensajes y devuelve texto limpio. Defaults Python:
// temperature 0.7, max_tokens 4096.
func (c *Client) Chat(messages []Message, temperature float64, maxTokens *int, responseFormat *ResponseFormat) (string, error) {
	temp := temperature
	raw, err := c.CreateCompletion(messages, &temp, maxTokens, responseFormat)
	if err != nil {
		return "", err
	}
	return CleanChatText(ExtractChatCompletionText(raw)), nil
}

// ChatJSON replica chat_json(): JSON-mode con negociación de capacidad
// separada de la regeneración de contenido.
func (c *Client) ChatJSON(messages []Message, temperature float64, maxTokens *int, maxAttempts int) (map[string]any, error) {
	if maxAttempts < 1 {
		return nil, fmt.Errorf("max_attempts must be at least 1")
	}

	var responseFormat *ResponseFormat = &ResponseFormat{Type: "json_object"}
	requestMaxTokens := maxTokens
	var lastError error

	temp := temperature
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Capacidad JSON-mode ≠ regeneración: un rechazo explícito de
		// response_format añade UNA petición pero no consume intento.
		var raw *RawResponse
		for {
			var err error
			raw, err = c.CreateCompletion(messages, &temp, requestMaxTokens, responseFormat)
			if err != nil {
				if responseFormat != nil && IsResponseFormatUnsupported(err) {
					responseFormat = nil // prompt-only JSON guidance
					continue
				}
				return nil, err
			}
			break
		}

		value, err := ParseJSONResponse(raw)
		if err != nil {
			lastError = err
			if attempt >= maxAttempts {
				return nil, err
			}
			// Un cap del llamante es causa común de JSON parcial: para el
			// reintento acotado se omite el límite de salida.
			requestMaxTokens = nil
			continue
		}
		return value, nil
	}
	if lastError != nil {
		return nil, lastError
	}
	return nil, &ResponseError{Msg: "LLM did not produce a JSON response"}
}

// ParseJSONResponse replica _parse_json_response.
func ParseJSONResponse(resp *RawResponse) (map[string]any, error) {
	fail := func(msg, finish string) error {
		return &ResponseError{Msg: msg, FinishReason: finish}
	}
	if len(resp.Choices) == 0 {
		return nil, fail("LLM returned no choices", "")
	}
	choice := resp.Choices[0]
	finish := choice.FinishReason
	switch {
	case finish == "length":
		return nil, fail("LLM JSON output was truncated at the token limit", finish)
	case finish != "" && finish != "stop":
		return nil, fail(fmt.Sprintf("LLM JSON generation stopped unexpectedly (%s)", finish), finish)
	}

	content := CleanChatText(ExtractChatCompletionText(resp))
	if content == "" {
		return nil, fail("LLM returned empty JSON content", finish)
	}

	value, end, decodeErr := rawDecodePrefix(content)
	if decodeErr != nil {
		return nil, fail(fmt.Sprintf("LLM returned invalid JSON (%v)", decodeErr), finish)
	}

	trailing := strings.TrimSpace(content[end:])
	if trailing != "" {
		if containsAdditionalJSONContainer(trailing) {
			return nil, fail("LLM returned multiple JSON values", finish)
		}
		// texto explicativo después de un JSON completo: se ignora
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fail("LLM JSON response must be a top-level JSON object", finish)
	}
	return value.(map[string]any), nil
}

// rawDecodePrefix decodifica un valor JSON desde el inicio, devolviendo el
// offset del final (equivalente a json.JSONDecoder().raw_decode). json.Decoder
// lee UN valor del stream sin exigir EOF — exactamente raw_decode.
func rawDecodePrefix(s string) (any, int, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, 0, err
	}
	return v, int(dec.InputOffset()), nil
}

// containsAdditionalJSONContainer replica _contains_additional_json_container:
// si el texto final empotra OTRO objeto/array JSON decodificable desde
// cualquier '{' o '['.
func containsAdditionalJSONContainer(content string) bool {
	for i, bit := range content {
		if bit != '{' && bit != '[' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(content[i:]))
		var v any
		if err := dec.Decode(&v); err == nil {
			return true
		}
	}
	return false
}

// CleanChatText replica _clean_chat_text: quita razones de pensamiento y el
// JSON fence externo de Markdown.
func CleanChatText(content string) string {
	cleaned := reThink.ReplaceAllString(content, "")
	cleaned = strings.TrimPrefix(cleaned, "\ufeff")
	cleaned = reLeadingFence.ReplaceAllString(cleaned, "")
	cleaned = reTrailingFence.ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

var (
	reThink         = regexp.MustCompile(`(?s)</think>\s*?`)
	reLeadingFence  = regexp.MustCompile(`(?i)^\x60\x60\x60(?:json)?[ \t]*\n?`)
	reTrailingFence = regexp.MustCompile(`\n?\x60\x60\x60[ \t]*$`)
)

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
