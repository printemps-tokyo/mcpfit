package mcp

import (
	"encoding/json"
	"fmt"
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
	ProtocolVersion string // requested version in initialize; defaults applied by New
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

	capabilities map[string]any
	cacheHints   bool
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

// call sends a request and returns its raw result, surfacing JSON-RPC errors.
func (c *Client) call(method string, params any) (json.RawMessage, error) {
	id := c.nextID
	c.nextID++
	resp, err := c.tr.sendRequest(newRequest(id, method, params))
	if err != nil {
		return nil, err
	}
	return unwrap(resp)
}

// callAllowError is like call but returns the RPCError instead of failing, so
// callers can distinguish "method not found" from a transport error.
func (c *Client) callAllowError(method string, params any) (json.RawMessage, *RPCError, error) {
	id := c.nextID
	c.nextID++
	resp, err := c.tr.sendRequest(newRequest(id, method, params))
	if err != nil {
		return nil, nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error, nil
	}
	return resp.Result, nil, nil
}

// initialize runs today's handshake and records negotiated version and caps.
func (c *Client) initialize() (InitializeResult, error) {
	if err := c.tr.start(); err != nil {
		return InitializeResult{}, err
	}
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
		c.noteCacheHints(lr)
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
		c.noteCacheHints(lr)
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
		c.noteCacheHints(lr)
		all = append(all, lr.Prompts...)
		if lr.NextCursor == "" {
			return all, nil
		}
		cursor = lr.NextCursor
	}
}

func (c *Client) noteCacheHints(lr listResult) {
	if lr.TTLMs != nil || lr.CacheScope != "" {
		c.cacheHints = true
	}
}

func cursorParams(cursor string) any {
	if cursor == "" {
		return nil
	}
	return map[string]any{"cursor": cursor}
}

// Close shuts down the transport.
func (c *Client) Close() error { return c.tr.close() }

// Probe runs the full flow (initialize, enumerate, capability probes) and
// returns both the raw enumeration (for --list) and the compact audit
// Observation. The transport is not closed here; the caller owns its lifecycle.
func (c *Client) Probe() (Enumeration, audit.Observation, error) {
	init, err := c.initialize()
	if err != nil {
		return Enumeration{}, audit.Observation{}, err
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
	blocking := c.probeBlockingTaskMethods(tasksCap)

	sig := c.tr.signals()

	enum := Enumeration{
		ServerInfo:      init.ServerInfo,
		ProtocolVersion: init.ProtocolVersion,
		Tools:           tools,
		Resources:       resources,
		Prompts:         prompts,
	}

	obs := audit.Observation{
		Target:                  c.targetLabel(),
		Transport:               c.transportName(),
		RequestedVersion:        c.opts.ProtocolVersion,
		ProtocolVersion:         init.ProtocolVersion,
		ServerName:              init.ServerInfo.Name,
		ServerVersion:           init.ServerInfo.Version,
		Capabilities:            c.capabilities,
		Tools:                   toAuditTools(tools),
		ResourceCount:           len(resources),
		PromptCount:             len(prompts),
		SessionIDIssued:         sig.SessionIDIssued,
		SessionIDValue:          sig.SessionIDValue,
		TasksCapability:         tasksCap,
		TaskMethodsBlocking:     blocking,
		CacheHints:              c.cacheHints,
		AuthCredentialsProvided: sig.AuthCredentialsProvided,
		AuthChallenged:          sig.AuthChallenged,
		AuthChallenge:           sig.AuthChallenge,
	}
	return enum, obs, nil
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

// probeBlockingTaskMethods checks (observably) whether the removed blocking
// method tasks/list still responds. It only runs when a tasks capability is
// declared, and treats a "method not found" error as good (removed).
func (c *Client) probeBlockingTaskMethods(tasksCap bool) []string {
	if !tasksCap {
		return nil
	}
	var blocking []string
	if _, rpcErr, err := c.callAllowError("tasks/list", nil); err == nil && rpcErr == nil {
		blocking = append(blocking, "tasks/list")
	}
	return blocking
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
