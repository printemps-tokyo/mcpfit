# mcpfit

`mcpfit` audits a Model Context Protocol (MCP) server for readiness against the
**2026-07-28 stateless specification** and prints a graded migration report with
a CI exit code. It connects to a target server, performs today's `initialize`
handshake, enumerates its tools/resources/prompts, then runs an opinionated set
of checks and tells you what will break under the new spec and how to fix it.

> [!IMPORTANT]
> The 2026-07-28 specification is a **release candidate** and may still change
> before it is finalized. mcpfit reserves `FAIL` for concrete, high-confidence
> breakages; anything speculative or forward-looking is reported as `WARN` or
> `INFO`. Treat the report as guidance, not a conformance certificate.

## How this differs from mcppeek

[`mcppeek`](https://github.com/printemps-tokyo/mcppeek) is an inspector: it
connects to an MCP server and lists what it exposes. `mcpfit` is a **migration
auditor** tied to one specific spec revision. It does not just list the surface;
it grades each part of it against the 2026-07-28 changes and returns a
pass/warn/fail verdict plus a CI exit code. Use `mcppeek` to see what a server
has; use `mcpfit` to find out whether that server is ready for the stateless
spec.

## What the 2026-07-28 spec changes (and what mcpfit checks)

Sources (read these for the authoritative details):

- Official RC announcement:
  <https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/>
- What changed / what breaks:
  <https://stacktr.ee/blog/mcp-2026-spec-changes>

Highlights the checks are anchored to:

- **Stateless protocol core (SEP-2567 / SEP-2575).** The `initialize` /
  `notifications/initialized` handshake and the protocol-level session
  (`Mcp-Session-Id` header) are removed. Protocol version, client info, and
  client capabilities now travel in `_meta` on every request, so any request can
  hit any server instance without sticky routing.
- **Extensions framework, MCP Apps, and a stateless Tasks extension
  (SEP-2663).** Blocking `tasks/result` is replaced by polling with `tasks/get`;
  `tasks/update` is added and `tasks/list` is removed.
- **Full JSON Schema 2020-12 validation** of tool input/output schemas.
- **Cache-control hints (SEP-2549):** `ttlMs` and `cacheScope` on cacheable
  results (`tools/list`, `prompts/list`, `resources/list`, `resources/read`,
  `resources/templates/list`).
- **W3C Trace Context propagation** (`traceparent` / `tracestate` / `baggage` in
  `_meta`).
- **Authorization hardening** (RFC 9207 `iss` validation / SEP-2468;
  `application_type` on Dynamic Client Registration / SEP-837).
- **A formal deprecation policy** (12-month minimum window; Roots, Sampling,
  Logging, and the HTTP+SSE transport are deprecated but not removed).

## Install

```bash
go install github.com/printemps-tokyo/mcpfit@latest
```

Prebuilt binaries for linux/darwin (amd64 + arm64) are attached to each tagged
release on GitHub. Download the tarball for your platform and verify it against
the accompanying `.sha256`.

Building from source requires Go 1.23 or newer. There are no third-party
dependencies: mcpfit is standard-library only.

## Quickstart

Audit a stdio server (mcpfit spawns it as a subprocess). Everything after `--`
is passed to the server verbatim:

```bash
mcpfit npx -- -y @modelcontextprotocol/server-everything
mcpfit ./my-mcp-server --flag value
```

Audit a Streamable HTTP server:

```bash
mcpfit --url https://api.example.com/mcp --header "Authorization: Bearer $TOKEN"
```

Machine-readable output and strict mode for CI:

```bash
mcpfit --json --strict --url https://api.example.com/mcp
```

## Example report

```
mcpfit readiness report
  Target:    go run ./mock-server
  Transport: stdio
  Server:    legacy-mock 0.1.0
  Protocol:  2025-06-18 (auditing against 2026-07-28)

  [INFO] protocol-version
        server negotiated protocolVersion 2025-06-18, older than the target 2026-07-28 (expected for servers not yet migrated)
        fix: plan to support protocolVersion 2026-07-28; the remaining checks show what to change first
  [FAIL] tool-schema-2020-12
        subject: legacy_tool (inputSchema $.properties.count.exclusiveMinimum)
        exclusiveMinimum is a boolean (draft-04 form); 2020-12 requires a number
        fix: replace the boolean exclusiveMinimum with a numeric bound, e.g. "exclusiveMinimum": 0
  [WARN] tool-schema-2020-12
        subject: legacy_tool (inputSchema $.definitions)
        uses legacy "definitions"; 2020-12 treats it as an unknown keyword, not a schema store
        fix: rename "definitions" to "$defs" and update $ref targets to #/$defs/*
  [WARN] deprecated-tasks
        server exposes legacy blocking task method(s): tasks/list
        fix: adopt the 2026-07-28 Tasks extension: return a task handle from tools/call and let clients poll tasks/get (and tasks/update); tasks/result and tasks/list are removed
  [INFO] cache-hints
        no cache-control hints (ttlMs / cacheScope) observed on list/read results
        fix: consider adding ttlMs and cacheScope (SEP-2549) to cacheable results so clients can cache them; optional but recommended for production

Summary: 0 pass, 3 info, 5 warn, 1 fail
Grade:   NOT READY
```

A clean, migrated server grades `READY`:

```
Summary: 3 pass, 2 info, 0 warn, 0 fail
Grade:   READY
```

## Exit-code contract

| Code | Meaning                                                              |
|------|---------------------------------------------------------------------|
| 0    | No `FAIL` findings (only pass/info/warn). With `--strict`, also no `WARN`. |
| 1    | At least one `FAIL`. With `--strict`, at least one `WARN` also yields 1.   |
| 2    | The audit could not run (bad arguments, or a connection/handshake error). |

This makes mcpfit drop-in for CI: a non-migrated server fails the build only
when it has a concrete breakage, and `--strict` lets you gate on warnings too.

## JSON output (`--json`)

`--json` emits the full report as a single JSON object:

```jsonc
{
  "target": "https://api.example.com/mcp",
  "transport": "http",
  "protocolVersion": "2025-06-18",
  "serverName": "example",
  "serverVersion": "1.2.0",
  "specVersion": "2026-07-28",
  "findings": [
    {
      "check": "session-reliance",
      "severity": "fail",
      "target": "",
      "message": "server issued an Mcp-Session-Id header ...",
      "fix": "stop minting/requiring Mcp-Session-Id; ..."
    }
  ],
  "counts": { "pass": 2, "info": 1, "warn": 1, "fail": 1 },
  "grade": "NOT READY",
  "ready": false
}
```

Field notes:

- `severity` is one of `pass`, `info`, `warn`, `fail`.
- `target` is the specific subject of a finding (for example a tool name and the
  offending JSON path); it is empty for whole-server findings.
- `fix` is omitted when there is nothing to change.
- `grade` is `READY`, `READY WITH WARNINGS`, or `NOT READY`; `ready` is `true`
  unless there is a `FAIL`.

## Check catalog

| Check                 | Grades        | What it looks at |
|-----------------------|---------------|------------------|
| `protocol-version`    | pass/info/warn | The negotiated `protocolVersion`. `PASS` when it equals 2026-07-28; `INFO` when it is older (expected today); `WARN` if none is reported. |
| `session-reliance`    | pass/warn/fail/info | Streamable HTTP: `FAIL` if the server issues or requires an `Mcp-Session-Id` header (removed by the stateless spec). stdio: `INFO` note that cross-request session state cannot be fully probed and must not be relied upon. |
| `tool-schema-2020-12` | pass/warn/fail/info | Structural JSON Schema 2020-12 checks on every tool `inputSchema` (and `outputSchema` when present): draft-04/06/07 residue (`id` vs `$id`, `definitions` vs `$defs`, boolean-form `exclusiveMinimum`/`exclusiveMaximum`, a pre-2020-12 `$schema`), missing/non-object root type, malformed `properties`/`required`, and overly-loose schemas. Each finding names the tool, the JSON path, and the fix. It is a targeted inspector, not a full validator. |
| `deprecated-tasks`    | pass/warn/info | If the server declares a tasks capability or exposes a removed blocking method: `WARN` when `tasks/list` still responds, `INFO` when a tasks capability is declared but no legacy method is observed. Only asserts what is observable. |
| `cache-hints`         | info          | Whether responses carry `ttlMs` / `cacheScope`. An opportunity, never a failure. |
| `auth-posture`        | info/warn     | Streamable HTTP: `INFO` when the endpoint challenges (401/403 or `WWW-Authenticate`) or when credentials were supplied (so enforcement cannot be probed); `WARN` when the endpoint served MCP without requiring authorization. |

## CLI reference

```
mcpfit [flags] <command> [args...]      audit a stdio server (spawned subprocess)
mcpfit [flags] <command> -- [args...]   pass args after -- verbatim to the command
mcpfit [flags] --url <endpoint>         audit a Streamable HTTP server

Flags:
  --url <endpoint>          Audit a Streamable HTTP MCP endpoint instead of stdio
  --header "K: V"           Add a request header, e.g. auth (repeatable, HTTP)
  --timeout <dur>           Per-request timeout (default 15s), e.g. 10s, 1500ms
  --protocol-version <ver>  protocolVersion to request in initialize
  --strict                  Treat WARN findings as failures for the exit code
  --json                    Emit the full structured report as JSON
  --list                    Also print the enumerated tools/resources/prompts
  --version                 Print version and exit
  --help                    Show this help and exit
```

All reads are bounded by `--timeout`, so mcpfit never hangs on a silent server.

## Development

```bash
gofmt -l .        # must print nothing
go vet ./...
go test ./...
go build ./...
```

Tests are fully offline: a tiny in-repo mock MCP server (a "modern" persona and
a "legacy" persona) drives the stdio path via `go run`, and an `httptest` server
exercises the Streamable HTTP path, including the `Mcp-Session-Id` and auth
signals. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. See [LICENSE](LICENSE).
