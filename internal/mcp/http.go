package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpTransport speaks the Streamable HTTP transport: each client message is an
// HTTP POST to the MCP endpoint. The server answers with either one
// application/json object or a text/event-stream (SSE) carrying the response.
// Requests in the 2026-07-28 form also carry the MCP-Protocol-Version and
// Mcp-Method headers that revision requires. All reads are bounded by the
// client timeout.
type httpTransport struct {
	url     string
	headers map[string]string
	client  *http.Client

	negotiatedVersion string
	sig               TransportSignals
}

func newHTTPTransport(url string, headers map[string]string, timeout time.Duration) *httpTransport {
	authProvided := false
	for k := range headers {
		if strings.EqualFold(k, "authorization") {
			authProvided = true
		}
	}
	return &httpTransport{
		url:     url,
		headers: headers,
		client:  &http.Client{Timeout: timeout},
		sig:     TransportSignals{AuthCredentialsProvided: authProvided},
	}
}

func (t *httpTransport) start() error { return nil }

func (t *httpTransport) sendRequest(req Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var extra map[string]string
	if req.protocolVersion != "" {
		extra = map[string]string{
			"MCP-Protocol-Version": req.protocolVersion,
			"Mcp-Method":           req.Method,
		}
	}
	status, ctype, respBody, err := t.post(body, extra)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		// Auth challenge: record it; surface a clear error so the run can still
		// report an auth-posture finding for HTTP targets.
		return nil, fmt.Errorf("http: server returned %d (authorization required) for %q", status, req.Method)
	}
	resp := matchResponse(ctype, respBody, req.ID)
	if status >= 400 {
		// A JSON-RPC error carried by a 4xx reply is a protocol-level answer
		// (2026-07-28 uses 400 for an unsupported version and 404 for an
		// unknown method), so hand it to the caller as an RPC error.
		if resp != nil && resp.Error != nil {
			return resp, nil
		}
		return nil, fmt.Errorf("http: server returned HTTP %d for %q%s", status, req.Method, snippet(respBody))
	}
	if resp == nil {
		return nil, fmt.Errorf("http: no JSON-RPC response for %q in the reply", req.Method)
	}
	if req.Method == methodInitialize && resp.Result != nil {
		var ir InitializeResult
		if json.Unmarshal(resp.Result, &ir) == nil && ir.ProtocolVersion != "" {
			t.negotiatedVersion = ir.ProtocolVersion
		}
	}
	return resp, nil
}

// matchResponse returns the JSON-RPC response with the given id from an HTTP
// body, or nil when there is none.
func matchResponse(contentType string, body []byte, id int) *Response {
	payloads, err := extractPayloads(contentType, body)
	if err != nil {
		return nil
	}
	for _, p := range payloads {
		var resp Response
		if json.Unmarshal(p, &resp) != nil {
			continue
		}
		if resp.ID != nil && *resp.ID == id {
			return &resp
		}
	}
	return nil
}

func (t *httpTransport) sendNotification(n Notification) error {
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}
	status, _, respBody, err := t.post(body, nil)
	if err != nil {
		return err
	}
	if status >= 400 && status != http.StatusUnauthorized && status != http.StatusForbidden {
		return fmt.Errorf("http: server returned HTTP %d for %q%s", status, n.Method, snippet(respBody))
	}
	return nil
}

// post issues the POST and records session/auth signals from the response.
// extra holds per-request headers; they take precedence over the negotiated
// legacy protocol version header.
func (t *httpTransport) post(body []byte, extra map[string]string) (status int, contentType string, respBody []byte, err error) {
	httpReq, err := http.NewRequest(http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return 0, "", nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}
	if t.sig.SessionIDValue != "" {
		httpReq.Header.Set("Mcp-Session-Id", t.sig.SessionIDValue)
	}
	if t.negotiatedVersion != "" {
		httpReq.Header.Set("MCP-Protocol-Version", t.negotiatedVersion)
	}
	for k, v := range extra {
		httpReq.Header.Set(k, v)
	}

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return 0, "", nil, fmt.Errorf("http: request failed: %w", err)
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sig.SessionIDIssued = true
		t.sig.SessionIDValue = sid
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		t.sig.AuthChallenged = true
		if ch := resp.Header.Get("WWW-Authenticate"); ch != "" {
			t.sig.AuthChallenge = ch
		}
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, resp.Header.Get("Content-Type"), nil, fmt.Errorf("http: reading response failed: %w", err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), data, nil
}

func (t *httpTransport) close() error { return nil }

func (t *httpTransport) signals() TransportSignals { return t.sig }

// extractPayloads returns the JSON-RPC payload(s) from an HTTP body: a single
// object for application/json, or the data of each SSE event for
// text/event-stream.
func extractPayloads(contentType string, body []byte) ([]json.RawMessage, error) {
	if strings.Contains(contentType, "text/event-stream") {
		return parseSSE(body), nil
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}
	return []json.RawMessage{trimmed}, nil
}

// parseSSE extracts the concatenated data of each event in an SSE stream. Only
// the "data:" lines matter for JSON-RPC payloads.
func parseSSE(body []byte) []json.RawMessage {
	var out []json.RawMessage
	var data []string
	flush := func() {
		if len(data) == 0 {
			return
		}
		joined := strings.TrimSpace(strings.Join(data, "\n"))
		data = nil
		if joined != "" {
			out = append(out, json.RawMessage(joined))
		}
	}
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(line[len("data:"):]))
		}
	}
	flush()
	return out
}

func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return ""
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return ": " + s
}
