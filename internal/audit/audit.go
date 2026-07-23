// Package audit contains mcpfit's pure, transport-independent readiness logic:
// the check catalog, the JSON Schema inspector, and the report/grade model.
//
// Nothing in this package performs I/O. Callers gather an Observation of a
// target MCP server (via the internal/mcp transports) and hand that plain
// struct to Run, which returns a graded Report. Keeping the logic pure makes
// every check unit-testable without a live server.
//
// The checks are anchored to the MCP 2026-07-28 stateless specification
// release candidate. See:
//   - https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/
//   - https://stacktr.ee/blog/mcp-2026-spec-changes
//
// Because the 2026-07-28 specification is a release candidate that may still
// change, speculative or forward-looking findings are graded INFO or WARN;
// FAIL is reserved for concrete, high-confidence breakages.
package audit

// TargetSpecVersion is the stateless specification revision mcpfit audits
// against. Servers that already advertise this negotiate cleanly; older
// revisions are expected today and reported as INFO, not failures.
const TargetSpecVersion = "2026-07-28"

// Severity is the outcome of a single check. Ordered from best to worst so the
// grader can pick the dominant severity.
type Severity int

const (
	// Pass means the check found no problem.
	Pass Severity = iota
	// Info is neutral context or an opportunity, never a failure.
	Info
	// Warn is a likely problem or a forward-looking risk under the RC.
	Warn
	// Fail is a concrete, high-confidence breakage under 2026-07-28.
	Fail
)

// String returns the lowercase label used in text and JSON output.
func (s Severity) String() string {
	switch s {
	case Pass:
		return "pass"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Fail:
		return "fail"
	default:
		return "unknown"
	}
}

// Finding is one graded observation produced by a check.
type Finding struct {
	Check    string   `json:"check"`
	Severity Severity `json:"-"`
	// Level mirrors Severity as a string so JSON consumers do not depend on the
	// integer ordering.
	Level string `json:"severity"`
	// Target names the specific subject (e.g. a tool name) when a finding is
	// scoped to one; empty for whole-server findings.
	Target  string `json:"target,omitempty"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Report is the full audit result for one target.
type Report struct {
	Target          string    `json:"target"`
	Transport       string    `json:"transport"`
	ProtocolVersion string    `json:"protocolVersion"`
	ServerName      string    `json:"serverName,omitempty"`
	ServerVersion   string    `json:"serverVersion,omitempty"`
	SpecVersion     string    `json:"specVersion"`
	Findings        []Finding `json:"findings"`
	Counts          Counts    `json:"counts"`
	Grade           string    `json:"grade"`
	Ready           bool      `json:"ready"`
}

// Counts is the per-severity tally shown in the summary line.
type Counts struct {
	Pass int `json:"pass"`
	Info int `json:"info"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

// Grade labels.
const (
	GradeReady    = "READY"
	GradeWarnings = "READY WITH WARNINGS"
	GradeNotReady = "NOT READY"
)

// Run executes every check against an observed server and returns a graded
// report. It is deterministic: check order and, within a check, finding order
// are stable.
func Run(obs Observation) Report {
	r := Report{
		Target:          obs.Target,
		Transport:       obs.Transport,
		ProtocolVersion: obs.ProtocolVersion,
		ServerName:      obs.ServerName,
		ServerVersion:   obs.ServerVersion,
		SpecVersion:     TargetSpecVersion,
	}

	r.Findings = append(r.Findings, checkProtocolVersion(obs)...)
	r.Findings = append(r.Findings, checkSessionReliance(obs)...)
	r.Findings = append(r.Findings, checkToolSchemas(obs)...)
	r.Findings = append(r.Findings, checkDeprecatedTasks(obs)...)
	r.Findings = append(r.Findings, checkCacheHints(obs)...)
	r.Findings = append(r.Findings, checkAuthPosture(obs)...)

	for i := range r.Findings {
		r.Findings[i].Level = r.Findings[i].Severity.String()
		switch r.Findings[i].Severity {
		case Pass:
			r.Counts.Pass++
		case Info:
			r.Counts.Info++
		case Warn:
			r.Counts.Warn++
		case Fail:
			r.Counts.Fail++
		}
	}

	switch {
	case r.Counts.Fail > 0:
		r.Grade = GradeNotReady
		r.Ready = false
	case r.Counts.Warn > 0:
		r.Grade = GradeWarnings
		r.Ready = true
	default:
		r.Grade = GradeReady
		r.Ready = true
	}
	return r
}

// ExitCode maps a report to a CI exit status. A FAIL is always non-zero; with
// strict set, a WARN is non-zero too. INFO and PASS never fail the build.
func (r Report) ExitCode(strict bool) int {
	if r.Counts.Fail > 0 {
		return 1
	}
	if strict && r.Counts.Warn > 0 {
		return 1
	}
	return 0
}
