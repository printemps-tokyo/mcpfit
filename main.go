// Command mcpfit audits a Model Context Protocol server against the 2026-07-28
// stateless specification, the current published revision, and prints a graded
// migration report with a CI exit code.
//
// Unlike the org's mcppeek (which inspects and lists a server), mcpfit is an
// opinionated migration auditor: it probes server/discover, falls back to the
// legacy initialize handshake when the server does not answer it, enumerates
// tools/resources/prompts, then runs a fixed catalog of readiness checks and
// grades the result. See the README for the check catalog and the 2026-07-28
// changes it targets:
//   - https://modelcontextprotocol.io/specification/2026-07-28
//   - https://modelcontextprotocol.io/specification/2026-07-28/changelog
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/printemps-tokyo/mcpfit/internal/audit"
	"github.com/printemps-tokyo/mcpfit/internal/mcp"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

const usage = `mcpfit - audit an MCP server against the 2026-07-28 stateless specification

Usage:
  mcpfit [flags] <command> [args...]      audit a stdio server (spawned subprocess)
  mcpfit [flags] <command> -- [args...]   pass args after -- verbatim to the command
  mcpfit [flags] --url <endpoint>         audit a Streamable HTTP server

Flags:
  --url <endpoint>          Audit a Streamable HTTP MCP endpoint instead of stdio
  --header "K: V"           Add a request header, e.g. auth (repeatable, HTTP)
  --timeout <dur>           Per-request timeout (default 15s), e.g. 10s, 1500ms
  --protocol-version <ver>  protocolVersion to request in the legacy initialize
                            fallback (default ` + mcp.DefaultProtocolVersion + `); modern servers are
                            always asked for ` + audit.TargetSpecVersion + `
  --strict                  Treat WARN findings as failures for the exit code
  --json                    Emit the full structured report as JSON
  --list                    Also print the enumerated tools/resources/prompts
  --version                 Print version and exit
  --help                    Show this help and exit

Exit codes:
  0  no FAIL findings (PASS/INFO/WARN only); with --strict, also no WARN
  1  at least one FAIL finding (or, with --strict, at least one WARN)
  2  the audit could not run (bad arguments or connection/handshake error)

Examples:
  mcpfit npx -- -y @modelcontextprotocol/server-everything
  mcpfit ./my-mcp-server --flag value
  mcpfit --url https://api.example.com/mcp --header "Authorization: Bearer $TOKEN"
  mcpfit --json --strict --url https://api.example.com/mcp
`

type config struct {
	command         string
	args            []string
	url             string
	headers         []string
	timeout         time.Duration
	protocolVersion string
	strict          bool
	jsonOut         bool
	list            bool
	showVersion     bool
	showHelp        bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mcpfit: %v\n\nRun 'mcpfit --help' for usage.\n", err)
		return 2
	}
	if cfg.showHelp {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if cfg.showVersion {
		fmt.Fprintf(stdout, "mcpfit %s\n", version)
		return 0
	}
	if cfg.url == "" && cfg.command == "" {
		fmt.Fprintf(stderr, "mcpfit: no target: provide a server command or --url\n\nRun 'mcpfit --help' for usage.\n")
		return 2
	}
	if cfg.url != "" && cfg.command != "" {
		fmt.Fprintf(stderr, "mcpfit: choose one target: a stdio command or --url, not both\n")
		return 2
	}

	headers, err := parseHeaders(cfg.headers)
	if err != nil {
		fmt.Fprintf(stderr, "mcpfit: %v\n", err)
		return 2
	}

	mcp.SetVersion(version)
	client, err := mcp.New(mcp.Options{
		Command:         cfg.command,
		Args:            cfg.args,
		URL:             cfg.url,
		Headers:         headers,
		Timeout:         cfg.timeout,
		ProtocolVersion: cfg.protocolVersion,
	})
	if err != nil {
		fmt.Fprintf(stderr, "mcpfit: %v\n", err)
		return 2
	}
	defer client.Close()

	enum, obs, err := client.Probe()
	if err != nil {
		fmt.Fprintf(stderr, "mcpfit: %v\n", err)
		return 2
	}

	report := audit.Run(obs)

	if cfg.jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(report); encErr != nil {
			fmt.Fprintf(stderr, "mcpfit: %v\n", encErr)
			return 2
		}
	} else {
		writeText(stdout, report)
		if cfg.list {
			writeListing(stdout, enum)
		}
	}

	return report.ExitCode(cfg.strict)
}

