// Port de backend/tests/test_openai_chat_compat.py.
package llm

import (
	"encoding/json"
	"testing"
)

func TestGPT5UsesCompletionTokenLimitWithoutTemperature(t *testing.T) {
	body := BuildCompletionBody(&RequestBody{
		Model:       "gpt-5-2025-08-07",
		Messages:    []Message{{Role: "user", Content: "hello"}},
		Temperature: fptr(0.2),
		MaxTokens:   iptr(123),
		ResponseFormat: &ResponseFormat{Type: "json_object"},
	})
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if _, present := m["temperature"]; present {
		t.Error("GPT-5 no debe recibir temperature")
	}
	if m["max_completion_tokens"] != float64(123) {
		t.Errorf("max_completion_tokens = %v", m["max_completion_tokens"])
	}
	if _, present := m["max_tokens"]; present {
		t.Error("max_tokens no debe enviarse a GPT-5")
	}
}

func TestLegacyModelPreservesOriginalRequestShape(t *testing.T) {
	body := BuildCompletionBody(&RequestBody{
		Model:       "third-party-chat-model",
		Messages:    []Message{{Role: "user", Content: "hello"}},
		Temperature: fptr(0.7),
		MaxTokens:   iptr(456),
		ResponseFormat: &ResponseFormat{Type: "json_object"},
	})
	out, _ := json.Marshal(body)
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if m["temperature"] != float64(0.7) || m["max_tokens"] != float64(456) {
		t.Errorf("forma legada alterada: %v", m)
	}
	if _, present := m["max_completion_tokens"]; present {
		t.Error("max_completion_tokens en modelo legado")
	}
}

func TestGPT5FamilyDetection(t *testing.T) {
	cases := map[string]bool{
		"gpt-5":           true,
		" GPT-5.1-mini ":  true,
		"gpt-4.1":         false,
		"my-gpt-5-proxy":  false,
		"":                false,
	}
	for model, expected := range cases {
		if got := IsGPT5Family(model); got != expected {
			t.Errorf("IsGPT5Family(%q) = %v", model, got)
		}
	}
}

func TestExtractsTextFromSupportedContentShapes(t *testing.T) {
	content := json.RawMessage(`[
		{"text": {"value": "first"}},
		{"content": " second"},
		{"text": " third"}
	]`)
	resp := &RawResponse{}
	if err := json.Unmarshal([]byte(`{"choices":[{}]}`), resp); err != nil {
		t.Fatal(err)
	}
	resp.Choices[0].Message.Content = content
	if got := ExtractChatCompletionText(resp); got != "first second third" {
		t.Errorf("extract = %q", got)
	}

	empty := &RawResponse{}
	if got := ExtractChatCompletionText(empty); got != "" {
		t.Errorf("sin choices = %q", got)
	}
}

func fptr(f float64) *float64 { return &f }
func iptr(i int) *int         { return &i }
