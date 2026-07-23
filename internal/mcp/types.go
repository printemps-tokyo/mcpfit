package mcp

import "encoding/json"

// DefaultProtocolVersion is the revision mcpfit requests in initialize by
// default. Today's servers still speak the pre-stateless handshake, so we ask
// for a widely supported version and let the server negotiate down or up. The
// audited target spec lives in internal/audit.TargetSpecVersion; this is only
// what we send on the wire to complete the current handshake.
const DefaultProtocolVersion = "2025-06-18"

// clientName / clientVersion identify mcpfit in initialize.
const (
	clientName = "mcpfit"
)

// Method names used by the client.
const (
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
// cache-control fields (2026-07-28, SEP-2549) are captured when present.
type listResult struct {
	Tools      []Tool     `json:"tools"`
	Resources  []Resource `json:"resources"`
	Prompts    []Prompt   `json:"prompts"`
	NextCursor string     `json:"nextCursor"`
	TTLMs      *int64     `json:"ttlMs"`
	CacheScope string     `json:"cacheScope"`
}
