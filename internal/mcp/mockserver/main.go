// Command mockserver is a tiny, offline MCP server used only by mcpfit's tests.
// It speaks JSON-RPC 2.0 over stdio and returns crafted discover / initialize /
// list responses. MOCK_MODE selects the persona:
//
//	modern         a clean 2026-07-28 server: answers server/discover, rejects
//	               initialize, requires per-request _meta, valid 2020-12
//	               schemas, no tasks capability, caching hints on every
//	               cacheable result.
//	legacy         an initialize-based server: rejects server/discover, has
//	               draft-04 residue in a schema that defaults to 2020-12, and
//	               declares a tasks capability whose tasks/list still responds.
//	legacy-silent  like legacy, but never answers server/discover, as some
//	               legacy servers do for requests sent before initialize.
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
	if mode == "modern" {
		handleModern(req, out)
		return
	}
	switch req.Method {
	case "server/discover":
		if mode == "legacy-silent" {
			return
		}
		replyError(out, *req.ID, -32601, "method not found")
	case "initialize":
		reply(out, *req.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities": map[string]any{
				"tools": map[string]any{},
				"tasks": map[string]any{},
			},
			"serverInfo": map[string]any{"name": "legacy-mock", "version": "0.1.0"},
		})
	case "tools/list":
		reply(out, *req.ID, toolsList(mode))
	case "resources/list":
		reply(out, *req.ID, map[string]any{"resources": []any{}})
	case "prompts/list":
		reply(out, *req.ID, map[string]any{"prompts": []any{}})
	case "tasks/list":
		reply(out, *req.ID, map[string]any{"tasks": []any{}})
	default:
		replyError(out, *req.ID, -32601, "method not found")
	}
}

// handleModern answers as a 2026-07-28 server: there is no initialize, and a
// request without the required per-request metadata is malformed.
func handleModern(req request, out *bufio.Writer) {
	var params struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	_ = json.Unmarshal(req.Params, &params)
	var version string
	_ = json.Unmarshal(params.Meta["io.modelcontextprotocol/protocolVersion"], &version)
	_, hasCaps := params.Meta["io.modelcontextprotocol/clientCapabilities"]

	switch {
	case req.Method == "initialize":
		replyError(out, *req.ID, -32601, "method not found; this server supports 2026-07-28 only")
		return
	case version == "" || !hasCaps:
		replyError(out, *req.ID, -32602, "missing required _meta fields")
		return
	case version != "2026-07-28":
		writeJSON(out, map[string]any{"jsonrpc": "2.0", "id": *req.ID, "error": map[string]any{
			"code":    -32022,
			"message": "Unsupported protocol version",
			"data":    map[string]any{"supported": []any{"2026-07-28"}, "requested": version},
		}})
		return
	}

	switch req.Method {
	case "server/discover":
		reply(out, *req.ID, cacheable(map[string]any{
			"supportedVersions": []any{"2026-07-28"},
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"_meta": map[string]any{
				"io.modelcontextprotocol/serverInfo": map[string]any{"name": "modern-mock", "version": "1.0.0"},
			},
		}))
	case "tools/list":
		reply(out, *req.ID, cacheable(toolsList("modern")))
	case "resources/list":
		reply(out, *req.ID, cacheable(map[string]any{"resources": []any{}}))
	case "prompts/list":
		reply(out, *req.ID, cacheable(map[string]any{"prompts": []any{}}))
	default:
		replyError(out, *req.ID, -32601, "method not found")
	}
}

// cacheable adds the fields 2026-07-28 requires on a complete, cacheable result.
func cacheable(result map[string]any) map[string]any {
	result["resultType"] = "complete"
	result["ttlMs"] = 60000
	result["cacheScope"] = "public"
	return result
}

func toolsList(mode string) map[string]any {
	if mode != "modern" {
		return map[string]any{
			"tools": []any{
				map[string]any{
					"name": "legacy_tool",
					"inputSchema": map[string]any{
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
	// modern: clean schemas; the caller adds the caching hints.
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
