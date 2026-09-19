package llm

import (
	"reflect"
	"testing"
)

// TestNormalizeFunctionParametersFlattensTopLevelOneOf reproduces the
// OpenStreetMap query_bbox schema rejected by OpenAI function tools.
func TestNormalizeFunctionParametersFlattensTopLevelOneOf(t *testing.T) {
	originalSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"bbox":  map[string]any{"type": "string"},
			"south": map[string]any{"type": "number"},
			"west":  map[string]any{"type": "number"},
			"north": map[string]any{"type": "number"},
			"east":  map[string]any{"type": "number"},
		},
		"oneOf": []any{
			map[string]any{"required": []any{"bbox"}},
			map[string]any{"required": []any{"south", "west", "north", "east"}},
		},
	}
	normalizedSchema, err := normalizeFunctionParameters(originalSchema)
	if err != nil {
		t.Fatalf("normalizeFunctionParameters returned an error: %v", err)
	}
	if _, stillHasOneOf := normalizedSchema["oneOf"]; stillHasOneOf {
		t.Fatal("normalized schema still contains top-level oneOf")
	}
	if _, originalStillHasOneOf := originalSchema["oneOf"]; !originalStillHasOneOf {
		t.Fatal("normalization mutated the provider-independent source schema")
	}
	if normalizedSchema["type"] != "object" {
		t.Fatalf("normalized type = %#v, want object", normalizedSchema["type"])
	}
	if _, hasRequired := normalizedSchema["required"]; hasRequired {
		t.Fatalf("mutually exclusive alternatives became jointly required: %#v", normalizedSchema["required"])
	}
	properties := normalizedSchema["properties"].(map[string]any)
	if len(properties) != 5 {
		t.Fatalf("normalized properties = %#v, want all five bbox inputs", properties)
	}
}

// TestNormalizeFunctionParametersMergesComposedProperties verifies allOf
// requirements and properties from anyOf alternatives survive flattening.
func TestNormalizeFunctionParametersMergesComposedProperties(t *testing.T) {
	normalizedSchema, err := normalizeFunctionParameters(map[string]any{
		"allOf": []any{
			map[string]any{
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
				"required":   []any{"query"},
			},
		},
		"anyOf": []any{
			map[string]any{
				"properties": map[string]any{"limit": map[string]any{"type": "integer"}},
				"required":   []any{"limit"},
			},
			map[string]any{
				"properties": map[string]any{"radius": map[string]any{"type": "number"}},
				"required":   []any{"radius"},
			},
		},
	})
	if err != nil {
		t.Fatalf("normalizeFunctionParameters returned an error: %v", err)
	}
	properties := normalizedSchema["properties"].(map[string]any)
	for _, expectedProperty := range []string{"query", "limit", "radius"} {
		if _, found := properties[expectedProperty]; !found {
			t.Fatalf("normalized properties = %#v, missing %q", properties, expectedProperty)
		}
	}
	if !reflect.DeepEqual(normalizedSchema["required"], []string{"query"}) {
		t.Fatalf("normalized required = %#v, want only query", normalizedSchema["required"])
	}
}

// TestNormalizeFunctionParametersRemovesUnsupportedRootConstraints verifies
// every non-composition keyword named by the OpenAI validation error is removed.
func TestNormalizeFunctionParametersRemovesUnsupportedRootConstraints(t *testing.T) {
	normalizedSchema, err := normalizeFunctionParameters(map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"enum":       []any{map[string]any{}},
		"const":      map[string]any{},
		"not":        map[string]any{"required": []any{"unsupported"}},
	})
	if err != nil {
		t.Fatalf("normalizeFunctionParameters returned an error: %v", err)
	}
	for _, unsupportedKeyword := range []string{"enum", "const", "not"} {
		if _, found := normalizedSchema[unsupportedKeyword]; found {
			t.Fatalf("normalized schema still contains top-level %s", unsupportedKeyword)
		}
	}
}
