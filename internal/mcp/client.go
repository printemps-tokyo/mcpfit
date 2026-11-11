package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/printemps-tokyo/mcpfit/internal/audit"
)

// Options configures a probe run.
type Options struct {
	// Transport target: either a stdio command (+ args) or an HTTP URL.
	Command string
	Args    []string
	URL     string

	Headers         map[string]string
	Timeout         time.Duration
	ProtocolVersion string // version requested in the legacy initialize fallback; defaults applied by New
}

// Enumeration holds the raw listing results, used for the optional --list
// output. The audit consumes the compact Observation, not this.
type Enumeration struct {
	ServerInfo      Implementation
	ProtocolVersion string
	Tools           []Tool
	Resources       []Resource
	Prompts         []Prompt
}

// Client is a single-use MCP client bound to one transport.
type Client struct {
	tr     transport
	opts   Options
	nextID int

	// modern is true once the server has answered server/discover: requests
	// then carry the 2026-07-28 per-request metadata and no handshake is sent.
	modern bool

	capabilities map[string]any
	// cacheable counts the results inspected for caching hints; cacheMissing
	// names the methods whose result lacked one.
	cacheable    int
	cacheMissing []string
}

// New builds a Client for the configured transport. HTTP is selected when URL
// is set; otherwise stdio spawns Command.
func New(opts Options) (*Client, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	if opts.ProtocolVersion == "" {
		opts.ProtocolVersion = DefaultProtocolVersion
	}
	var tr transport
	switch {
	case opts.URL != "":
		tr = newHTTPTransport(opts.URL, opts.Headers, opts.Timeout)
	case opts.Command != "":
		tr = newStdioTransport(opts.Command, opts.Args, opts.Timeout)
	default:
		return nil, fmt.Errorf("no target: provide a server command or --url")
	}
	return &Client{tr: tr, opts: opts, nextID: 1}, nil
}

// transportName reports "http" or "stdio".
func (c *Client) transportName() string {
	if c.opts.URL != "" {
		return "http"
	}
	return "stdio"
}

// request builds the next request. For a modern server it attaches the
// per-request metadata 2026-07-28 requires (protocol version and client
// capabilities, plus the recommended client info) under params._meta.
func (c *Client) request(method string, params map[string]any) Request {
	id := c.nextID
	c.nextID++
	if !c.modern {
		if len(params) == 0 {
			return newRequest(id, method, nil)
		}
		return newRequest(id, method, params)
	}
	p := make(map[string]any, len(params)+1)
	for k, v := range params {
		p[k] = v
	}
	p["_meta"] = map[string]any{
		metaProtocolVersion: audit.TargetSpecVersion,
		metaClientInfo: map[string]any{
			"name":    clientName,
			"version": Version(),
		},
		metaClientCapabilities: map[string]any{},
	}
	req := newRequest(id, method, p)
	req.protocolVersion = audit.TargetSpecVersion
	return req
}

// call sends a request and returns its raw result, surfacing JSON-RPC errors.
func (c *Client) call(method string, params map[string]any) (json.RawMessage, error) {
	resp, err := c.tr.sendRequest(c.request(method, params))
	if err != nil {
		return nil, err
	}
	return unwrap(resp)
}

// callAllowError is like call but returns the RPCError instead of failing, so
// callers can distinguish "method not found" from a transport error.
func (c *Client) callAllowError(method string, params map[string]any) (json.RawMessage, *RPCError, error) {
	resp, err := c.tr.sendRequest(c.request(method, params))
	if err != nil {
		return nil, nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error, nil
	}
	return resp.Result, nil, nil
}

// discover probes server/discover with the audited revision, as the
// specification recommends for clients that support both eras. It returns the
// result when the server is modern, and nil when the server is legacy and the
// caller must fall back to initialize: any error that is not a recognized
// modern error, no usable result, or no answer within the timeout.
func (c *Client) discover() (*DiscoverResult, error) {
	c.modern = true
	resp, err := c.tr.sendRequest(c.request(methodDiscover, nil))
	if err != nil {
		c.modern = false
		return nil, nil
	}
	if resp.Error != nil {
		switch resp.Error.Code {
		case codeUnsupportedProtocolVersion:
			var data struct {
				Supported []string `json:"supported"`
			}
			_ = json.Unmarshal(resp.Error.Data, &data)
			return nil, fmt.Errorf("server speaks the per-request-metadata protocol but does not support %s (supported: %s); this build of mcpfit audits %s only",
				audit.TargetSpecVersion, strings.Join(data.Supported, ", "), audit.TargetSpecVersion)
		case codeHeaderMismatch, codeMissingClientCapability:
			return nil, fmt.Errorf("server/discover was rejected: %w", resp.Error)
		}
		c.modern = false
		return nil, nil
	}
	var res DiscoverResult
	if json.Unmarshal(resp.Result, &res) != nil || len(res.SupportedVersions) == 0 {
		c.modern = false
		return nil, nil
	}
	if len(res.Capabilities) > 0 {
		_ = json.Unmarshal(res.Capabilities, &c.capabilities)
	}
	c.noteCacheHints(methodDiscover, res.TTLMs, res.CacheScope)
	return &res, nil
}

