package audit

import (
	"fmt"
	"sort"
	"strings"
)

// This file holds the six mcpfit checks. Each returns zero or more graded
// findings. Grading policy: FAIL only for concrete, high-confidence breakages
// against the published 2026-07-28 text; WARN for requirements the server has
// yet to meet and for opinionated lint; INFO for neutral context.

// Check IDs (stable strings used in output and tests).
const (
	CheckProtocolVersion = "protocol-version"
	CheckSessionReliance = "session-reliance"
	CheckToolSchema      = "tool-schema-2020-12"
	CheckDeprecatedTasks = "deprecated-tasks"
	CheckCacheHints      = "cache-hints"
	CheckAuthPosture     = "auth-posture"
)

// checkProtocolVersion reports which protocol era and version the server
// speaks. 2026-07-28 has no initialize handshake: a server implements it by
// answering server/discover (which servers MUST implement) and accepting the
// version in per-request metadata. A server that only answers initialize is a
// legacy server; that is INFO, not a failure, because the remaining checks
// name what has to change.
func checkProtocolVersion(obs Observation) []Finding {
	if obs.Era == EraModern {
		listed := strings.Join(obs.SupportedVersions, ", ")
		for _, v := range obs.SupportedVersions {
			if v == TargetSpecVersion {
				return []Finding{{
					Check:    CheckProtocolVersion,
					Severity: Pass,
					Message:  fmt.Sprintf("server answers server/discover and lists %s among its supported versions (%s)", TargetSpecVersion, listed),
				}}
			}
		}
		return []Finding{{
			Check:    CheckProtocolVersion,
			Severity: Info,
			Message:  fmt.Sprintf("server answers server/discover but does not list %s (supported: %s)", TargetSpecVersion, listed),
			Fix:      fmt.Sprintf("if the server targets a newer revision, upgrade mcpfit; otherwise add %s to supportedVersions", TargetSpecVersion),
		}}
	}

	if obs.ProtocolVersion == "" {
		return []Finding{{
			Check:    CheckProtocolVersion,
			Severity: Warn,
			Message:  "server did not answer server/discover and did not report a protocolVersion in its initialize result",
			Fix:      fmt.Sprintf("implement server/discover and per-request metadata as specified by %s", TargetSpecVersion),
		}}
	}
	cmp := compareVersions(obs.ProtocolVersion, TargetSpecVersion)
	switch {
	case cmp == 0:
		return []Finding{{
			Check:    CheckProtocolVersion,
			Severity: Warn,
			Message:  fmt.Sprintf("server returned protocolVersion %s from initialize but did not answer server/discover; that revision has no initialize handshake", obs.ProtocolVersion),
			Fix:      "implement server/discover and read the protocol version from each request's _meta instead of negotiating it in initialize",
		}}
	case cmp < 0:
		return []Finding{{
			Check:    CheckProtocolVersion,
			Severity: Info,
			Message:  fmt.Sprintf("server did not answer server/discover and negotiated protocolVersion %s through the legacy initialize handshake; the current revision is %s", obs.ProtocolVersion, TargetSpecVersion),
			Fix:      fmt.Sprintf("to support %s, implement server/discover and per-request metadata; the remaining checks show what to change first", TargetSpecVersion),
		}}
	default:
		return []Finding{{
			Check:    CheckProtocolVersion,
			Severity: Info,
			Message:  fmt.Sprintf("server negotiated protocolVersion %s, newer than the target %s this build audits", obs.ProtocolVersion, TargetSpecVersion),
			Fix:      "upgrade mcpfit to audit against the newer revision",
		}}
	}
}