func parseArgs(args []string) (config, error) {
	cfg := config{timeout: 15 * time.Second}
	var positional []string
	sawDoubleDash := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if sawDoubleDash {
			positional = append(positional, arg)
			continue
		}
		switch {
		case arg == "--":
			sawDoubleDash = true
		case arg == "--help" || arg == "-h":
			cfg.showHelp = true
		case arg == "--version" || arg == "-V":
			cfg.showVersion = true
		case arg == "--strict":
			cfg.strict = true
		case arg == "--json":
			cfg.jsonOut = true
		case arg == "--list":
			cfg.list = true
		case arg == "--url" || strings.HasPrefix(arg, "--url="):
			v, err := valueFor(arg, &i, args, "--url")
			if err != nil {
				return cfg, err
			}
			cfg.url = v
		case arg == "--header" || strings.HasPrefix(arg, "--header="):
			v, err := valueFor(arg, &i, args, "--header")
			if err != nil {
				return cfg, err
			}
			cfg.headers = append(cfg.headers, v)
		case arg == "--timeout" || strings.HasPrefix(arg, "--timeout="):
			v, err := valueFor(arg, &i, args, "--timeout")
			if err != nil {
				return cfg, err
			}
			d, perr := time.ParseDuration(v)
			if perr != nil {
				return cfg, fmt.Errorf("invalid --timeout %q: %w", v, perr)
			}
			if d <= 0 {
				return cfg, fmt.Errorf("invalid --timeout %q: must be positive", v)
			}
			cfg.timeout = d
		case arg == "--protocol-version" || strings.HasPrefix(arg, "--protocol-version="):
			v, err := valueFor(arg, &i, args, "--protocol-version")
			if err != nil {
				return cfg, err
			}
			cfg.protocolVersion = v
		case strings.HasPrefix(arg, "-") && arg != "-":
			// Once a positional (the command) has begun, unknown dashed tokens
			// belong to the command; before that, they are mcpfit flag errors.
			if len(positional) == 0 {
				return cfg, fmt.Errorf("unknown flag %q", arg)
			}
			positional = append(positional, arg)
		default:
			positional = append(positional, arg)
		}
	}

	if len(positional) > 0 {
		cfg.command = positional[0]
		cfg.args = positional[1:]
	}
	return cfg, nil
}

func valueFor(arg string, i *int, args []string, name string) (string, error) {
	if eq := strings.IndexByte(arg, '='); eq >= 0 {
		return arg[eq+1:], nil
	}
	if *i+1 >= len(args) {
		return "", fmt.Errorf("flag %s needs a value", name)
	}
	*i++
	return args[*i], nil
}

// parseHeaders splits "Key: Value" strings into a map, validating shape.
func parseHeaders(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, h := range raw {
		idx := strings.IndexByte(h, ':')
		if idx <= 0 {
			return nil, fmt.Errorf("invalid --header %q: expected \"Key: Value\"", h)
		}
		key := strings.TrimSpace(h[:idx])
		val := strings.TrimSpace(h[idx+1:])
		if key == "" {
			return nil, fmt.Errorf("invalid --header %q: empty key", h)
		}
		out[key] = val
	}
	return out, nil
}

// writeText renders the human-readable graded report.
func writeText(w io.Writer, r audit.Report) {
	fmt.Fprintf(w, "mcpfit readiness report\n")
	fmt.Fprintf(w, "  Target:    %s\n", r.Target)
	fmt.Fprintf(w, "  Transport: %s\n", r.Transport)
	if r.ServerName != "" {
		fmt.Fprintf(w, "  Server:    %s %s\n", r.ServerName, r.ServerVersion)
	}
	fmt.Fprintf(w, "  Protocol:  %s (auditing against %s)\n", orDash(r.ProtocolVersion), r.SpecVersion)
	fmt.Fprintln(w)

	for _, f := range r.Findings {
		fmt.Fprintf(w, "  [%s] %s\n", strings.ToUpper(f.Level), f.Check)
		if f.Target != "" {
			fmt.Fprintf(w, "        subject: %s\n", f.Target)
		}
		fmt.Fprintf(w, "        %s\n", f.Message)
		if f.Fix != "" {
			fmt.Fprintf(w, "        fix: %s\n", f.Fix)
		}
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Summary: %d pass, %d info, %d warn, %d fail\n",
		r.Counts.Pass, r.Counts.Info, r.Counts.Warn, r.Counts.Fail)
	fmt.Fprintf(w, "Grade:   %s\n", r.Grade)
}

// writeListing prints the enumerated server surface when --list is set.
func writeListing(w io.Writer, e mcp.Enumeration) {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Enumerated surface\n")
	fmt.Fprintf(w, "  Tools (%d):\n", len(e.Tools))
	for _, t := range e.Tools {
		fmt.Fprintf(w, "    - %s\n", t.Name)
	}
	fmt.Fprintf(w, "  Resources (%d):\n", len(e.Resources))
	for _, r := range e.Resources {
		name := r.Name
		if name == "" {
			name = r.URI
		}
		fmt.Fprintf(w, "    - %s\n", name)
	}
	fmt.Fprintf(w, "  Prompts (%d):\n", len(e.Prompts))
	for _, p := range e.Prompts {
		fmt.Fprintf(w, "    - %s\n", p.Name)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
