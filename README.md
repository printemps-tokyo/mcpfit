# mcpfit

`mcpfit` audits a Model Context Protocol (MCP) server against the
2026-07-28 stateless specification, the current published revision, and prints
a graded migration report with a CI exit code. It probes `server/discover`,
falls back to the legacy `initialize` handshake when the server does not answer
it, enumerates the server's tools/resources/prompts, then runs an opinionated
set of checks and tells you what does not match the specification and how to
fix it.

> [!NOTE]
> The 2026-07-28 revision was published on 2026-07-28 and is what
> <https://modelcontextprotocol.io/specification/latest> resolves to. mcpfit
> reserves `FAIL` for concrete, high-confidence breakages against that text;
> requirements a server has yet to meet and mcpfit's own lint are `WARN`, and
> neutral context is `INFO`. mcpfit inspects a handful of observable signals.
> Treat the report as guidance, not a conformance certificate.

## How this differs from mcppeek

[`mcppeek`](https://github.com/printemps-tokyo/mcppeek) is an inspector: it
connects to an MCP server and lists what it exposes. `mcpfit` is a migration
auditor tied to one specific spec revision. It does not just list the surface;
it grades each part of it against the 2026-07-28 changes and returns a
pass/warn/fail verdict plus a CI exit code. Use `mcppeek` to see what a server
has; use `mcpfit` to find out whether that server matches the stateless spec.

## What the 2026-07-28 spec changed (and what mcpfit checks)

Sources (read these for the authoritative details):

- Specification: <https://modelcontextprotocol.io/specification/2026-07-28>
- Key changes since 2025-11-25:
  <https://modelcontextprotocol.io/specification/2026-07-28/changelog>
- Versioning and backward compatibility:
  <https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning>
- Source repository (tag `2026-07-28`):
  <https://github.com/modelcontextprotocol/modelcontextprotocol/tree/2026-07-28>

Changes the checks are anchored to:

- Stateless protocol core (SEP-2567 / SEP-2575). The `initialize` /
  `notifications/initialized` handshake and the protocol-level session
  (`Mcp-Session-Id` header) are removed. Protocol version and client
  capabilities travel in `_meta` on every request, so any request can hit any
  server instance. Servers must implement `server/discover`, which reports the
  supported versions, capabilities and identity. On Streamable HTTP every POST
  carries the `MCP-Protocol-Version` and `Mcp-Method` headers (SEP-2243).
- Tasks moved out of the core protocol into the
  `io.modelcontextprotocol/tasks` extension (SEP-2663). The blocking
  `tasks/result` is replaced by polling with `tasks/get`, `tasks/update` is
  added and `tasks/list` is removed.
- JSON Schema usage. A schema without `$schema` is JSON Schema 2020-12; a
  schema may declare another dialect with `$schema` and is then validated
  against that dialect; implementations are only required to support 2020-12.
  `inputSchema` and `outputSchema` may use any 2020-12 keyword (SEP-2106); the
  root of an `inputSchema` must be `"type": "object"`.
- Caching hints (SEP-2549). `ttlMs` and `cacheScope` are required on results of
  `server/discover`, `tools/list`, `prompts/list`, `resources/list`,
  `resources/templates/list` and `resources/read`.
- Authorization. Authorization servers should send `iss` and clients must
  validate it (RFC 9207 / SEP-2468); clients must send `application_type`
  during Dynamic Client Registration (SEP-837), and Dynamic Client Registration
  itself is deprecated in favor of Client ID Metadata Documents. Authorization
  remains optional for MCP implementations.

Changes in the same revision that mcpfit does not check: multi round-trip
requests and the required `resultType` field (SEP-2322), `subscriptions/listen`
replacing the HTTP GET stream and `resources/subscribe`, the removal of `ping`
and `logging/setLevel`, the removal of SSE resumability, trace context
conventions in `_meta` (SEP-414), and the deprecation of Roots, Sampling,
Logging and the HTTP+SSE transport under the new feature lifecycle policy
(SEP-2577 / SEP-2596).

## How mcpfit connects

mcpfit follows the backward-compatibility rules of the specification for a
client that supports both eras:

1. It sends `server/discover` with protocol version 2026-07-28 in `_meta`.
2. If the server returns a result with `supportedVersions`, the server is
   modern. Every later request carries the per-request `_meta` (and, on HTTP,
   the required headers); no handshake is sent.
3. If the server returns `UnsupportedProtocolVersionError` (`-32022`), it is
   modern but does not support 2026-07-28. mcpfit stops with exit code 2 and
   names the versions the server supports.
4. Any other error, a result without `supportedVersions`, or no answer within
   `--timeout` means the server is legacy. mcpfit falls back to `initialize`
   and audits what the legacy server exposes. A stdio server that stays silent
   on unknown requests therefore costs one `--timeout` before the fallback.

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

A legacy stdio server (it only answers `initialize`):

```
mcpfit readiness report
  Target:    ./mock-server
  Transport: stdio
  Server:    legacy-mock 0.1.0
  Protocol:  2025-06-18 (auditing against 2026-07-28)

  [INFO] protocol-version
        server did not answer server/discover and negotiated protocolVersion 2025-06-18 through the legacy initialize handshake; the current revision is 2026-07-28
        fix: to support 2026-07-28, implement server/discover and per-request metadata; the remaining checks show what to change first
  [INFO] session-reliance
        stdio transport: cross-request session state cannot be fully probed here
        fix: ensure any request can be served independently; 2026-07-28 has no protocol-level session, list results must not vary per connection, and cross-call state belongs in explicit handles passed as tool arguments
  [WARN] tool-schema-2020-12
        subject: legacy_tool (inputSchema $.id)
        uses draft-04 "id"; 2020-12 ignores it, losing the schema identifier
        fix: rename "id" to "$id"
  [WARN] tool-schema-2020-12
        subject: legacy_tool (inputSchema $.definitions)
        uses "definitions", which 2020-12 keeps only as a deprecated keyword replaced by "$defs"
        fix: rename "definitions" to "$defs" and update $ref targets to #/$defs/*
  [FAIL] tool-schema-2020-12
        subject: legacy_tool (inputSchema $.properties.count.exclusiveMinimum)
        exclusiveMinimum is a boolean (draft-04 form); 2020-12 requires a number
        fix: replace the boolean exclusiveMinimum with a numeric bound, e.g. "exclusiveMinimum": 0, or declare the dialect the schema is written in with $schema
  [WARN] tool-schema-2020-12
        subject: legacy_tool (inputSchema $.required)
        "required" names properties not in "properties": missing
        fix: add the missing entries to "properties" or drop them from "required"
  [WARN] deprecated-tasks
        server still answers task method(s) removed in 2026-07-28: tasks/list
        fix: move to the io.modelcontextprotocol/tasks extension: return a task handle and let clients poll tasks/get (and send input with tasks/update); tasks/list is removed and the blocking tasks/result is replaced by polling
  [WARN] cache-hints
        result(s) without both ttlMs and cacheScope: tools/list, resources/list, prompts/list
        fix: 2026-07-28 requires ttlMs (>= 0) and cacheScope ("public" or "private") on results of server/discover, tools/list, prompts/list, resources/list, resources/templates/list and resources/read (SEP-2549)

Summary: 0 pass, 2 info, 5 warn, 1 fail
Grade:   NOT READY
```

A clean stdio server that implements 2026-07-28 grades `READY`:

```
Summary: 4 pass, 1 info, 0 warn, 0 fail
Grade:   READY
```

## Exit-code contract

| Code | Meaning                                                              |
|------|---------------------------------------------------------------------|
| 0    | No `FAIL` findings (only pass/info/warn). With `--strict`, also no `WARN`. |
| 1    | At least one `FAIL`. With `--strict`, at least one `WARN` also yields 1.   |
| 2    | The audit could not run (bad arguments, or a connection/handshake error). |

This makes mcpfit drop-in for CI: a legacy server fails the build only when it
has a concrete breakage, and `--strict` lets you gate on warnings too. A modern
server that does not support 2026-07-28 is reported with exit code 2, because
this build audits that revision only.

## JSON output (`--json`)

`--json` emits the full report as a single JSON object:

```jsonc
{
  "target": "https://api.example.com/mcp",
  "transport": "http",
  "protocolVersion": "2025-11-25",
  "serverName": "example",
  "serverVersion": "1.2.0",
  "specVersion": "2026-07-28",
  "findings": [
    {
      "check": "session-reliance",
      "severity": "fail",
      "target": "",
      "message": "server issued an Mcp-Session-Id header ...; 2026-07-28 removed protocol-level sessions ...",
      "fix": "stop minting/requiring Mcp-Session-Id; ..."
    }
  ],
  "counts": { "pass": 2, "info": 1, "warn": 1, "fail": 1 },
  "grade": "NOT READY",
  "ready": false
}
```

Field notes:

- `protocolVersion` is 2026-07-28 for a server that lists it in
  `server/discover` (otherwise the newest version it lists), or the version a
  legacy server negotiated in `initialize`.
- `severity` is one of `pass`, `info`, `warn`, `fail`.
- `target` is the specific subject of a finding (for example a tool name and the
  offending JSON path); it is empty for whole-server findings.
- `fix` is omitted when there is nothing to change.
- `grade` is `READY`, `READY WITH WARNINGS`, or `NOT READY`; `ready` is `true`
  unless there is a `FAIL`.

## Check catalog

| Check                 | Grades        | What it looks at |
|-----------------------|---------------|------------------|
| `protocol-version`    | pass/info/warn | Which era and version the server speaks. `PASS` when it answers `server/discover` and lists 2026-07-28; `INFO` when it answers `server/discover` without listing it, or when it only speaks an older revision through `initialize`; `WARN` when it reports no version, or returns 2026-07-28 from `initialize` without answering `server/discover` (that revision has no handshake). |
| `session-reliance`    | pass/fail/info | Streamable HTTP: `FAIL` if the server issues an `Mcp-Session-Id` header (removed in 2026-07-28), whether in answer to a 2026-07-28 request or during the legacy handshake of a server that does not answer `server/discover`. stdio: `INFO` note that cross-request session state cannot be fully probed and must not be relied upon. |
| `tool-schema-2020-12` | pass/warn/fail/info | Structural checks on every tool `inputSchema` (and `outputSchema` when present). For schemas that are 2020-12 by default or by declaration: draft-04 `id` (`WARN`), deprecated `definitions` (`WARN`), boolean-form `exclusiveMinimum`/`exclusiveMaximum` (`FAIL`). For schemas that declare another dialect with `$schema`: one `INFO`, and the keyword checks are skipped because the schema is validated against its own dialect. For every schema: a missing `inputSchema` or missing root type on an `inputSchema` (`WARN`), a non-object `inputSchema` root (`FAIL`), malformed `properties`/`required` (`FAIL`), `required` naming undeclared properties (`WARN`), and an object that accepts anything (`INFO`). Each finding names the tool, the JSON path, and the fix. It is a targeted inspector, not a full validator. |
| `deprecated-tasks`    | pass/warn/info | If the server declares a tasks capability: `WARN` when the removed `tasks/list` still responds, `INFO` when it does not. Only asserts what is observable; the blocking `tasks/result` cannot be probed without a task. |
| `cache-hints`         | pass/warn/info | Whether `server/discover` and the list results carry both `ttlMs` and `cacheScope`, which 2026-07-28 requires. `WARN` names the methods that lack them; `INFO` when no cacheable result was returned. `resources/templates/list` and `resources/read` are not probed. |
| `auth-posture`        | info/warn     | Streamable HTTP: `INFO` when the endpoint challenges (401/403 or `WWW-Authenticate`) or when credentials were supplied (so enforcement cannot be probed); `WARN` when the endpoint served MCP without requiring authorization. Authorization is optional in MCP, so this `WARN` is mcpfit's own posture lint, not a specification requirement. |

## CLI reference

```
mcpfit [flags] <command> [args...]      audit a stdio server (spawned subprocess)
mcpfit [flags] <command> -- [args...]   pass args after -- verbatim to the command
mcpfit [flags] --url <endpoint>         audit a Streamable HTTP server

Flags:
  --url <endpoint>          Audit a Streamable HTTP MCP endpoint instead of stdio
  --header "K: V"           Add a request header, e.g. auth (repeatable, HTTP)
  --timeout <dur>           Per-request timeout (default 15s), e.g. 10s, 1500ms
  --protocol-version <ver>  protocolVersion to request in the legacy initialize
                            fallback (default 2025-06-18); modern servers are
                            always asked for 2026-07-28
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

Tests are fully offline: a tiny in-repo mock MCP server (a "modern" persona
that answers `server/discover` and rejects `initialize`, and two "legacy"
personas) is built and driven over the stdio path, and `httptest` servers
exercise the Streamable HTTP path for both eras, including the required request
headers and the `Mcp-Session-Id` and auth signals. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. See [LICENSE](LICENSE).
