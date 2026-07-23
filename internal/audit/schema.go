package audit

import (
	"fmt"
	"sort"
	"strings"
)

// The 2026-07-28 specification requires tool schemas to be valid JSON Schema
// 2020-12 and stricter hosts will reject legacy residue. This file is a
// targeted, high-signal STRUCTURAL inspector, not a full JSON Schema validator:
// it flags the specific legacy patterns and loose shapes that break or degrade
// under 2020-12 validation. Each returned finding names the offending keyword
// and the fix.
//
// References for the 2020-12 requirement:
//   - https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/
//   - https://stacktr.ee/blog/mcp-2026-spec-changes

// schemaFinding is an internal, un-graded result from the inspector. role is
// "inputSchema" or "outputSchema"; path locates the keyword within the schema.
type schemaFinding struct {
	severity Severity
	path     string
	message  string
	fix      string
}

// inspectSchema runs the structural checks over one tool schema. rootMustBeObject
// is true for inputSchema (MCP requires an object root) and false for
// outputSchema (2026-07-28 leaves output schemas unrestricted).
func inspectSchema(schema map[string]any, rootMustBeObject bool) []schemaFinding {
	var out []schemaFinding
	// Recursive walk for legacy keywords that may appear at any depth. The root
	// is passed through so a stray "definitions" can be cross-checked against
	// $ref targets anywhere in the schema.
	walkLegacy(schema, schema, "$", &out)
	// Root-level structural checks.
	out = append(out, inspectRoot(schema, rootMustBeObject)...)
	return out
}

