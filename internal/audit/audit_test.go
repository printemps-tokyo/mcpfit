package audit

import "testing"

func findingFor(r Report, check string) (Finding, bool) {
	for _, f := range r.Findings {
		if f.Check == check {
			return f, true
		}
	}
	return Finding{}, false
}

func countSeverity(r Report, check string, sev Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Check == check && f.Severity == sev {
			n++
		}
	}
	return n
}

// cleanObservation is a modern, ready HTTP server.
func cleanObservation() Observation {
	return Observation{
		Target:          "https://example.com/mcp",
		Transport:       "http",
		ProtocolVersion: TargetSpecVersion,
		ServerName:      "modern",
		Tools: []Tool{{
			Name:     "search",
			HasInput: true,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
				"required":   []any{"query"},
			},
		}},
		CacheHints:     true,
		AuthChallenged: true,
	}
}

func TestRun_CleanServerIsReady(t *testing.T) {
	r := Run(cleanObservation())
	if r.Counts.Fail != 0 {
		t.Fatalf("clean server has FAILs: %+v", r.Findings)
	}
	if r.Grade != GradeReady {
		t.Errorf("grade = %q, want %q", r.Grade, GradeReady)
	}
	if r.ExitCode(false) != 0 || r.ExitCode(true) != 0 {
		t.Errorf("clean server exit codes non-zero: %d/%d", r.ExitCode(false), r.ExitCode(true))
	}
	if f, ok := findingFor(r, CheckProtocolVersion); !ok || f.Severity != Pass {
		t.Errorf("protocol-version = %+v", f)
	}
	if f, ok := findingFor(r, CheckSessionReliance); !ok || f.Severity != Pass {
		t.Errorf("session-reliance = %+v", f)
	}
}

func TestRun_LegacyServerNotReady(t *testing.T) {
	obs := Observation{
		Target:          "legacy-server",
		Transport:       "http",
		ProtocolVersion: "2025-06-18",
		SessionIDIssued: true,
		SessionIDValue:  "abc123",
		TasksCapability: true,
		Tools: []Tool{{
			Name:     "legacy_tool",
			HasInput: true,
			InputSchema: map[string]any{
				"$schema":     "http://json-schema.org/draft-04/schema#",
				"id":          "urn:legacy",
				"type":        "object",
				"definitions": map[string]any{"p": map[string]any{"type": "number"}},
				"properties": map[string]any{
					"count": map[string]any{"type": "integer", "exclusiveMinimum": true},
				},
				"required": []any{"count", "missing"},
			},
		}},
		TaskMethodsBlocking: []string{"tasks/list"},
	}
	r := Run(obs)
	if r.Counts.Fail == 0 {
		t.Fatalf("legacy server produced no FAILs: %+v", r.Findings)
	}
	if r.Grade != GradeNotReady {
		t.Errorf("grade = %q, want %q", r.Grade, GradeNotReady)
	}
	if r.ExitCode(false) != 1 {
		t.Errorf("legacy exit code = %d, want 1", r.ExitCode(false))
	}
	// session-reliance must FAIL on an issued Mcp-Session-Id.
	if f, _ := findingFor(r, CheckSessionReliance); f.Severity != Fail {
		t.Errorf("session-reliance severity = %v, want Fail", f.Severity)
	}
	// deprecated-tasks must WARN on the blocking method.
	if f, _ := findingFor(r, CheckDeprecatedTasks); f.Severity != Warn {
		t.Errorf("deprecated-tasks severity = %v, want Warn", f.Severity)
	}
	// tool-schema must contain the boolean-exclusiveMinimum FAIL.
	if countSeverity(r, CheckToolSchema, Fail) == 0 {
		t.Errorf("expected a FAIL from tool-schema, findings: %+v", r.Findings)
	}
	// protocol-version older is INFO, not a failure.
	if f, _ := findingFor(r, CheckProtocolVersion); f.Severity != Info {
		t.Errorf("protocol-version severity = %v, want Info", f.Severity)
	}
}

func TestRun_StrictPromotesWarn(t *testing.T) {
	// stdio server with a loose schema -> WARN but no FAIL.
	obs := Observation{
		Target:          "cmd",
		Transport:       "stdio",
		ProtocolVersion: "2025-06-18",
		Tools: []Tool{{
			Name:        "t",
			HasInput:    true,
			InputSchema: map[string]any{"type": "object"}, // loose -> WARN
		}},
	}
	r := Run(obs)
	if r.Counts.Fail != 0 {
		t.Fatalf("unexpected FAIL: %+v", r.Findings)
	}
	if r.Counts.Warn == 0 {
		t.Fatalf("expected a WARN, got none: %+v", r.Findings)
	}
	if r.Grade != GradeWarnings {
		t.Errorf("grade = %q, want %q", r.Grade, GradeWarnings)
	}
	if r.ExitCode(false) != 0 {
		t.Errorf("non-strict exit = %d, want 0", r.ExitCode(false))
	}
	if r.ExitCode(true) != 1 {
		t.Errorf("strict exit = %d, want 1", r.ExitCode(true))
	}
}

func TestCheckAuthPosture(t *testing.T) {
	// no auth, no creds -> WARN
	warn := Run(Observation{Transport: "http", ProtocolVersion: TargetSpecVersion})
	if f, _ := findingFor(warn, CheckAuthPosture); f.Severity != Warn {
		t.Errorf("unauth endpoint severity = %v, want Warn", f.Severity)
	}
	// creds provided -> INFO (not probed)
	creds := Run(Observation{Transport: "http", ProtocolVersion: TargetSpecVersion, AuthCredentialsProvided: true})
	if f, _ := findingFor(creds, CheckAuthPosture); f.Severity != Info {
		t.Errorf("creds-provided severity = %v, want Info", f.Severity)
	}
	// stdio -> no auth-posture finding
	stdio := Run(Observation{Transport: "stdio", ProtocolVersion: TargetSpecVersion})
	if _, ok := findingFor(stdio, CheckAuthPosture); ok {
		t.Errorf("auth-posture should not apply to stdio")
	}
}

func TestCheckCacheHints(t *testing.T) {
	with := Run(cleanObservation())
	if f, _ := findingFor(with, CheckCacheHints); f.Severity != Info {
		t.Errorf("cache-hints severity = %v, want Info", f.Severity)
	}
}