// checkSessionReliance flags dependence on protocol-level session state, which
// 2026-07-28 removed from the Streamable HTTP transport (SEP-2567).
func checkSessionReliance(obs Observation) []Finding {
	if obs.Transport == "stdio" {
		return []Finding{{
			Check:    CheckSessionReliance,
			Severity: Info,
			Message:  "stdio transport: cross-request session state cannot be fully probed here",
			Fix:      "ensure any request can be served independently; 2026-07-28 has no protocol-level session, list results must not vary per connection, and cross-call state belongs in explicit handles passed as tool arguments",
		}}
	}
	if obs.SessionIDIssued {
		msg := "server issued an Mcp-Session-Id header"
		if obs.SessionIDValue != "" {
			msg = fmt.Sprintf("server issued an Mcp-Session-Id header (%s)", truncate(obs.SessionIDValue, 24))
		}
		if obs.Era == EraModern {
			msg += fmt.Sprintf(" in response to a %s request", TargetSpecVersion)
		} else {
			msg += " during the legacy initialize handshake and does not answer server/discover"
		}
		return []Finding{{
			Check:    CheckSessionReliance,
			Severity: Fail,
			Message:  msg + "; 2026-07-28 removed protocol-level sessions and the Mcp-Session-Id header (SEP-2567)",
			Fix:      "stop minting/requiring Mcp-Session-Id; make every request independently routable and carry any needed state in explicit, server-minted handles passed as ordinary tool arguments",
		}}
	}
	return []Finding{{
		Check:    CheckSessionReliance,
		Severity: Pass,
		Message:  "no Mcp-Session-Id header observed; server does not appear to rely on protocol-level sessions",
	}}
}

// checkToolSchemas runs the structural schema inspector over every tool's
// inputSchema (and outputSchema when present), promoting inspector results to
// graded findings scoped by tool name.
func checkToolSchemas(obs Observation) []Finding {
	if len(obs.Tools) == 0 {
		return []Finding{{
			Check:    CheckToolSchema,
			Severity: Info,
			Message:  "server exposes no tools to inspect",
		}}
	}

	var findings []Finding
	clean := true
	for _, tool := range obs.Tools {
		if tool.HasInput {
			for _, sf := range inspectSchema(tool.InputSchema, true) {
				clean = false
				findings = append(findings, toFinding(tool.Name, "inputSchema", sf))
			}
		} else {
			findings = append(findings, Finding{
				Check:    CheckToolSchema,
				Severity: Warn,
				Target:   tool.Name,
				Message:  "tool has no inputSchema",
				Fix:      "declare an inputSchema; it is a required field and must be a JSON Schema object with \"type\": \"object\"",
			})
			clean = false
		}
		if tool.HasOutput {
			for _, sf := range inspectSchema(tool.OutputSchema, false) {
				clean = false
				findings = append(findings, toFinding(tool.Name, "outputSchema", sf))
			}
		}
	}

	if clean {
		return []Finding{{
			Check:    CheckToolSchema,
			Severity: Pass,
			Message:  fmt.Sprintf("all %d tool schema(s) pass the structural 2020-12 checks", len(obs.Tools)),
		}}
	}
	return findings
}

func toFinding(tool, role string, sf schemaFinding) Finding {
	return Finding{
		Check:    CheckToolSchema,
		Severity: sf.severity,
		Target:   fmt.Sprintf("%s (%s %s)", tool, role, sf.path),
		Message:  sf.message,
		Fix:      sf.fix,
	}
}

// checkDeprecatedTasks reports on task support. 2026-07-28 moved the
// experimental tasks feature out of the core protocol into the
// io.modelcontextprotocol/tasks extension (SEP-2663), which replaces the
// blocking tasks/result with tasks/get polling, adds tasks/update, and removes
// tasks/list. It only asserts what is observable: tasks/list is the one method
// that can be probed without creating a task.
func checkDeprecatedTasks(obs Observation) []Finding {
	if len(obs.TaskMethodsRemoved) > 0 {
		methods := append([]string(nil), obs.TaskMethodsRemoved...)
		sort.Strings(methods)
		return []Finding{{
			Check:    CheckDeprecatedTasks,
			Severity: Warn,
			Message:  fmt.Sprintf("server still answers task method(s) removed in 2026-07-28: %s", strings.Join(methods, ", ")),
			Fix:      "move to the io.modelcontextprotocol/tasks extension: return a task handle and let clients poll tasks/get (and send input with tasks/update); tasks/list is removed and the blocking tasks/result is replaced by polling",
		}}
	}
	if obs.TasksCapability {
		return []Finding{{
			Check:    CheckDeprecatedTasks,
			Severity: Info,
			Message:  "server declares a tasks-related capability; the removed tasks/list method did not respond",
			Fix:      "confirm tasks are advertised as the io.modelcontextprotocol/tasks extension and use tasks/get polling (plus tasks/update / tasks/cancel), not the blocking tasks/result",
		}}
	}
	return []Finding{{
		Check:    CheckDeprecatedTasks,
		Severity: Pass,
		Message:  "no tasks capability or removed task methods observed",
	}}
}

