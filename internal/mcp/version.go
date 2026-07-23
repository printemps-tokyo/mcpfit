package mcp

// clientVersion is the version mcpfit reports as clientInfo.version in
// initialize. main sets it from the build-time version via SetVersion.
var clientVersion = "dev"

// SetVersion overrides the reported client version (called from main).
func SetVersion(v string) {
	if v != "" {
		clientVersion = v
	}
}

// Version returns the reported client version.
func Version() string { return clientVersion }
