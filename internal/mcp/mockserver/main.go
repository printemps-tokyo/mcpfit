// Command mockserver is a tiny, offline MCP server used only by mcpfit's tests.
// It speaks JSON-RPC 2.0 over stdio and returns crafted initialize / list
// responses. MOCK_MODE selects the persona:
//
//	modern  a clean 2026-07-28 server: valid 2020-12 schemas, no sessions,
//	        no tasks capability, cache hints on lists.
//	legacy  a pre-stateless server: draft-04 schema residue and a declared
//	        tasks capability whose blocking tasks/list still responds.
//
// It is not a general-purpose server; it only answers what the tests exercise.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func main() {
	mode := os.Getenv("MOCK_MODE")
	if mode == "" {
		mode = "modern"
	}
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for {
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			handle(line, mode, out)
			out.Flush()
		}
		if err != nil {
			return
		}
	}
}

func handle(line, mode string, out *bufio.Writer) {
	var req request
	if json.Unmarshal([]byte(line), &req) != nil {
		return
	}
	if req.ID == nil {
		return // notification: nothing to answer
	}
	switch req.Method {
	case "initialize":
		reply(out, *req.ID, initializeResult(mode))
	case "tools/list":
		reply(out, *req.ID, toolsList(mode))
	case "resources/list":
		reply(out, *req.ID, map[string]any{"resources": []any{}})
	case "prompts/list":
		reply(out, *req.ID, map[string]any{"prompts": []any{}})
	case "tasks/list":
		if mode == "legacy" {
			reply(out, *req.ID, map[string]any{"tasks": []any{}})
		} else {
			replyError(out, *req.ID, -32601, "method not found")
		}
	default:
		replyError(out, *req.ID, -32601, "method not found")
	}
}

func initializeResult(mode string) map[string]any {
	if mode == "legacy" {
		return map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities": map[string]any{
				"tools": map[string]any{},
				"tasks": map[string]any{},
			},
			"serverInfo": map[string]any{"name": "legacy-mock", "version": "0.1.0"},
		}
	}
	return map[string]any{
		"protocolVersion": "2026-07-28",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{"name": "modern-mock", "version": "1.0.0"},
	}
}

func toolsList(mode string) map[string]any {
	if mode == "legacy" {
		return map[string]any{
			"tools": []any{
				map[string]any{
					"name": "legacy_tool",
					"inputSchema": map[string]any{
						"$schema":     "http://json-schema.org/draft-04/schema#",
						"id":          "urn:legacy:tool",
						"type":        "object",
						"definitions": map[string]any{"pos": map[string]any{"type": "number"}},
						"properties": map[string]any{
							"count": map[string]any{
								"type":             "integer",
								"exclusiveMinimum": true,
								"minimum":          0,
							},
						},
						"required": []any{"count", "missing"},
					},
				},
			},
		}
	}
	// modern: clean schema plus cache hints on the list result.
	return map[string]any{
		"tools": []any{
			map[string]any{
				"name": "search",
				"inputSchema": map[string]any{
					"$schema": "https://json-schema.org/draft/2020-12/schema",
					"type":    "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
					"required": []any{"query"},
				},
				"outputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"results": map[string]any{"type": "array"},
					},
				},
			},
		},
		"ttlMs":      60000,
		"cacheScope": "public",
	}
}

func reply(out *bufio.Writer, id int, result any) {
	writeJSON(out, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func replyError(out *bufio.Writer, id, code int, msg string) {
	writeJSON(out, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func writeJSON(out *bufio.Writer, v any) {
	b, _ := json.Marshal(v)
	out.Write(b)
	out.WriteByte('\n')
}