// checkCacheHints reports whether cacheable results carry the caching hints.
// 2026-07-28 makes ttlMs and cacheScope required on complete results of
// server/discover, tools/list, prompts/list, resources/list,
// resources/templates/list and resources/read (SEP-2549). A missing hint is a
// WARN rather than a FAIL: clients are told to treat an absent ttlMs as 0, so
// nothing breaks, but the server does not meet the requirement.
func checkCacheHints(obs Observation) []Finding {
	if obs.CacheableResults == 0 {
		return []Finding{{
			Check:    CheckCacheHints,
			Severity: Info,
			Message:  "no cacheable results were returned, so caching hints could not be inspected",
		}}
	}
	if len(obs.CacheHintsMissing) > 0 {
		return []Finding{{
			Check:    CheckCacheHints,
			Severity: Warn,
			Message:  fmt.Sprintf("result(s) without both ttlMs and cacheScope: %s", strings.Join(obs.CacheHintsMissing, ", ")),
			Fix:      "2026-07-28 requires ttlMs (>= 0) and cacheScope (\"public\" or \"private\") on results of server/discover, tools/list, prompts/list, resources/list, resources/templates/list and resources/read (SEP-2549)",
		}}
	}
	return []Finding{{
		Check:    CheckCacheHints,
		Severity: Pass,
		Message:  fmt.Sprintf("all %d inspected result(s) carry ttlMs and cacheScope (resources/templates/list and resources/read are not probed)", obs.CacheableResults),
	}}
}

// checkAuthPosture reports on authorization enforcement for HTTP endpoints.
// Authorization is optional in MCP, so an open endpoint is not a specification
// violation; the WARN is mcpfit's own opinionated posture lint.
func checkAuthPosture(obs Observation) []Finding {
	if obs.Transport != "http" {
		return nil
	}
	switch {
	case obs.AuthChallenged:
		msg := "endpoint enforces authorization (returned a 401/403 or WWW-Authenticate challenge)"
		if obs.AuthChallenge != "" {
			msg = fmt.Sprintf("endpoint enforces authorization (WWW-Authenticate: %s)", truncate(obs.AuthChallenge, 60))
		}
		return []Finding{{
			Check:    CheckAuthPosture,
			Severity: Info,
			Message:  msg,
			Fix:      "for the authorization flow, note the 2026-07-28 changes: authorization servers should send iss and clients must validate it (RFC 9207 / SEP-2468); Dynamic Client Registration is deprecated in favor of Client ID Metadata Documents, and clients that still use it must send application_type (SEP-837)",
		}}
	case obs.AuthCredentialsProvided:
		return []Finding{{
			Check:    CheckAuthPosture,
			Severity: Info,
			Message:  "authorization was not probed because credentials were supplied via --header",
			Fix:      "to check enforcement, run once without an Authorization header and confirm the endpoint returns 401",
		}}
	default:
		return []Finding{{
			Check:    CheckAuthPosture,
			Severity: Warn,
			Message:  "endpoint served MCP requests without requiring authorization",
			Fix:      "authorization is optional in MCP, so this is fine for a deliberately public endpoint; if it is not meant to be public, enforce authorization as described in the 2026-07-28 authorization section",
		}}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
