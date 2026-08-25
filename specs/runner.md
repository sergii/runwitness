# RunWitness Runner Specification

Status: Draft
Target: v0.0.1

## 1. Purpose

RunWitness Runner is the deterministic execution coordinator for a development run.

It is not an AI agent and does not decide how to change code. Its job is to execute a command inside a clearly defined run boundary, collect evidence from available sources, normalize that evidence, derive findings, compare the run with an explicit baseline when requested, and produce a deterministic machine-readable verdict.

An external coding agent, developer, CI job, IDE integration, or other automation can consume the result.

The core product question is:

> What actually changed in application behavior when this code was executed?

## 2. Core concept: Run

A Run is the primary unit of RunWitness.

A Run represents one bounded execution of code plus enough repository and runtime context to reproduce, compare, and reason about the observed behavior.

For v0.0.1 a Run records at least:

- `run_id`
- start and finish timestamps
- duration
- working directory
- command and arguments
- process exit status
- repository identity when available
- git branch when available
- git HEAD SHA before execution
- git HEAD SHA after execution
- dirty working-tree state before execution
- dirty working-tree state after execution
- deterministic hashes of the working-tree diff before and after execution when available
- environment label such as `local`, `test`, or `ci`
- RunWitness Runner version

`run_id` MUST be a UUIDv7.

Git commit identity alone is not sufficient to identify a development state because multiple runs can execute different uncommitted patches on the same commit. RunWitness therefore captures dirty-state and diff hashes as part of the run context.

Future metadata may include:

- agent identity and session
- task or issue reference
- parent run
- framework/runtime metadata
- deployment or production correlation metadata

## 3. Implementation language

The universal Runner SHOULD be implemented in Go.

The reason is distribution and portability rather than raw performance. The target user experience is one small binary that can coordinate Ruby, Python, Node.js, Go, Java, Elixir, and other application runtimes without requiring the host application language to implement the Runner itself.

Framework-specific intelligence belongs in adapters, not in the universal core.

## 4. CLI contract

The primary v0.0.1 command is:

```text
runwitness run -- <command> [args...]
```

Examples:

```text
runwitness run -- bundle exec rspec
runwitness run -- bin/rails test
runwitness run -- pytest
runwitness run -- npm test
runwitness run -- go test ./...
```

The `--` separator MUST distinguish RunWitness arguments from target-command arguments.

Initial optional arguments SHOULD include:

```text
--baseline <run_id>
--label <name>
```

Future options may include:

```text
--json
--strict
--meta key=value
--fail-on-warn
```

Project-level configuration SHOULD use `.runwitness.yml` when configuration becomes necessary.

## 5. Execution boundary

The Runner MUST create an explicit boundary around the target command.

For v0.0.1 the boundary is centered on a process tree launched directly by RunWitness.

The Runner MUST inject:

```text
RUNWITNESS_RUN_ID=<uuidv7>
```

into the target process environment so child processes inherit the Run identity where the operating system and target runtime support it.

The Runner SHOULD additionally use:

- process identity and process-group information
- start and finish timestamps
- adapter lifecycle boundaries
- OpenTelemetry metadata where available

Adapters use the conceptual lifecycle:

```text
before_run
execute target command
after_run
```

For OpenTelemetry-backed evidence, adapters SHOULD attach or correlate `runwitness.run_id` with the active Run. An initial integration may also use source-specific snapshots, for example an `otlp-mcp` snapshot before execution and another after execution.

Long-running application servers that are not launched by the Runner are out of scope for v0.0.1. Future versions may propagate Run identity through HTTP headers, W3C Baggage, span attributes, or another cross-process mechanism.

## 6. Evidence sources

The Runner is source-agnostic. Evidence sources are adapters.

### Core sources for v0.0.1

The core Runner owns:

- process exit status
- stdout and stderr capture
- process timing
- git state and diff hashes

### First universal adapter

The first universal runtime-observability adapter SHOULD be OpenTelemetry, initially reusing an existing local backend such as `otlp-mcp` rather than implementing another collector, trace store, or viewer.

### First framework adapter

The first framework-specific adapter SHOULD target Ruby/Rails and may collect:

