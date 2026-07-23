// Package mcp is mcpfit's Model Context Protocol client: JSON-RPC 2.0 over
// either stdio (a spawned subprocess) or the Streamable HTTP transport. It
// performs today's initialize handshake, enumerates tools/resources/prompts,
// and records the low-level signals (session header, cache hints, auth
// challenge) that the pure internal/audit package grades.
//
// The transports mirror the org's mcppeek reference implementation but are
// re-implemented in Go with the standard library only.
package mcp

import (
	"encoding/json"
	"fmt"
)

// Request is a JSON-RPC 2.0 request object.
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Notification is a JSON-RPC 2.0 notification (a request without an id).
type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response object. Result is kept raw so callers
// decode into the exact shape they expect.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// newRequest builds a request with the standard version tag.
func newRequest(id int, method string, params any) Request {
	return Request{JSONRPC: "2.0", ID: id, Method: method, Params: params}
}

// newNotification builds a notification with the standard version tag.
func newNotification(method string, params any) Notification {
	return Notification{JSONRPC: "2.0", Method: method, Params: params}
}

// unwrap returns the raw result of a response or its error.
func unwrap(r *Response) (json.RawMessage, error) {
	if r.Error != nil {
		return nil, r.Error
	}
	return r.Result, nil
}
