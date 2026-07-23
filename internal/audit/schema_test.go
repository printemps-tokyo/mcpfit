package audit

import (
	"strings"
	"testing"
)

// findBy returns the first finding whose message contains sub, or fails.
func findBy(t *testing.T, fs []schemaFinding, sub string) schemaFinding {
	t.Helper()
	for _, f := range fs {
		if strings.Contains(f.message, sub) {
			return f
		}
	}
	t.Fatalf("no schema finding containing %q in %+v", sub, fs)
	return schemaFinding{}
}

func hasMessage(fs []schemaFinding, sub string) bool {
	for _, f := range fs {
		if strings.Contains(f.message, sub) {
			return true
		}
	}
	return false
}

func TestInspectSchema_CleanObjectPasses(t *testing.T) {
	schema := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []any{"query"},
	}
	if fs := inspectSchema(schema, true); len(fs) != 0 {
		t.Errorf("clean schema produced findings: %+v", fs)
	}
}

func TestInspectSchema_BooleanExclusiveMinimumFails(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"n": map[string]any{"type": "integer", "exclusiveMinimum": true},
		},
	}
	f := findBy(t, inspectSchema(schema, true), "exclusiveMinimum is a boolean")
	if f.severity != Fail {
		t.Errorf("boolean exclusiveMinimum severity = %v, want Fail", f.severity)
	}
}

func TestInspectSchema_LegacyIdAndDefinitions(t *testing.T) {
	schema := map[string]any{
		"type":        "object",
		"id":          "urn:x",
		"definitions": map[string]any{"a": map[string]any{"type": "string"}},
		"properties":  map[string]any{"x": map[string]any{"type": "string"}},
	}
	fs := inspectSchema(schema, true)
	if !hasMessage(fs, "draft-04 \"id\"") {
		t.Errorf("expected id finding, got %+v", fs)
	}
	def := findBy(t, fs, "legacy \"definitions\"")
	if def.severity != Warn {
		t.Errorf("definitions without $ref severity = %v, want Warn", def.severity)
	}
}

func TestInspectSchema_DefinitionsWithRefFails(t *testing.T) {
	schema := map[string]any{
		"type":        "object",
		"definitions": map[string]any{"a": map[string]any{"type": "string"}},
		"properties": map[string]any{
			"x": map[string]any{"$ref": "#/definitions/a"},
		},
	}
	def := findBy(t, inspectSchema(schema, true), "does not resolve")
	if def.severity != Fail {
		t.Errorf("definitions with #/definitions/ $ref severity = %v, want Fail", def.severity)
	}
}

func TestInspectSchema_PreDialect(t *testing.T) {
	schema := map[string]any{
		"$schema":    "http://json-schema.org/draft-07/schema#",
		"type":       "object",
		"properties": map[string]any{"x": map[string]any{"type": "string"}},
	}
	f := findBy(t, inspectSchema(schema, true), "pre-2020-12 dialect")
	if f.severity != Warn {
		t.Errorf("pre-2020-12 dialect severity = %v, want Warn", f.severity)
	}
}

func TestInspectSchema_MissingType(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{"x": map[string]any{"type": "string"}},
	}
	f := findBy(t, inspectSchema(schema, true), "no top-level \"type\"")
	if f.severity != Warn {
		t.Errorf("missing type severity = %v, want Warn", f.severity)
	}
}

func TestInspectSchema_NonObjectRootForInput(t *testing.T) {
	schema := map[string]any{"type": "string"}
	f := findBy(t, inspectSchema(schema, true), "must be an object")
	if f.severity != Fail {
		t.Errorf("non-object input root severity = %v, want Fail", f.severity)
	}
	// For output schemas, a non-object root is allowed (unrestricted).
	if fs := inspectSchema(schema, false); hasMessage(fs, "must be an object") {
		t.Errorf("output schema wrongly flagged non-object root: %+v", fs)
	}
}

func TestInspectSchema_MalformedProperties(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": []any{"nope"}}
	f := findBy(t, inspectSchema(schema, true), "\"properties\" is not an object")
	if f.severity != Fail {
		t.Errorf("malformed properties severity = %v, want Fail", f.severity)
	}
}

func TestInspectSchema_RequiredShapeAndCrossCheck(t *testing.T) {
	// required not an array -> Fail
	f := findBy(t, inspectSchema(map[string]any{"type": "object", "properties": map[string]any{}, "required": "x"}, true), "\"required\" is not an array")
	if f.severity != Fail {
		t.Errorf("required-not-array severity = %v, want Fail", f.severity)
	}
	// required names a missing property -> Warn
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"a": map[string]any{"type": "string"}},
		"required":   []any{"a", "b"},
	}
	f2 := findBy(t, inspectSchema(schema, true), "names properties not in")
	if f2.severity != Warn || !strings.Contains(f2.message, "b") {
		t.Errorf("required cross-check = %+v", f2)
	}
}

func TestInspectSchema_LooseObjectWarns(t *testing.T) {
	// object with no properties and no constraints
	f := findBy(t, inspectSchema(map[string]any{"type": "object"}, true), "accepts any object")
	if f.severity != Warn {
		t.Errorf("loose object severity = %v, want Warn", f.severity)
	}
	// object with an explicit constraint is not loose
	notLoose := map[string]any{"type": "object", "additionalProperties": false}
	if fs := inspectSchema(notLoose, true); hasMessage(fs, "accepts any object") {
		t.Errorf("constrained object wrongly flagged loose: %+v", fs)
	}
}
