package mcp

// transport is the minimal contract the client needs from stdio or HTTP: send
// a request and get its response, send a fire-and-forget notification, and shut
// down cleanly. Every read is bounded by the transport's own timeout so the
// client can never hang.
type transport interface {
	start() error
	sendRequest(req Request) (*Response, error)
	sendNotification(n Notification) error
	close() error
	// signals exposes transport-level observations the audit needs but that are
	// not part of JSON-RPC (e.g. an HTTP session header or auth challenge).
	signals() TransportSignals
}

// TransportSignals carries out-of-band observations gathered while talking to
// the server.
type TransportSignals struct {
	// SessionIDIssued / SessionIDValue: an Mcp-Session-Id header was returned
	// (Streamable HTTP only).
	SessionIDIssued bool
	SessionIDValue  string
	// AuthChallenged / AuthChallenge: the endpoint returned a 401/403 or a
	// WWW-Authenticate challenge (Streamable HTTP only).
	AuthChallenged bool
	AuthChallenge  string
	// AuthCredentialsProvided: the caller supplied an Authorization header.
	AuthCredentialsProvided bool
}
