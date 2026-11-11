package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/printemps-tokyo/mcpfit/internal/audit"
)

// jsonRPCHandler crafts responses for a legacy Streamable HTTP server: it
// issues an Mcp-Session-Id header and answers initialize/list methods.
func newLegacyHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)

		w.Header().Set("Mcp-Session-Id", "sess-42")
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted) // notification
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "http-mock", "version": "0.1.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{
					"name":        "t",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}, "required": []any{"q"}},
				},
			}}
		default:
			result = map[string]any{}
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestHTTPProbe_SessionAndNoAuthFlagged(t *testing.T) {
	srv := newLegacyHTTPServer(t)
	defer srv.Close()

	c, err := New(Options{URL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	_, obs, err := c.Probe()
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !obs.SessionIDIssued || obs.SessionIDValue != "sess-42" {
		t.Errorf("session not captured: issued=%v value=%q", obs.SessionIDIssued, obs.SessionIDValue)
	}
	if obs.AuthCredentialsProvided {
		t.Errorf("no Authorization header was sent, but AuthCredentialsProvided=true")
	}
	if obs.AuthChallenged {
		t.Errorf("server did not challenge, but AuthChallenged=true")
	}
	if obs.Era != audit.EraLegacy {
		t.Errorf("era = %q, want legacy (server/discover returned no supportedVersions)", obs.Era)
	}
	if obs.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion = %q", obs.ProtocolVersion)
	}
	if len(obs.Tools) != 1 || obs.Tools[0].Name != "t" {
		t.Errorf("tools = %+v", obs.Tools)
	}
}

// newModernHTTPServer answers as a 2026-07-28 Streamable HTTP server: it
// validates the required request headers against the body, has no initialize
// and no sessions, and returns JSON-RPC errors with the status codes that
// revision defines.
func newModernHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
			Params struct {
				Meta map[string]json.RawMessage `json:"_meta"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if req.ID == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fail := func(status, code int, msg string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "error": map[string]any{"code": code, "message": msg}})
		}
		var version string
		_ = json.Unmarshal(req.Params.Meta["io.modelcontextprotocol/protocolVersion"], &version)
		_, hasCaps := req.Params.Meta["io.modelcontextprotocol/clientCapabilities"]
		switch {
		case version == "" || !hasCaps:
			fail(http.StatusBadRequest, -32602, "missing required _meta fields")
			return
		case r.Header.Get("MCP-Protocol-Version") != version || r.Header.Get("Mcp-Method") != req.Method:
			fail(http.StatusBadRequest, -32020, "header mismatch")
			return
		}
		var result map[string]any
		switch req.Method {
		case "server/discover":
			result = map[string]any{
				"supportedVersions": []any{"2026-07-28"},
				"capabilities": map[string]any{
					"tools":      map[string]any{},
					"extensions": map[string]any{"io.modelcontextprotocol/tasks": map[string]any{}},
				},
				"_meta": map[string]any{
					"io.modelcontextprotocol/serverInfo": map[string]any{"name": "http-modern", "version": "2.0.0"},
				},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{
					"name":        "t",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}, "required": []any{"q"}},
				},
			}}
		case "prompts/list":
			// Deliberately missing cacheScope.
			result = map[string]any{"prompts": []any{}, "resultType": "complete", "ttlMs": 0}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
			return
		default:
			// resources/list and the removed tasks/list are not implemented.
			fail(http.StatusNotFound, -32601, "Method not found")
			return
		}
		result["resultType"] = "complete"
		result["ttlMs"] = 1000.0
		result["cacheScope"] = "private"
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
	}))
}

func TestHTTPProbe_ModernServer(t *testing.T) {
	srv := newModernHTTPServer(t)
	defer srv.Close()

	c, err := New(Options{URL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	_, obs, err := c.Probe()
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if obs.Era != audit.EraModern || obs.ProtocolVersion != "2026-07-28" {
		t.Errorf("era/version = %q/%q, want modern/2026-07-28", obs.Era, obs.ProtocolVersion)
	}
	if obs.ServerName != "http-modern" || obs.ServerVersion != "2.0.0" {
		t.Errorf("server = %q %q", obs.ServerName, obs.ServerVersion)
	}
	if obs.SessionIDIssued {
		t.Errorf("no session header was sent, but SessionIDIssued=true")
	}
	if len(obs.Tools) != 1 {
		t.Errorf("tools = %+v, want 1 (a 404 JSON-RPC error on other lists must not abort)", obs.Tools)
	}
	// The extension is declared and the removed tasks/list answers 404.
	if !obs.TasksCapability || len(obs.TaskMethodsRemoved) != 0 {
		t.Errorf("tasks: capability=%v removed=%v", obs.TasksCapability, obs.TaskMethodsRemoved)
	}
	// discover, tools/list and prompts/list were inspected; only prompts/list
	// lacks a hint.
	if obs.CacheableResults != 3 || len(obs.CacheHintsMissing) != 1 || obs.CacheHintsMissing[0] != "prompts/list" {
		t.Errorf("cache hints: inspected=%d missing=%v", obs.CacheableResults, obs.CacheHintsMissing)
	}
}

func TestHTTPProbe_ModernServerWithoutTargetVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID *int `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "error": map[string]any{
			"code":    -32022,
			"message": "Unsupported protocol version",
			"data":    map[string]any{"supported": []any{"2027-03-01"}, "requested": "2026-07-28"},
		}})
	}))
	defer srv.Close()

	c, err := New(Options{URL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// A recognized modern error must not trigger the initialize fallback.
	_, _, err = c.Probe()
	if err == nil || !strings.Contains(err.Error(), "2027-03-01") || strings.Contains(err.Error(), "initialize") {
		t.Fatalf("err = %v, want an unsupported-version error naming the supported versions", err)
	}
}

func TestHTTPProbe_AuthChallenge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer realm=\"mcp\"")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c, err := New(Options{URL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	_, _, err = c.Probe()
	if err == nil {
		t.Fatalf("expected an error for a 401 endpoint")
	}
	// The transport records the challenge even though the probe cannot complete.
	if sig := c.tr.signals(); !sig.AuthChallenged || !strings.Contains(sig.AuthChallenge, "Bearer") {
		t.Errorf("auth challenge not recorded: %+v", sig)
	}
}

func TestHTTPProbe_CredentialsProvided(t *testing.T) {
	srv := newLegacyHTTPServer(t)
	defer srv.Close()

	c, err := New(Options{
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		Headers: map[string]string{"Authorization": "Bearer xyz"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	_, obs, err := c.Probe()
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !obs.AuthCredentialsProvided {
		t.Errorf("Authorization header sent but AuthCredentialsProvided=false")
	}
}

func TestParseSSE(t *testing.T) {
	body := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"
	payloads := parseSSE([]byte(body))
	if len(payloads) != 1 {
		t.Fatalf("parseSSE returned %d payloads", len(payloads))
	}
	var resp Response
	if err := json.Unmarshal(payloads[0], &resp); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if resp.ID == nil || *resp.ID != 1 {
		t.Errorf("bad id in SSE payload: %+v", resp)
	}
}