// walkLegacy recursively flags draft-04/06/07 residue anywhere in the schema.
// root is the whole schema, used to resolve $ref cross-checks.
func walkLegacy(node, root any, path string, out *[]schemaFinding) {
	switch v := node.(type) {
	case map[string]any:
		// $schema pointing at a pre-2020-12 dialect.
		if raw, ok := v["$schema"]; ok {
			if s, ok := raw.(string); ok && isPre2020Dialect(s) {
				*out = append(*out, schemaFinding{
					severity: Warn,
					path:     join(path, "$schema"),
					message:  fmt.Sprintf("$schema declares a pre-2020-12 dialect (%q)", s),
					fix:      "set $schema to \"https://json-schema.org/draft/2020-12/schema\" or omit it; MCP 2026-07-28 validates tool schemas as 2020-12",
				})
			}
		}
		// draft-04 "id" instead of 2020-12 "$id".
		if _, ok := v["id"]; ok {
			if _, hasDollar := v["$id"]; !hasDollar {
				*out = append(*out, schemaFinding{
					severity: Warn,
					path:     join(path, "id"),
					message:  "uses draft-04 \"id\"; 2020-12 ignores it, losing the schema identifier",
					fix:      "rename \"id\" to \"$id\"",
				})
			}
		}
		// draft "definitions" instead of 2020-12 "$defs".
		if _, ok := v["definitions"]; ok {
			if _, hasDefs := v["$defs"]; !hasDefs {
				sev := Warn
				msg := "uses legacy \"definitions\"; 2020-12 treats it as an unknown keyword, not a schema store"
				if refsInto(root, "#/definitions/") {
					sev = Fail
					msg = "uses legacy \"definitions\" and a $ref targets #/definitions/*, which does not resolve under 2020-12"
				}
				*out = append(*out, schemaFinding{
					severity: sev,
					path:     join(path, "definitions"),
					message:  msg,
					fix:      "rename \"definitions\" to \"$defs\" and update $ref targets to #/$defs/*",
				})
			}
		}
		// Boolean-form exclusiveMinimum / exclusiveMaximum (draft-04 style).
		for _, kw := range []string{"exclusiveMinimum", "exclusiveMaximum"} {
			if raw, ok := v[kw]; ok {
				if _, isBool := raw.(bool); isBool {
					*out = append(*out, schemaFinding{
						severity: Fail,
						path:     join(path, kw),
						message:  fmt.Sprintf("%s is a boolean (draft-04 form); 2020-12 requires a number", kw),
						fix:      fmt.Sprintf("replace the boolean %s with a numeric bound, e.g. \"%s\": 0", kw, kw),
					})
				}
			}
		}
		// Recurse into nested schemas in a stable key order.
		for _, k := range sortedKeys(v) {
			walkLegacy(v[k], root, join(path, k), out)
		}
	case []any:
		for i, item := range v {
			walkLegacy(item, root, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

// inspectRoot checks the top-level shape a tool schema must present.
func inspectRoot(schema map[string]any, mustBeObject bool) []schemaFinding {
	var out []schemaFinding

	typ, hasType := schema["type"]

	if !hasType {
		out = append(out, schemaFinding{
			severity: Warn,
			path:     "$.type",
			message:  "schema has no top-level \"type\"",
			fix:      "declare \"type\": \"object\" so 2020-12 validators and stricter hosts accept the schema",
		})
	} else if mustBeObject && !typeAllowsObject(typ) {
		out = append(out, schemaFinding{
			severity: Fail,
			path:     "$.type",
			message:  fmt.Sprintf("input schema root type is %s, but MCP tool inputSchema must be an object", describeType(typ)),
			fix:      "set the root \"type\" to \"object\" and put fields under \"properties\"",
		})
	}

	// properties, when present, must be an object.
	if props, ok := schema["properties"]; ok {
		pm, isMap := props.(map[string]any)
		if !isMap {
			out = append(out, schemaFinding{
				severity: Fail,
				path:     "$.properties",
				message:  "\"properties\" is not an object",
				fix:      "make \"properties\" a JSON object mapping field names to subschemas",
			})
		} else {
			out = append(out, inspectRequired(schema, pm)...)
			// Loose-schema heuristic: an object with no properties and no
			// constraints validates almost anything; stricter hosts may reject.
			if mustBeObject && len(pm) == 0 && looseObject(schema) {
				out = append(out, schemaFinding{
					severity: Warn,
					path:     "$",
					message:  "object schema declares no properties and no constraints (accepts any object)",
					fix:      "declare the expected \"properties\" (and \"required\"/\"additionalProperties\") so 2020-12 validation is meaningful",
				})
			}
		}
	} else if mustBeObject && typeAllowsObject(orString(typ, "object")) {
		// type object but no properties block at all -> also loose.
		if looseObject(schema) {
			out = append(out, schemaFinding{
				severity: Warn,
				path:     "$",
				message:  "object schema declares no \"properties\" and no constraints (accepts any object)",
				fix:      "add a \"properties\" block describing the tool's arguments",
			})
		}
	} else {
		// required without properties is checked below regardless.
		out = append(out, inspectRequired(schema, nil)...)
	}

	return out
}

// inspectRequired validates the "required" keyword shape and cross-checks it
// against declared properties.
func inspectRequired(schema map[string]any, props map[string]any) []schemaFinding {
	raw, ok := schema["required"]
	if !ok {
		return nil
	}
	arr, isArr := raw.([]any)
	if !isArr {
		return []schemaFinding{{
			severity: Fail,
			path:     "$.required",
			message:  "\"required\" is not an array",
			fix:      "make \"required\" an array of property-name strings",
		}}
	}
	var out []schemaFinding
	var missing []string
	for i, item := range arr {
		name, isStr := item.(string)
		if !isStr {
			out = append(out, schemaFinding{
				severity: Fail,
				path:     fmt.Sprintf("$.required[%d]", i),
				message:  "\"required\" contains a non-string entry",
				fix:      "every \"required\" entry must be a property-name string",
			})
			continue
		}
		if props != nil {
			if _, present := props[name]; !present {
				missing = append(missing, name)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		out = append(out, schemaFinding{
			severity: Warn,
			path:     "$.required",
			message:  fmt.Sprintf("\"required\" names properties not in \"properties\": %s", strings.Join(missing, ", ")),
			fix:      "add the missing entries to \"properties\" or drop them from \"required\"",
		})
	}
	return out
}

// looseObject reports whether an object schema carries no meaningful
// constraints beyond its type.
func looseObject(schema map[string]any) bool {
	constraints := []string{
		"properties", "required", "additionalProperties", "patternProperties",
		"minProperties", "maxProperties", "propertyNames", "allOf", "anyOf",
		"oneOf", "not", "$ref", "if", "then", "else", "dependentRequired",
		"dependentSchemas", "unevaluatedProperties", "enum", "const",
	}
	for _, k := range constraints {
		if v, ok := schema[k]; ok {
			// An empty properties map is not a real constraint.
			if k == "properties" {
				if pm, isMap := v.(map[string]any); isMap && len(pm) == 0 {
					continue
				}
			}
			return false
		}
	}
	return true
}

// isPre2020Dialect reports whether a $schema URI names a draft older than
// 2020-12.
func isPre2020Dialect(s string) bool {
	l := strings.ToLower(s)
	for _, marker := range []string{"draft-04", "draft-06", "draft-07", "draft/2019-09", "draft-03"} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

// refsInto reports whether any $ref in the schema targets the given fragment
// prefix (used to decide whether stray "definitions" actually breaks a $ref).
func refsInto(node any, prefix string) bool {
	switch v := node.(type) {
	case map[string]any:
		if raw, ok := v["$ref"]; ok {
			if s, ok := raw.(string); ok && strings.Contains(s, prefix) {
				return true
			}
		}
		for _, val := range v {
			if refsInto(val, prefix) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if refsInto(item, prefix) {
				return true
			}
		}
	}
	return false
}

// typeAllowsObject reports whether a "type" value permits an object instance.
// "type" may be a string or an array of strings in JSON Schema.
func typeAllowsObject(typ any) bool {
	switch t := typ.(type) {
	case string:
		return t == "object"
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok && s == "object" {
				return true
			}
		}
	}
	return false
}

func describeType(typ any) string {
	switch t := typ.(type) {
	case string:
		return fmt.Sprintf("%q", t)
	case []any:
		var parts []string
		for _, item := range t {
			parts = append(parts, fmt.Sprintf("%v", item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%v", typ)
	}
}

func orString(v any, fallback string) any {
	if v == nil {
		return fallback
	}
	return v
}

func join(path, key string) string {
	if path == "$" {
		return "$." + key
	}
	return path + "." + key
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
