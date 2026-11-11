package mcp

import "encoding/json"

// DefaultProtocolVersion is the revision mcpfit requests in initialize when it
// falls back to the legacy handshake, that is, when the server does not answer
// server/discover. It is a widely supported legacy version; the server
// negotiates down or up from it. The audited revision, which is also what the
// server/discover probe and modern requests carry, lives in
// internal/audit.TargetSpecVersion.
const DefaultProtocolVersion = "2025-06-18"

// clientName / clientVersion identify mcpfit in initialize.
const (
	clientName = "mcpfit"
)

// Reserved _meta keys that carry the per-request protocol fields in
// 2026-07-28, and the error codes that revision defines. Any of them in
// answer to server/discover identifies a modern server.
const (
	metaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaServerInfo         = "io.modelcontextprotocol/serverInfo"

	codeHeaderMismatch             = -32020
	codeMissingClientCapability    = -32021
	codeUnsupportedProtocolVersion = -32022
)

// Method names used by the client.
const (
	methodDiscover              = "server/discover"
	methodInitialize            = "initialize"
	methodInitialized           = "notifications/initialized"
	methodToolsList             = "tools/list"
	methodResourcesList         = "resources/list"
	methodResourceTemplatesList = "resources/templates/list"
	methodPromptsList           = "prompts/list"
)

// Implementation is the name/version pair for a client or server.
type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// InitializeResult is the server's answer to initialize. Capabilities is kept
// raw and also decoded into a generic map for capability probing.
type InitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ServerInfo      Implementation  `json:"serverInfo"`
	Instructions    string          `json:"instructions,omitempty"`
}

// DiscoverResult is the server's answer to server/discover (2026-07-28). The
// server identity travels in _meta; the caching hints are required on it.
type DiscoverResult struct {
	SupportedVersions []string                   `json:"supportedVersions"`
	Capabilities      json.RawMessage            `json:"capabilities"`
	Meta              map[string]json.RawMessage `json:"_meta"`
	Instructions      string                     `json:"instructions,omitempty"`
	TTLMs             *float64                   `json:"ttlMs"`
	CacheScope        string                     `json:"cacheScope"`
}

// Tool is a decoded tool definition. Schemas stay as generic decoded JSON so
// the audit inspector can examine any keyword structurally.
type Tool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
}

// Resource is a decoded resource entry (subset).
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// Prompt is a decoded prompt entry (subset).
type Prompt struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// listResult is the shape shared by the paginated list methods. Only one of the
// item slices is populated per call; nextCursor drives pagination and the
// caching hints (required by 2026-07-28, SEP-2549) are captured when present.
type listResult struct {
	Tools      []Tool     `json:"tools"`
	Resources  []Resource `json:"resources"`
	Prompts    []Prompt   `json:"prompts"`
	NextCursor string     `json:"nextCursor"`
	TTLMs      *float64   `json:"ttlMs"`
	CacheScope string     `json:"cacheScope"`
}
