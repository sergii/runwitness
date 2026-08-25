# RunWitness Runner Specification

Status: Draft

## 1. Purpose

RunWitness Runner is the deterministic execution coordinator for a development run.

It does not act as an AI agent and does not decide how to change code. Its job is to execute a command inside a clearly defined run boundary, collect evidence from available sources, normalize that evidence, compare it with a baseline when available, and produce a machine-readable verdict.

An external coding agent, developer, CI job, or other automation can consume the result.

## 2. Core concept: Run

A Run is the primary unit of RunWitness.

A Run represents one bounded execution of code with enough context to answer:

> What actually happened when this code was executed?

Minimum Run metadata:

- `run_id`
- start and finish timestamps
- working directory
- command and arguments
- process exit status
- repository identity when available
- git branch when available
- git commit before execution
- dirty working-tree state when available
- environment label such as `local`, `test`, or `ci`

Future metadata may include:

- agent identity and session
- task or issue reference
- git state after a patch
- parent/baseline run
- framework/runtime metadata

## 3. Inputs

The Runner accepts:

1. A command to execute.
2. Optional Run metadata.
3. Optional evidence-source configuration.
4. Optional baseline Run or baseline policy.
5. Optional quality-gate policy.

Example conceptual interface:

```text
runwitness run bundle exec rspec
```

The concrete CLI contract is intentionally not fixed by this specification yet.

## 4. Execution boundary

The Runner MUST create an explicit boundary around the target command.

The boundary MUST allow evidence collected during the execution to be attributed to the correct Run.

The initial implementation may use timestamps, process identity, environment variables, OpenTelemetry attributes, source-specific snapshots, or a combination of these mechanisms.

A future preferred mechanism is a propagated `run_id` that can travel through supported telemetry and framework adapters.

## 5. Evidence sources

The Runner is source-agnostic. Evidence sources are adapters.

Potential sources include:

- process exit status and stdout/stderr
- test-framework results
- OpenTelemetry traces, logs, and metrics
- Rails runtime/error instrumentation
- browser and Chrome DevTools data
- PostgreSQL findings from tools such as pgbot
- Ruby allocation and memory profiling
- security/static-analysis tools
- CI metadata

The Runner SHOULD reuse existing tools instead of reimplementing collectors, profilers, databases, or debuggers where practical.

## 6. Evidence

Evidence is an observed fact produced by a source during a Run.

Examples:

- an exception was raised
- a request returned HTTP 500
- a SQL statement executed 27 times
- a browser console error occurred
- 14,802 Ruby objects were allocated
- a PostgreSQL query regressed from 8 ms to 26 ms

Evidence SHOULD preserve source-specific detail while also exposing normalized fields when possible.

## 7. Findings

A Finding is an interpretation of one or more pieces of Evidence.

Examples:

- `runtime.unhandled_exception`
- `database.n_plus_one`
- `database.query_regression`
- `browser.console_error`
- `ruby.allocations_regression`
- `performance.request_regression`

A Finding SHOULD contain:

- stable `finding_id`
- kind
- severity
- source or sources
- summary
- evidence references
- code location when known
- confidence when heuristic rather than deterministic
- baseline comparison when applicable

The long-term value of RunWitness is expected to live primarily in Run semantics, correlation, and deterministic or high-confidence Findings rather than raw telemetry collection.

## 8. Baseline and diff

A Run MAY be compared with another Run or a stored baseline.

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

A baseline is not required for every Finding. Some findings, such as an unhandled exception, can be evaluated independently.

## 9. Verdict

The Runner MUST produce a deterministic Run status derived from execution outcome and configured quality gates.

Initial conceptual statuses:

- `pass`
- `warn`
- `fail`

Example policy:

```text
new unhandled exception       -> fail
new browser console error     -> fail
HTTP 5xx                      -> fail
query count regression > 100% -> warn
allocation regression > 50%   -> warn
```

The AI agent MAY explain a verdict, but the agent SHOULD NOT be required to determine whether deterministic gates passed or failed.

## 10. Output contract

The Runner MUST expose machine-readable output.

Conceptual shape:

```json
{
  "run_id": "...",
  "status": "fail",
  "command": "bundle exec rspec",
  "exit_code": 0,
  "findings": [],
  "summary": {}
}
```

Human-readable CLI output MAY be derived from the same model.

Future interfaces may include:

- CLI
- JSON output
- MCP server
- CI annotations
- local web UI

These interfaces MUST consume the same underlying Run model rather than define separate semantics.

## 11. Non-goals for the first version

The first version is not intended to be:

- another OpenTelemetry collector
- another APM dashboard
- another Sentry clone
- another browser debugger
- an autonomous coding agent
- a replacement for specialized profilers or database analyzers

RunWitness should compose those systems where useful and add the missing execution semantics, correlation, findings, comparison, and verdict layer.

## 12. Initial architecture boundary

```text
Application / Tests
        |
        +--> OpenTelemetry / otlp-mcp
        +--> framework adapters
        +--> database analyzers
        +--> browser/DevTools
        +--> runtime profilers
        |
        v
RunWitness Runner
        |
        +--> Run model
        +--> Evidence normalization
        +--> Findings
        +--> Baseline / Diff
        +--> Verdict
        |
        v
CLI / JSON / MCP / CI
        |
        v
Developer or coding agent
```

## 13. Open questions

The following decisions are deliberately deferred:

1. Implementation language for the universal Runner.
2. Exact CLI command structure.
3. Persistent storage format, if any.
4. Whether `run_id` propagation should use OpenTelemetry baggage, resource attributes, span attributes, environment variables, or a hybrid.
5. Exact Finding schema and taxonomy.
6. Baseline-selection strategy.
7. Which evidence adapters belong in core versus separate packages.
8. How much framework-specific intelligence belongs in RunWitness versus external tools.
9. How local Runs should correlate with CI and production telemetry.
10. Whether MCP belongs in the Runner process or as a thin adapter over the Run model.
