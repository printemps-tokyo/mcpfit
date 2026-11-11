package audit

import (
	"fmt"
	"sort"
	"strings"
)

// The 2026-07-28 specification defines how JSON Schema is used in MCP: a
// schema without "$schema" is JSON Schema 2020-12, a schema MAY declare another
// dialect with "$schema", every schema MUST be valid for its declared or
// default dialect, and implementations are only required to support 2020-12.
// A tool inputSchema must be a schema object whose root "type" is "object";
// an outputSchema may be any valid schema.
//
// This file is a targeted, high-signal STRUCTURAL inspector, not a full JSON
// Schema validator: it flags specific pre-2020-12 residue in schemas that are
// (by default or by declaration) 2020-12, plus malformed shapes. Each returned
// finding names the offending keyword and the fix.
//
// References:
//   - https://modelcontextprotocol.io/specification/2026-07-28/basic#json-schema-usage
//   - https://modelcontextprotocol.io/specification/2026-07-28/server/tools#tool
//   - https://json-schema.org/draft/2020-12/schema

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
// outputSchema (any valid schema is allowed).
func inspectSchema(schema map[string]any, rootMustBeObject bool) []schemaFinding {
	var out []schemaFinding
	if dialect, ok := schema["$schema"].(string); ok && !is2020Dialect(dialect) {
		// The schema is validated against the dialect it declares, so keywords
		// that are residue under 2020-12 may be perfectly valid here. Report
		// the dialect and skip the 2020-12 residue checks.
		out = append(out, schemaFinding{
			severity: Info,
			path:     "$.$schema",
			message:  fmt.Sprintf("$schema declares a dialect other than 2020-12 (%q); this is allowed, but implementations are only required to support 2020-12, so the 2020-12 keyword checks were skipped", dialect),
			fix:      "prefer JSON Schema 2020-12 (recommended by the spec; it is the default when $schema is omitted) unless the clients you target document support for this dialect",
		})
	} else {
		// Recursive walk for pre-2020-12 keywords that may appear at any depth.
		walkLegacy(schema, "$", &out)
	}
	// Root-level structural checks.
	out = append(out, inspectRoot(schema, rootMustBeObject)...)
	return out
}

// schemaMapKeywords hold a map from an arbitrary name to a subschema, so their
// keys are names, not schema keywords. dataKeywords hold instance data, not
// schemas, and are not walked at all.
var (
	schemaMapKeywords = map[string]bool{
		"properties": true, "patternProperties": true, "$defs": true,
		"definitions": true, "dependentSchemas": true,
	}
	dataKeywords = map[string]bool{
		"enum": true, "const": true, "default": true, "examples": true,
	}
)

// walkLegacy recursively flags draft-04/06/07 residue in a schema that is
// validated as 2020-12. node is a schema (or an array of schemas).
func walkLegacy(node any, path string, out *[]schemaFinding) {
	switch v := node.(type) {
	case map[string]any:
		// draft-04 "id" instead of 2020-12 "$id". The value must be a string:
		// that is what a schema identifier looks like.
		if raw, ok := v["id"]; ok {
			if _, isString := raw.(string); isString {
				if _, hasDollar := v["$id"]; !hasDollar {
					*out = append(*out, schemaFinding{
						severity: Warn,
						path:     join(path, "id"),
						message:  "uses draft-04 \"id\"; 2020-12 ignores it, losing the schema identifier",
						fix:      "rename \"id\" to \"$id\"",
					})
				}
			}
		}
		// "definitions" instead of "$defs". The 2020-12 meta-schema still
		// defines "definitions", marked deprecated, so this is not a breakage.
		if _, ok := v["definitions"]; ok {
			if _, hasDefs := v["$defs"]; !hasDefs {
				*out = append(*out, schemaFinding{
					severity: Warn,
					path:     join(path, "definitions"),
					message:  "uses \"definitions\", which 2020-12 keeps only as a deprecated keyword replaced by \"$defs\"",
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
						fix:      fmt.Sprintf("replace the boolean %s with a numeric bound, e.g. \"%s\": 0, or declare the dialect the schema is written in with $schema", kw, kw),
					})
				}
			}
		}
		// Recurse into nested schemas in a stable key order.
		for _, k := range sortedKeys(v) {
			child := join(path, k)
			switch {
			case dataKeywords[k]:
				// Instance data, not a schema.
			case schemaMapKeywords[k]:
				if named, ok := v[k].(map[string]any); ok {
					for _, name := range sortedKeys(named) {
						walkLegacy(named[name], join(child, name), out)
					}
				}
			default:
				walkLegacy(v[k], child, out)
			}
		}
	case []any:
		for i, item := range v {
			walkLegacy(item, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

// inspectRoot checks the top-level shape a tool schema must present.
func inspectRoot(schema map[string]any, mustBeObject bool) []schemaFinding {
	var out []schemaFinding

	typ, hasType := schema["type"]

	if !hasType {
		// Only inputSchema has a required root type; an outputSchema may be
		// any valid schema, so a missing "type" there is not reported.
		if mustBeObject {
			out = append(out, schemaFinding{
				severity: Warn,
				path:     "$.type",
				message:  "schema has no top-level \"type\"; MCP requires the root of a tool inputSchema to declare \"type\": \"object\"",
				fix:      "declare \"type\": \"object\" at the root of the inputSchema",
			})
		}
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
			// Loose-schema note: an object with no properties and no
			// constraints accepts any object. The spec lists this as a valid
			// form for tools without parameters, so it is INFO only.
			if mustBeObject && len(pm) == 0 && looseObject(schema) {
				out = append(out, looseFinding())
			}
		}
	} else if mustBeObject && typeAllowsObject(orString(typ, "object")) {
		// type object but no properties block at all -> also loose.
		if looseObject(schema) {
			out = append(out, looseFinding())
		}
	} else {
		// required without properties is checked below regardless.
		out = append(out, inspectRequired(schema, nil)...)
	}

	return out
}

func looseFinding() schemaFinding {
	return schemaFinding{
		severity: Info,
		path:     "$",
		message:  "object schema declares no properties and no constraints (accepts any object)",
		fix:      "this is valid; for a tool with no parameters the spec recommends {\"type\": \"object\", \"additionalProperties\": false}, otherwise declare the expected \"properties\"",
	}
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

// is2020Dialect reports whether a $schema URI names JSON Schema 2020-12.
func is2020Dialect(s string) bool {
	return strings.Contains(strings.ToLower(s), "draft/2020-12")
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
