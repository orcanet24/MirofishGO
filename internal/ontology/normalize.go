// Package ontology — port de backend/app/utils/ontology.py y las ayudas de
// backend/app/services/ontology_generator.py (ToPascalCase, ToUpperSnakeCase).
package ontology

import (
	"regexp"
	"strings"
)

const (
	// MaxTypes replica MAX_ONTOLOGY_TYPES.
	MaxTypes = 10
	// MaxAttributes replica MAX_ONTOLOGY_ATTRIBUTES.
	MaxAttributes = 10
	// MaxSourceTargets replica MAX_ONTOLOGY_SOURCE_TARGETS.
	MaxSourceTargets = 10
)

// ReservedAttributeNames replica RESERVED_ONTOLOGY_ATTRIBUTE_NAMES.
var ReservedAttributeNames = map[string]bool{
	"uuid": true, "name": true, "group_id": true, "graph_id": true,
	"name_embedding": true, "summary": true, "created_at": true,
}

// FallBackAttribute replica _FALLBACK_ATTRIBUTE.
func FallBackAttribute() map[string]any {
	return map[string]any{
		"name":        "details",
		"type":        "text",
		"description": "Additional details about this ontology type.",
	}
}

// NormalizeAttribute replica normalize_ontology_attribute: nil para
// valores inutilizables (str vacía, no-dicts, name inválido).
func NormalizeAttribute(v any) map[string]any {
	if s, ok := v.(string); ok {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return map[string]any{"name": s, "type": "text", "description": s}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	name, _ := m["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil
	}
	out := map[string]any{}
	for k, val := range m {
		out[k] = val
	}
	if desc, ok := out["description"].(string); !ok || strings.TrimSpace(desc) == "" {
		out["description"] = name
	}
	return out
}

// NormalizeAttributes replica normalize_ontology_attributes: lista no vacía
// dentro de límites; fallback `details` si todo falla.
func NormalizeAttributes(v any) []map[string]any {
	list, _ := v.([]any)
	out := []map[string]any{}
	for _, item := range list {
		norm := NormalizeAttribute(item)
		if norm == nil {
			continue
		}
		out = append(out, norm)
		if len(out) == MaxAttributes {
			break
		}
	}
	if len(out) == 0 {
		out = append(out, FallBackAttribute())
	}
	return out
}

// NormalizeSourceTargets replica normalize_ontology_source_targets.
// limit <= 0 = sin límite (limit=None del Python).
func NormalizeSourceTargets(v any, limit int) []map[string]string {
	list, _ := v.([]any)
	out := []map[string]string{}
	seen := map[string]bool{}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		source, sok := m["source"].(string)
		target, tok := m["target"].(string)
		if !sok || strings.TrimSpace(source) == "" || !tok || strings.TrimSpace(target) == "" {
			continue
		}
		source = strings.TrimSpace(source)
		target = strings.TrimSpace(target)
		pair := source + "\x00" + target
		if seen[pair] {
			continue
		}
		seen[pair] = true
		out = append(out, map[string]string{"source": source, "target": target})
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

// ToPascalCase replica _to_pascal_case ('works_for' → 'WorksFor').
func ToPascalCase(name string) string {
	parts := reNonAlnum.Split(name, -1)
	words := []string{}
	for _, part := range parts {
		camel := reCamelBoundary.ReplaceAllString(part, "${1}_${2}")
		words = append(words, strings.Split(camel, "_")...)
	}
	out := ""
	for _, w := range words {
		if w == "" {
			continue
		}
		// Python word.capitalize(): primera mayúscula, resto minúscula.
		r := []rune(w)
		first := strings.ToUpper(string(r[0]))
		rest := ""
		if len(r) > 1 {
			rest = strings.ToLower(string(r[1:]))
		}
		out += first + rest
	}
	if out == "" {
		return "Unknown"
	}
	return out
}

// ToUpperSnakeCase replica _to_upper_snake_case → SCREAMING_SNAKE_CASE.
func ToUpperSnakeCase(name string) string {
	separated := reCamelDigitBoundary.ReplaceAllString(strings.TrimSpace(name), "${1}_${2}")
	normalized := strings.ToUpper(strings.Trim(reNonIdent.ReplaceAllString(separated, "_"), "_"))
	if normalized == "" {
		return "UNKNOWN"
	}
	if normalized[0] >= '0' && normalized[0] <= '9' {
		normalized = "REL_" + normalized
	}
	return normalized
}

var (
	reNonAlnum         = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	reCamelBoundary    = regexp.MustCompile(`([a-z])([A-Z])`)
	reCamelDigitBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	reNonIdent         = regexp.MustCompile(`[^a-zA-Z0-9]+`)
)