// initialize runs the legacy handshake and records negotiated version and caps.
func (c *Client) initialize() (InitializeResult, error) {
	params := map[string]any{
		"protocolVersion": c.opts.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    clientName,
			"version": Version(),
		},
	}
	raw, err := c.call(methodInitialize, params)
	if err != nil {
		return InitializeResult{}, fmt.Errorf("initialize failed: %w", err)
	}
	var res InitializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return InitializeResult{}, fmt.Errorf("initialize returned malformed result: %w", err)
	}
	if len(res.Capabilities) > 0 {
		_ = json.Unmarshal(res.Capabilities, &c.capabilities)
	}
	// Complete the handshake (best effort; some servers require it before lists).
	_ = c.tr.sendNotification(newNotification(methodInitialized, nil))
	return res, nil
}

// listTools/Resources/Prompts collect every page, capturing cache hints.
func (c *Client) listTools() ([]Tool, error) {
	var all []Tool
	cursor := ""
	for {
		raw, rpcErr, err := c.callAllowError(methodToolsList, cursorParams(cursor))
		if err != nil {
			return nil, err
		}
		if rpcErr != nil {
			return all, nil // server has no tools capability; treat as empty
		}
		var lr listResult
		if err := json.Unmarshal(raw, &lr); err != nil {
			return nil, fmt.Errorf("tools/list returned malformed result: %w", err)
		}
		c.noteCacheHints(methodToolsList, lr.TTLMs, lr.CacheScope)
		all = append(all, lr.Tools...)
		if lr.NextCursor == "" {
			return all, nil
		}
		cursor = lr.NextCursor
	}
}

func (c *Client) listResources() ([]Resource, error) {
	var all []Resource
	cursor := ""
	for {
		raw, rpcErr, err := c.callAllowError(methodResourcesList, cursorParams(cursor))
		if err != nil {
			return nil, err
		}
		if rpcErr != nil {
			return all, nil
		}
		var lr listResult
		if err := json.Unmarshal(raw, &lr); err != nil {
			return nil, fmt.Errorf("resources/list returned malformed result: %w", err)
		}
		c.noteCacheHints(methodResourcesList, lr.TTLMs, lr.CacheScope)
		all = append(all, lr.Resources...)
		if lr.NextCursor == "" {
			return all, nil
		}
		cursor = lr.NextCursor
	}
}

func (c *Client) listPrompts() ([]Prompt, error) {
	var all []Prompt
	cursor := ""
	for {
		raw, rpcErr, err := c.callAllowError(methodPromptsList, cursorParams(cursor))
		if err != nil {
			return nil, err
		}
		if rpcErr != nil {
			return all, nil
		}
		var lr listResult
		if err := json.Unmarshal(raw, &lr); err != nil {
			return nil, fmt.Errorf("prompts/list returned malformed result: %w", err)
		}
		c.noteCacheHints(methodPromptsList, lr.TTLMs, lr.CacheScope)
		all = append(all, lr.Prompts...)
		if lr.NextCursor == "" {
			return all, nil
		}
		cursor = lr.NextCursor
	}
}

// noteCacheHints records one result that 2026-07-28 requires to carry both
// caching hints, and remembers the method when either is absent.
func (c *Client) noteCacheHints(method string, ttlMs *float64, cacheScope string) {
	c.cacheable++
	if ttlMs != nil && cacheScope != "" {
		return
	}
	for _, m := range c.cacheMissing {
		if m == method {
			return
		}
	}
	c.cacheMissing = append(c.cacheMissing, method)
}

func cursorParams(cursor string) map[string]any {
	if cursor == "" {
		return nil
	}
	return map[string]any{"cursor": cursor}
}

// Close shuts down the transport.
func (c *Client) Close() error { return c.tr.close() }

