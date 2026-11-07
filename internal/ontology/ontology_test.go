// Port de backend/tests/test_ontology_attributes.py (casos de normalización
// y de _validate_and_process del generador; los de GraphBuilderService van
// con el port de graph_builder).
package ontology

import (
	"fmt"
	"reflect"
	"testing"
)

// ===== normalize_ontology_attribute(s) =====

func TestNormalizeStringAttribute(t *testing.T) {
	got := NormalizeAttribute("role")
	want := map[string]any{"name": "role", "type": "text", "description": "role"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NormalizeAttribute(\"role\") = %v, want %v", got, want)
	}
}

func TestPreserveValidDictionaryAttribute(t *testing.T) {
	original := map[string]any{"name": "role", "type": "text", "description": "Public role"}
	got := NormalizeAttribute(original)
	if !reflect.DeepEqual(got, original) {
		t.Errorf("normalizado = %v, want %v", got, original)
	}
	got["description"] = "modificado"
	if original["description"] != "Public role" {
		t.Error("NormalizeAttribute debe devolver una copia defensiva")
	}
}

func TestRejectUnusableAttributeShapes(t *testing.T) {
	for _, v := range []any{
		nil, float64(7), []any{}, map[string]any{},
		map[string]any{"name": nil}, map[string]any{"name": ""}, "   ",
	} {
		if got := NormalizeAttribute(v); got != nil {
			t.Errorf("NormalizeAttribute(%#v) = %v, want nil", v, got)
		}
	}
}