- Rails error reports
- Ruby runtime metadata
- Ruby allocation counters
- additional Rails-specific evidence where deterministic and low-overhead

Later adapters may include:

- PostgreSQL findings from pgbot
- browser evidence from Chrome DevTools or Playwright MCP
- deeper Ruby memory and allocation profilers
- static/security tools
- Node.js, Python, Java, and other runtime adapters

The Runner SHOULD reuse specialized tools instead of reimplementing collectors, profilers, database analyzers, or debuggers where practical.

## 7. Evidence

Evidence is an observed fact produced by a source during a Run.

Evidence MUST remain distinct from interpretation.

Examples:

- an exception was reported
- a request returned HTTP 500
- a SQL statement executed 27 times
- a browser console error occurred
- 14,802 Ruby objects were allocated
- a PostgreSQL query took 26 ms

Conceptual shape:

```json
{
  "evidence_id": "ev_...",
  "run_id": "019...",
  "source": "rails",
  "kind": "exception",
  "observed_at": "2026-08-25T00:00:00Z",
  "attributes": {},
  "payload": {}
}
```

Evidence SHOULD preserve source-specific detail while exposing normalized fields where useful.

Raw source payload SHOULD be retained when practical so higher-level interpretations remain auditable.

## 8. Findings

A Finding is an interpretation of one or more pieces of Evidence.

Examples:

- `runtime.unhandled_exception`
- `database.n_plus_one`
- `database.query_regression`
- `browser.console_error`
- `ruby.allocations_regression`
- `performance.request_regression`

A Finding SHOULD contain:

- deterministic `finding_id`
- kind
- severity
- rule identifier
- source or sources
- summary
- evidence references
- code location when known
- confidence when heuristic rather than deterministic
- baseline value when applicable
- current value when applicable
- delta when applicable

`finding_id` MUST be derived from a stable fingerprint of the logical problem rather than generated randomly. This allows RunWitness to recognize the same finding across runs and classify findings as new, unchanged, resolved, regressed, or improved.

Example fingerprint inputs may include:

```text
finding kind
code location or operation identity
exception class, query fingerprint, or equivalent domain key
```

Severity and quality-gate action MUST remain separate concepts. A finding may have severity `warning` while a project's policy chooses to treat that finding kind as a failing gate.

The long-term product value is expected to live primarily in Run semantics, correlation, deterministic or high-confidence Findings, comparison, and verdicts rather than raw telemetry collection.

## 9. Baseline and diff

A Run MAY be compared with another Run.

For v0.0.1 baseline selection is explicit only:

```text
runwitness run --baseline <run_id> -- <command>
```

Automatic baseline strategies such as `last`, `main`, or `gold` are deferred.

The diff model SHOULD distinguish:

- new findings
- resolved findings
- unchanged findings
- regressions
- improvements

Examples:

```text
SQL queries:       17 -> 43
Request duration:  121 ms -> 264 ms
Ruby allocations:  8,421 -> 14,802
Unhandled errors:  0 -> 1
```

A baseline is not required for absolute findings such as an unhandled exception or HTTP 5xx response.

## 10. Verdict

The Runner MUST produce a deterministic Run status derived from execution outcome and configured quality gates.

Statuses:

- `pass`
- `warn`
- `fail`
- `error`

Meanings:

- `pass` - target execution completed successfully and no configured gate produced a warning or failure.
- `warn` - target execution completed, no failing gate fired, but one or more non-blocking findings or warning gates exist.
- `fail` - target command failed or a configured quality gate failed.
- `error` - RunWitness itself could not complete the observation or analysis reliably.

An instrumentation failure MUST NOT be reported as an application failure. For example, if tests pass but the required OpenTelemetry adapter cannot collect evidence, the Run may have status `error`, not `fail`.

Initial CLI exit-code contract:

```text
0 = pass or warn
1 = fail
2 = RunWitness error
```

A future `--fail-on-warn` option may make warning verdicts non-zero for strict CI usage.

The AI agent MAY explain a verdict, but the agent MUST NOT be required to decide whether deterministic gates passed or failed.

## 11. Storage and output contract

v0.0.1 MUST NOT require a database.

Run artifacts SHOULD be stored under:

