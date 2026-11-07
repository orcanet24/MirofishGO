// Package llm — port of backend/app/utils/openai_chat_compat.py.
//
// Ayudas para Chat Completions compatibles OpenAI: adaptación de parámetros
// por familia de modelo (GPT-5 no acepta temperature; usa
// max_completion_tokens) y extracción de texto de las formas de content
// que devuelven los proveedores.
package llm

import (
	"encoding/json"
	"strings"
)

// IsGPT5Family devuelve true para alias/snapshots de la familia GPT-5.
func IsGPT5Family(model string) bool {
	if model == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-5")
}

// Message replica {"role", "content"} de la API de chat.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// RequestBody es el payload POST {base}/chat/completions con la misma forma
// que construye client.chat.completions.create(**kwargs) en Python: solo los
// parámetros no-nil se serializan (omitempty + punteros).
type RequestBody struct {
	Model               string           `json:"model"`
	Messages            []Message        `json:"messages"`
	Temperature         *float64         `json:"temperature,omitempty"`
	MaxTokens           *int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int             `json:"max_completion_tokens,omitempty"`
	ResponseFormat      *ResponseFormat  `json:"response_format,omitempty"`
	Tools               []map[string]any `json:"tools,omitempty"`
	ToolChoice          any              `json:"tool_choice,omitempty"`
}

// ResponseFormat replica {"type": "json_object"}.
type ResponseFormat struct {
	Type string `json:"type"`
}

// BuildCompletionBody aplica la estrategia de compatibilidad in-place:
//
//   - familia GPT-5: sin temperature; límite vía max_completion_tokens
//   - resto: preserva temperature y max_tokens legados
//
// Los errores del proveedor se propagan tal cual (no se adivinan del texto).
func BuildCompletionBody(body *RequestBody) *RequestBody {
	if IsGPT5Family(body.Model) {
		body.Temperature = nil
		if body.MaxTokens != nil {
			body.MaxCompletionTokens = body.MaxTokens
			body.MaxTokens = nil
		}
	}
	return body
}

// RawResponse es la respuesta cruda de /chat/completions tal como llega del
// proveedor (se conserva para extraer finish_reason, usage, tool_calls...).
type RawResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			ToolCalls []ToolCall      `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
		Param   string `json:"param"`
	} `json:"error"`
}

// ToolCall replica los function-calls de OpenAI.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ExtractChatCompletionText replays extract_chat_completion_text(): contenido
// string directo; listas con items {text: "..."} / {text: {value: ...}} /
// {content: "..."} concatenados y strip final.
func ExtractChatCompletionText(resp *RawResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	content := resp.Choices[0].Message.Content
	if len(content) == 0 {
		return ""
	}
	var asString string
	if json.Unmarshal(content, &asString) == nil {
		return asString
	}
	var items []json.RawMessage
	if json.Unmarshal(content, &items) != nil {
		return string(content)
	}
	var b strings.Builder
	for _, raw := range items {
		var item map[string]any
		if json.Unmarshal(raw, &item) == nil {
			// item.text: string o dict {"value": string}
			switch v := item["text"].(type) {
			case string:
				b.WriteString(v)
				continue
			case map[string]any:
				if value, ok := v["value"].(string); ok {
					b.WriteString(value)
					continue
				}
			}
			if v, ok := item["content"].(string); ok {
				b.WriteString(v)
			}
		}
	}
	return strings.TrimSpace(b.String())
}
