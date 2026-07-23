package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	if obs.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion = %q", obs.ProtocolVersion)
	}
	if len(obs.Tools) != 1 || obs.Tools[0].Name != "t" {
		t.Errorf("tools = %+v", obs.Tools)
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