```text
.runwitness/
  runs/
    <run_id>/
      run.json
      evidence.jsonl
      stdout.log
      stderr.log
```

`run.json` MUST be the canonical machine-readable Run result and MUST include a versioned schema identifier.

Conceptual shape:

```json
{
  "schema_version": 1,
  "run": {},
  "summary": {},
  "findings": [],
  "verdict": {}
}
```

`evidence.jsonl` SHOULD contain append-friendly Evidence records.

Human-readable CLI output MUST be derived from the same underlying Run model rather than maintaining a separate semantic representation.

Future interfaces may include:

- JSON output to stdout
- MCP adapter
- CI annotations
- local web UI

All interfaces MUST consume the same Run model.

## 12. Core versus adapters

The core Runner owns only universal orchestration and semantics:

```text
Runner
Run model
process lifecycle
git state
local artifact storage
Evidence model
Finding model
diff
quality gates
Verdict
adapter lifecycle
```

Framework- or tool-specific logic belongs outside core:

```text
OpenTelemetry
Ruby / Rails
PostgreSQL / pgbot
Chrome / browser
Node.js
Python
Java
security tools
other profilers and analyzers
```

The core MUST NOT contain ActiveRecord-, Sidekiq-, Rails-, Postgres-, or browser-specific domain logic.

## 13. MCP boundary

MCP is not part of the Runner core.

The architecture SHOULD be:

```text
Coding agent
    |
    v
RunWitness MCP adapter
    |
    v
Run model / Runner
```

The CLI and Runner MUST remain fully usable without AI and without MCP.

MCP is a thin machine interface over the same Run model used by CLI and CI integrations.

## 14. Non-goals for v0.0.1

The first version is not intended to be:

- another OpenTelemetry collector
- another APM dashboard
- another Sentry clone
- another browser debugger
- an autonomous coding agent
- a replacement for specialized profilers or database analyzers
- a production observability backend
- an automatic baseline-selection system
- a long-running remote-server tracing system

RunWitness should compose specialized systems where useful and add the missing execution semantics, correlation, findings, comparison, and verdict layer.

## 15. Initial architecture boundary

```text
Application / Tests
        |
        +--> OpenTelemetry / otlp-mcp
        +--> Ruby / Rails adapter
        +--> future database analyzers
        +--> future browser adapters
        +--> future runtime profilers
        |
        v
RunWitness Runner
        |
        +--> Run model
        +--> Evidence normalization
        +--> Findings
        +--> explicit Baseline / Diff
        +--> Quality gates
        +--> Verdict
        |
        v
run.json / evidence.jsonl
        |
        +--> CLI
        +--> future MCP adapter
        +--> future CI integration
        |
        v
Developer or coding agent
```

## 16. Locked decisions for v0.0.1

The following decisions are now part of the v0.0.1 architecture:

1. Universal Runner implementation language: Go.
2. Primary CLI: `runwitness run -- <command>`.
3. Run identity: UUIDv7.
4. Initial Run propagation: `RUNWITNESS_RUN_ID` plus source-specific correlation and OpenTelemetry metadata when available.
5. Storage: versioned JSON/JSONL files under `.runwitness/`; no database required.
6. Baseline selection: explicit Run ID only.
7. Findings use deterministic fingerprints.
8. Finding severity and quality-gate action are separate.
9. Core contains only universal orchestration and semantics.
10. Framework/tool intelligence lives in adapters.
11. First universal runtime adapter: OpenTelemetry, initially reusing existing tooling such as `otlp-mcp`.
12. First framework adapter: Ruby/Rails.
13. Verdict states: `pass`, `warn`, `fail`, `error`.
14. MCP is a thin external adapter, not part of Runner core.

## 17. Deferred questions

The following questions remain deliberately open beyond v0.0.1:

1. Exact Finding taxonomy and versioning strategy beyond the initial rules.
2. Automatic baseline selection such as `last`, `main`, or curated gold runs.
3. Cross-request Run propagation for already-running servers.
4. Browser-to-backend correlation model.
5. Local-to-CI-to-production correlation model.
6. Long-term retention and indexing once filesystem storage becomes insufficient.
7. Package naming and distribution model for language/framework adapters.
8. MCP tool contract and CI annotation contract.