// Probe runs the full flow (server/discover, or the initialize fallback for a
// legacy server; enumeration; capability probes) and returns both the raw
// enumeration (for --list) and the compact audit Observation. The transport is
// not closed here; the caller owns its lifecycle.
func (c *Client) Probe() (Enumeration, audit.Observation, error) {
	if err := c.tr.start(); err != nil {
		return Enumeration{}, audit.Observation{}, err
	}

	var (
		era        string
		supported  []string
		version    string
		requested  string
		serverInfo Implementation
	)
	disc, err := c.discover()
	if err != nil {
		return Enumeration{}, audit.Observation{}, err
	}
	if disc != nil {
		era = audit.EraModern
		supported = disc.SupportedVersions
		requested = audit.TargetSpecVersion
		version = inUseVersion(supported)
		if raw, ok := disc.Meta[metaServerInfo]; ok {
			_ = json.Unmarshal(raw, &serverInfo)
		}
	} else {
		init, err := c.initialize()
		if err != nil {
			return Enumeration{}, audit.Observation{}, err
		}
		era = audit.EraLegacy
		requested = c.opts.ProtocolVersion
		version = init.ProtocolVersion
		serverInfo = init.ServerInfo
	}

	tools, err := c.listTools()
	if err != nil {
		return Enumeration{}, audit.Observation{}, fmt.Errorf("enumerating tools: %w", err)
	}
	resources, err := c.listResources()
	if err != nil {
		return Enumeration{}, audit.Observation{}, fmt.Errorf("enumerating resources: %w", err)
	}
	prompts, err := c.listPrompts()
	if err != nil {
		return Enumeration{}, audit.Observation{}, fmt.Errorf("enumerating prompts: %w", err)
	}

	tasksCap := c.hasTasksCapability()
	removed := c.probeRemovedTaskMethods(tasksCap)

	sig := c.tr.signals()

	enum := Enumeration{
		ServerInfo:      serverInfo,
		ProtocolVersion: version,
		Tools:           tools,
		Resources:       resources,
		Prompts:         prompts,
	}

	obs := audit.Observation{
		Target:                  c.targetLabel(),
		Transport:               c.transportName(),
		Era:                     era,
		SupportedVersions:       supported,
		RequestedVersion:        requested,
		ProtocolVersion:         version,
		ServerName:              serverInfo.Name,
		ServerVersion:           serverInfo.Version,
		Capabilities:            c.capabilities,
		Tools:                   toAuditTools(tools),
		ResourceCount:           len(resources),
		PromptCount:             len(prompts),
		SessionIDIssued:         sig.SessionIDIssued,
		SessionIDValue:          sig.SessionIDValue,
		TasksCapability:         tasksCap,
		TaskMethodsRemoved:      removed,
		CacheableResults:        c.cacheable,
		CacheHintsMissing:       c.cacheMissing,
		AuthCredentialsProvided: sig.AuthCredentialsProvided,
		AuthChallenged:          sig.AuthChallenged,
		AuthChallenge:           sig.AuthChallenge,
	}
	return enum, obs, nil
}

// inUseVersion picks the version to report for a modern server: the audited
// revision when the server lists it (that is what mcpfit sends), otherwise the
// newest listed one. MCP versions are dates, so string order is date order.
func inUseVersion(supported []string) string {
	newest := ""
	for _, v := range supported {
		if v == audit.TargetSpecVersion {
			return v
		}
		if v > newest {
			newest = v
		}
	}
	return newest
}

func (c *Client) targetLabel() string {
	if c.opts.URL != "" {
		return c.opts.URL
	}
	label := c.opts.Command
	for _, a := range c.opts.Args {
		label += " " + a
	}
	return label
}

// hasTasksCapability inspects the capability map for a tasks-related capability,
// including experimental and reverse-DNS extension entries.
func (c *Client) hasTasksCapability() bool {
	if c.capabilities == nil {
		return false
	}
	if _, ok := c.capabilities["tasks"]; ok {
		return true
	}
	if exp, ok := c.capabilities["experimental"].(map[string]any); ok {
		if _, ok := exp["tasks"]; ok {
			return true
		}
	}
	if ext, ok := c.capabilities["extensions"].(map[string]any); ok {
		for key := range ext {
			if containsTasks(key) {
				return true
			}
		}
	}
	return false
}

// probeRemovedTaskMethods checks (observably) whether tasks/list, which the
// 2026-07-28 Tasks extension removed, still responds. It only runs when a
// tasks capability is declared, and treats any error as good (removed). The
// blocking tasks/result is not probed: it needs a real task id.
func (c *Client) probeRemovedTaskMethods(tasksCap bool) []string {
	if !tasksCap {
		return nil
	}
	var removed []string
	if _, rpcErr, err := c.callAllowError("tasks/list", nil); err == nil && rpcErr == nil {
		removed = append(removed, "tasks/list")
	}
	return removed
}

func containsTasks(s string) bool {
	for i := 0; i+5 <= len(s); i++ {
		if s[i:i+5] == "tasks" || s[i:i+5] == "Tasks" {
			return true
		}
	}
	return false
}

func toAuditTools(tools []Tool) []audit.Tool {
	out := make([]audit.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, audit.Tool{
			Name:         t.Name,
			InputSchema:  t.InputSchema,
			OutputSchema: t.OutputSchema,
			HasInput:     t.InputSchema != nil,
			HasOutput:    t.OutputSchema != nil,
		})
	}
	return out
}
