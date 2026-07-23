package audit

import (
	"fmt"
	"sort"
	"strings"
)

// This file holds the six mcpfit checks. Each returns zero or more graded
// findings. The grading policy follows the RC caveat: FAIL only for concrete,
// high-confidence breakages under 2026-07-28; WARN/INFO for likely or
// forward-looking issues.

// Check IDs (stable strings used in output and tests).
const (
	CheckProtocolVersion = "protocol-version"
	CheckSessionReliance = "session-reliance"
	CheckToolSchema      = "tool-schema-2020-12"
	CheckDeprecatedTasks = "deprecated-tasks"
	CheckCacheHints      = "cache-hints"
	CheckAuthPosture     = "auth-posture"
)

// checkProtocolVersion reports the negotiated protocolVersion. It is INFO (not
// a failure) when the server does not yet advertise 2026-07-28, since older
// revisions such as 2025-06-18 are expected today.
func checkProtocolVersion(obs Observation) []Finding {
	if obs.ProtocolVersion == "" {
		return []Finding{{
			Check:    CheckProtocolVersion,
			Severity: Warn,
			Message:  "server did not report a protocolVersion in its initialize result",
			Fix:      "return a protocolVersion from initialize so clients can negotiate correctly",
		}}
	}
	cmp := compareVersions(obs.ProtocolVersion, TargetSpecVersion)
	switch {
	case cmp == 0:
		return []Finding{{
			Check:    CheckProtocolVersion,
			Severity: Pass,
			Message:  fmt.Sprintf("server negotiated protocolVersion %s (matches the audited spec)", obs.ProtocolVersion),
		}}
	case cmp < 0:
		return []Finding{{
			Check:    CheckProtocolVersion,
			Severity: Info,
			Message:  fmt.Sprintf("server negotiated protocolVersion %s, older than the target %s (expected for servers not yet migrated)", obs.ProtocolVersion, TargetSpecVersion),
			Fix:      fmt.Sprintf("plan to support protocolVersion %s; the remaining checks show what to change first", TargetSpecVersion),
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
// SEP-2567 removes from the Streamable HTTP transport in 2026-07-28.
func checkSessionReliance(obs Observation) []Finding {
	if obs.Transport == "stdio" {
		return []Finding{{
			Check:    CheckSessionReliance,
			Severity: Info,
			Message:  "stdio transport: cross-request session state cannot be fully probed here",
			Fix:      "ensure any request can be served independently; the stateless spec forbids relying on per-connection session state",
		}}
	}
	if obs.SessionIDIssued {
		msg := "server issued an Mcp-Session-Id header"
		if obs.SessionIDValue != "" {
			msg = fmt.Sprintf("server issued an Mcp-Session-Id header (%s)", truncate(obs.SessionIDValue, 24))
		}
		return []Finding{{
			Check:    CheckSessionReliance,
			Severity: Fail,
			Message:  msg + "; the stateless spec (SEP-2567) removes protocol-level sessions and the Mcp-Session-Id header",
			Fix:      "stop minting/requiring Mcp-Session-Id; make every request independently routable and carry any needed state in explicit tool arguments or handles",
		}}
	}
	return []Finding{{
		Check:    CheckSessionReliance,
		Severity: Pass,
		Message:  "no Mcp-Session-Id header observed; server does not appear to rely on protocol-level sessions",
	}}
}

// checkToolSchemas runs the structural 2020-12 inspector over every tool's
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
				Fix:      "declare an inputSchema (\"type\": \"object\") so the tool's arguments can be validated under 2020-12",
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

// checkDeprecatedTasks warns when a server declares tasks support, since the
// 2026-07-28 Tasks extension (SEP-2663) replaces blocking tasks/result with
// tasks/get polling and removes tasks/list. It only asserts what is observable.
func checkDeprecatedTasks(obs Observation) []Finding {
	if len(obs.TaskMethodsBlocking) > 0 {
		methods := append([]string(nil), obs.TaskMethodsBlocking...)
		sort.Strings(methods)
		return []Finding{{
			Check:    CheckDeprecatedTasks,
			Severity: Warn,
			Message:  fmt.Sprintf("server exposes legacy blocking task method(s): %s", strings.Join(methods, ", ")),
			Fix:      "adopt the 2026-07-28 Tasks extension: return a task handle from tools/call and let clients poll tasks/get (and tasks/update); tasks/result and tasks/list are removed",
		}}
	}
	if obs.TasksCapability {
		return []Finding{{
			Check:    CheckDeprecatedTasks,
			Severity: Info,
			Message:  "server declares a tasks-related capability; blocking task methods were not observed",
			Fix:      "confirm the implementation uses tasks/get polling (plus tasks/update / tasks/cancel), not the removed tasks/result or tasks/list",
		}}
	}
	return []Finding{{
		Check:    CheckDeprecatedTasks,
		Severity: Pass,
		Message:  "no tasks capability or legacy task methods observed",
	}}
}

// checkCacheHints reports whether responses carry the new cache-control fields
// (ttlMs / cacheScope). This is an opportunity, never a failure.
func checkCacheHints(obs Observation) []Finding {
	if obs.CacheHints {
		return []Finding{{
			Check:    CheckCacheHints,
			Severity: Info,
			Message:  "responses carry cache-control hints (ttlMs / cacheScope)",
		}}
	}
	return []Finding{{
		Check:    CheckCacheHints,
		Severity: Info,
		Message:  "no cache-control hints (ttlMs / cacheScope) observed on list/read results",
		Fix:      "consider adding ttlMs and cacheScope (SEP-2549) to cacheable results so clients can cache them; optional but recommended for production",
	}}
}

// checkAuthPosture reports on authorization enforcement for HTTP endpoints. The
// 2026-07-28 spec hardens authorization; a public unauthenticated endpoint is a
// risk worth flagging.
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
			Fix:      "verify the flow also validates the iss parameter (RFC 9207 / SEP-2468) and declares application_type on dynamic client registration (SEP-837)",
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
			Fix:      "if this endpoint is not intended to be public, enforce authorization; 2026-07-28 hardens auth (RFC 9207 iss validation, application_type on DCR)",
		}}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
