package audit

// Observation is a plain-struct snapshot of a target MCP server, gathered by a
// transport in internal/mcp and consumed by Run. It carries only what the
// checks need, decoded from the wire, so the audit logic never touches I/O.
type Observation struct {
	// Target is a human label for the server (command line or URL).
	Target string
	// Transport is "stdio" or "http".
	Transport string

	// RequestedVersion is the protocolVersion mcpfit asked for in initialize.
	RequestedVersion string
	// ProtocolVersion is the version the server negotiated in its initialize
	// result. Empty if the handshake did not yield one.
	ProtocolVersion string

	ServerName    string
	ServerVersion string

	// Capabilities is the server's declared capability map from initialize.
	Capabilities map[string]any

	// Tools holds every enumerated tool (all pages).
	Tools []Tool
	// ResourceCount and PromptCount are enumeration tallies for context.
	ResourceCount int
	PromptCount   int

	// SessionIDIssued is true when a Streamable HTTP server returned an
	// Mcp-Session-Id header. SessionIDValue is that value (for the message).
	SessionIDIssued bool
	SessionIDValue  string

	// TasksCapability is true when the server declares a tasks-related
	// capability (core, experimental, or extensions map).
	TasksCapability bool
	// TaskMethodsBlocking lists any observed legacy blocking task methods
	// (tasks/result, tasks/list) that the 2026-07-28 Tasks extension removes.
	TaskMethodsBlocking []string

	// CacheHints is true when any list/read result carried the 2026-07-28
	// cache-control fields (ttlMs / cacheScope).
	CacheHints bool

	// HTTP authorization posture.
	// AuthCredentialsProvided is true when the caller supplied an Authorization
	// header (so mcpfit cannot conclude whether auth is enforced).
	AuthCredentialsProvided bool
	// AuthChallenged is true when the endpoint answered an unauthenticated
	// request with 401/403 or a WWW-Authenticate challenge.
	AuthChallenged bool
	// AuthChallenge is the raw WWW-Authenticate value, when present.
	AuthChallenge string
}

// Tool is a decoded tool definition. Schemas are kept as generic decoded JSON
// (map[string]any) so the inspector can examine any keyword structurally.
type Tool struct {
	Name         string
	InputSchema  map[string]any
	OutputSchema map[string]any
	HasInput     bool
	HasOutput    bool
}
