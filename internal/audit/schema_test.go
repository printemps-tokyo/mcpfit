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
		"properties":  map[string]any{"x": map[string]any{"$ref": "#/definitions/a"}},
	}
	fs := inspectSchema(schema, true)
	if !hasMessage(fs, "draft-04 \"id\"") {
		t.Errorf("expected id finding, got %+v", fs)
	}
	// 2020-12 still defines "definitions" (deprecated), so it is a WARN even
	// when a $ref points into it.
	def := findBy(t, fs, "\"definitions\"")
	if def.severity != Warn {
		t.Errorf("definitions severity = %v, want Warn", def.severity)
	}
}

// Names under "properties" (and data under "default"/"enum") are not schema
// keywords: a property called "id" or "definitions" is not draft-04 residue.
func TestInspectSchema_PropertyNamesAreNotKeywords(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string"},
			"definitions": map[string]any{"type": "array"},
			"opts": map[string]any{
				"type":    "object",
				"default": map[string]any{"id": "x", "exclusiveMinimum": true},
			},
		},
	}
	if fs := inspectSchema(schema, true); len(fs) != 0 {
		t.Errorf("property names wrongly flagged: %+v", fs)
	}
}

// A schema may declare another dialect; it is then validated against that
// dialect, so draft-04 keywords are not residue. Only an INFO is reported.
func TestInspectSchema_ExplicitOlderDialect(t *testing.T) {
	schema := map[string]any{
		"$schema":     "http://json-schema.org/draft-04/schema#",
		"id":          "urn:x",
		"type":        "object",
		"definitions": map[string]any{"a": map[string]any{"type": "string"}},
		"properties": map[string]any{
			"n": map[string]any{"type": "integer", "minimum": 0, "exclusiveMinimum": true},
		},
	}
	fs := inspectSchema(schema, true)
	if len(fs) != 1 {
		t.Fatalf("want exactly the dialect note, got %+v", fs)
	}
	f := findBy(t, fs, "dialect other than 2020-12")
	if f.severity != Info {
		t.Errorf("explicit dialect severity = %v, want Info", f.severity)
	}
	// Root rules still apply regardless of dialect.
	schema["type"] = "string"
	if f := findBy(t, inspectSchema(schema, true), "must be an object"); f.severity != Fail {
		t.Errorf("non-object root under draft-04 severity = %v, want Fail", f.severity)
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
	// An outputSchema may be any valid schema; a missing root type is fine.
	if fs := inspectSchema(schema, false); len(fs) != 0 {
		t.Errorf("output schema without a root type wrongly flagged: %+v", fs)
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

func TestInspectSchema_LooseObjectIsInfo(t *testing.T) {
	// object with no properties and no constraints: the spec lists
	// {"type": "object"} as a valid schema for a tool without parameters.
	f := findBy(t, inspectSchema(map[string]any{"type": "object"}, true), "accepts any object")
	if f.severity != Info {
		t.Errorf("loose object severity = %v, want Info", f.severity)
	}
	// object with an explicit constraint is not loose
	notLoose := map[string]any{"type": "object", "additionalProperties": false}
	if fs := inspectSchema(notLoose, true); hasMessage(fs, "accepts any object") {
		t.Errorf("constrained object wrongly flagged loose: %+v", fs)
	}
}