func TestAttributeListIsNonEmptyAndCappedForZep(t *testing.T) {
	got := NormalizeAttributes(nil)
	want := []map[string]any{{
		"name": "details", "type": "text",
		"description": "Additional details about this ontology type.",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NormalizeAttributes(nil) = %v, want %v", got, want)
	}

	attrs := []any{nil}
	for i := 0; i < 12; i++ {
		attrs = append(attrs, "field_"+itoa(i))
	}
	normalized := NormalizeAttributes(attrs)

	if len(normalized) != MaxAttributes {
		t.Fatalf("len = %d, want %d", len(normalized), MaxAttributes)
	}
	for i, a := range normalized {
		if name, _ := a["name"].(string); name != "field_"+itoa(i) {
			t.Errorf("attrs[%d] = %v", i, a)
		}
	}
}

// ===== _validate_and_process =====

func testValidate(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	out, err := ValidateAndProcess(result)
	if err != nil {
		t.Fatalf("ValidateAndProcess: %v", err)
	}
	return out
}

func TestGeneratorNormalizesEntityAndEdgeAttributes(t *testing.T) {
	result := testValidate(t, map[string]any{
		"entity_types": []any{map[string]any{"name": "speaker", "attributes": []any{"role", nil}}},
		"edge_types":   []any{map[string]any{"name": "quotes", "attributes": []any{"source_url", map[string]any{}}}},
	})

	entities, _ := result["entity_types"].([]any)
	if len(entities) == 0 {
		t.Fatal("sin entity_types")
	}
	entity := entities[0].(map[string]any)
	wantAttrs := []any{map[string]any{"name": "role", "type": "text", "description": "role"}}
	if !reflect.DeepEqual(entity["attributes"], wantAttrs) {
		t.Errorf("entity attrs = %v, want %v", entity["attributes"], wantAttrs)
	}

	edges, _ := result["edge_types"].([]any)
	if len(edges) == 0 {
		t.Fatal("sin edge_types")
	}
	edge := edges[0].(map[string]any)
	wantEdgeAttrs := []any{map[string]any{"name": "source_url", "type": "text", "description": "source_url"}}
	if !reflect.DeepEqual(edge["attributes"], wantEdgeAttrs) {
		t.Errorf("edge attrs = %v, want %v", edge["attributes"], wantEdgeAttrs)
	}
}

func TestGeneratorAddsAPropertyToEmptyCustomTypes(t *testing.T) {
	result := testValidate(t, map[string]any{
		"entity_types": []any{map[string]any{"name": "speaker", "attributes": []any{}}},
		"edge_types":   []any{map[string]any{"name": "quotes", "attributes": []any{}}},
	})

	entities, _ := result["entity_types"].([]any)
	entity := entities[0].(map[string]any)
	if attrs, _ := entity["attributes"].([]any); attrs[0].(map[string]any)["name"] != "details" {
		t.Errorf("entity attrs = %v", entity["attributes"])
	}
	edges, _ := result["edge_types"].([]any)
	edge := edges[0].(map[string]any)
	if attrs, _ := edge["attributes"].([]any); attrs[0].(map[string]any)["name"] != "details" {
		t.Errorf("edge attrs = %v", edge["attributes"])
	}
}

func TestGeneratorIgnoresInvalidEntriesAndNormalizesEdgeNames(t *testing.T) {
	sourceTargets := []any{
		map[string]any{"source": "speaker", "target": "news outlet"},
		map[string]any{"source": "speaker", "target": "news outlet"},
		nil,
	}
	for i := 0; i < 12; i++ {
		target := "news outlet"
		if i != 0 {
			target = "Person"
		}
		sourceTargets = append(sourceTargets, map[string]any{"source": "speaker", "target": target})
	}

	result := testValidate(t, map[string]any{
		"entity_types": []any{"speaker", nil, float64(7), map[string]any{"name": "news outlet"}},
		"edge_types": []any{
			"unusable edge",
			nil,
			map[string]any{"name": "worksFor", "source_targets": sourceTargets},
			map[string]any{"name": "works-for", "source_targets": []any{}},
		},
	})

	entities, _ := result["entity_types"].([]any)
	if len(entities) < 4 {
		t.Fatalf("entity_types = %v (esperado >= 2 procesadas + 2 fallback)", entities)
	}
	var entityNames []string
	for _, e := range entities {
		entityNames = append(entityNames, e.(map[string]any)["name"].(string))
	}
	if entityNames[0] != "Speaker" || entityNames[1] != "NewsOutlet" {
		t.Errorf("entity_types[:2] = %v, want [Speaker NewsOutlet]", entityNames[:2])
	}
	var hasPerson, hasOrganization bool
	for _, n := range entityNames[2:] {
		if n == "Person" {
			hasPerson = true
		}
		if n == "Organization" {
			hasOrganization = true
		}
	}
	if !hasPerson || !hasOrganization {
		t.Errorf("fallback types faltantes: %v", entityNames)
	}

	edges, _ := result["edge_types"].([]any)
	if len(edges) != 1 {
		t.Fatalf("edge_types = %v, want solo [WORKS_FOR]", edges)
	}
	edge := edges[0].(map[string]any)
	if edge["name"] != "WORKS_FOR" {
		t.Errorf("edge name = %v", edge["name"])
	}
	wantTargets := []any{
		map[string]any{"source": "Speaker", "target": "NewsOutlet"},
		map[string]any{"source": "Speaker", "target": "Person"},
	}
	if !reflect.DeepEqual(edge["source_targets"], wantTargets) {
		t.Errorf("source_targets = %v, want %v", edge["source_targets"], wantTargets)
	}
}

func TestGeneratorCapsAfterDiscardingInvalidEdgeEndpoints(t *testing.T) {
	invalidFirst := []any{}
	for i := 0; i < MaxSourceTargets; i++ {
		invalidFirst = append(invalidFirst, map[string]any{
			"source": "Removed" + itoa(i), "target": "AlsoRemoved",
		})
	}
	result := testValidate(t, map[string]any{
		"entity_types": []any{map[string]any{"name": "person"}, map[string]any{"name": "organization"}},
		"edge_types": []any{
			map[string]any{
				"name":           "works_for",
				"source_targets": append(invalidFirst, map[string]any{"source": "person", "target": "organization"}),
			},
		},
	})

	edges, _ := result["edge_types"].([]any)
	if len(edges) != 1 {
		t.Fatalf("edge_types = %v", edges)
	}
	want := []any{map[string]any{"source": "Person", "target": "Organization"}}
	if !reflect.DeepEqual(edges[0].(map[string]any)["source_targets"], want) {
		t.Errorf("source_targets = %v, want %v", edges[0].(map[string]any)["source_targets"], want)
	}
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }
