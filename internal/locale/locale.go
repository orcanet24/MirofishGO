// Package locale — port of backend/app/utils/locale.py.
//
// Carga locales/*.json embebidos en el binario y resuelve traducciones con
// fallback a zh (igual que Python). El locale por thread/thread-local de
// Python aquí es explícito: cada llamada pasa el locale o usa un default.
package locale

import (
	"embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed *.json
var localeFS embed.FS

type languageEntry struct {
	Name           string `json:"name"`
	LLMInstruction string `json:"llmInstruction"`
}

var (
	mu           sync.RWMutex
	translations = map[string]map[string]any{}
	languages    = map[string]languageEntry{}
	defaultKey   = "zh"
)

func init() {
	// languages.json: {“zh”: {"name": ..., "llmInstruction": ...}, ...}
	raw, err := localeFS.ReadFile("languages.json")
	if err == nil {
		_ = json.Unmarshal(raw, &languages)
	}
	entries, _ := localeFS.ReadDir(".")
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || name == "languages.json" {
			continue
		}
		locale := strings.TrimSuffix(name, ".json")
		raw, err := localeFS.ReadFile(name)
		if err != nil {
			continue
		}
		m := map[string]any{}
		if json.Unmarshal(raw, &m) == nil {
			translations[locale] = m
		}
	}
}

// Locales returns the loaded locale codes.
func Locales() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(translations))
	for k := range translations {
		out = append(out, k)
	}
	return out
}

// IsValid mirrors `raw in _translations`.
func IsValid(locale string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := translations[locale]
	return ok
}

// lookup chains nested dot-paths inside a translation map.
func lookup(m map[string]any, key string) (string, bool) {
	value := any(m)
	for _, part := range strings.Split(key, ".") {
		dict, ok := value.(map[string]any)
		if !ok {
			return "", false
		}
		value = dict[part]
	}
	s, ok := value.(string)
	return s, ok
}

// T returns the translation for key in locale (fallback zh, then the key
// itself), interpolating {args} — identical semantics to locale.t().
func T(locale, key string, args map[string]string) string {
	mu.RLock()
	defer mu.RUnlock()

	value, ok := lookup(translations[locale], key)
	if !ok {
		value, ok = lookup(translations[defaultKey], key)
	}
	if !ok {
		return key
	}
	for k, v := range args {
		value = strings.ReplaceAll(value, "{"+k+"}", v)
	}
	return value
}

// GetLanguageInstruction mirrors locale.get_language_instruction().
func GetLanguageInstruction(locale string) string {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := languages[locale]
	if !ok {
		e, ok = languages[defaultKey]
		if !ok {
			return "请使用中文回答。"
		}
	}
	if e.LLMInstruction == "" {
		return "请使用中文回答。"
	}
	return e.LLMInstruction
}

// Normalize picks the locale from an Accept-Language header value like the
// API layer does ('zh' when unknown).
func Normalize(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultKey
	}
	// Accept-Language: es-ES,es;q=0.9,zh;q=0.8 → primer código válido
	parts := strings.Split(raw, ",")
	for _, p := range parts {
		code := strings.TrimSpace(strings.SplitN(p, ";", 2)[0])
		if idx := strings.Index(code, "-"); idx > 0 {
			code = code[:idx]
		}
		if IsValid(code) {
			return code
		}
	}
	return defaultKey
}
