# RunWitness

RunWitness is a local execution witness for developers, CI systems, and coding agents.

It runs a command inside an explicit Run boundary and records what actually happened: process outcome, stdout/stderr, repository state before and after execution, and versioned machine-readable runtime evidence. v0.0.4 adds stable cross-Run Finding identity on top of the semantic Findings and quality gates introduced in v0.0.3.

The core question is:

> What actually changed in application behavior when this code was executed?

## Install

RunWitness requires Go 1.23 or newer when installed from source/module:

```bash
go install github.com/sergii/runwitness/cmd/runwitness@v0.0.4
```

Check the version:

```bash
runwitness --version
# RunWitness v0.0.4
```

## Run commands

Run any command:

```bash
runwitness run -- echo hello
runwitness run -- bundle exec rspec
runwitness run -- pytest
runwitness run -- npm test
runwitness run -- go test ./...
```

Optionally label a Run:

```bash
runwitness run --label checkout-specs -- bundle exec rspec spec/requests/checkout_spec.rb
```

Each Run is stored under:

```text
.runwitness/runs/<run_id>/
  run.json
  evidence.jsonl
  stdout.log
  stderr.log
```

`run_id` is UUIDv7 and is propagated to the target process as `RUNWITNESS_RUN_ID`.

## OpenTelemetry evidence

Observe a command with OpenTelemetry:

```bash
runwitness run --otel -- bundle exec rspec
```

RunWitness deliberately does not implement its own OTLP collector. The reference adapter uses [`tobert/otlp-mcp`](https://github.com/tobert/otlp-mcp) as a local, Run-owned backend.

Install `otlp-mcp` separately and make sure it is available on `PATH`. One upstream-supported source installation path is:

```bash
go install github.com/tobert/otlp-mcp/cmd/otlp-mcp@latest
```

The current upstream module requires Go 1.25. If `otlp-mcp` lives elsewhere, point RunWitness at the executable:

```bash
RUNWITNESS_OTLP_MCP_BIN=/path/to/otlp-mcp \
  runwitness run --otel -- bundle exec rspec
```

For an observed Run, RunWitness starts an isolated `otlp-mcp` process, asks it for the Run-local OTLP endpoint, creates a start snapshot, injects exporter environment variables into the target process, executes the target, creates an end snapshot, and normalizes data between the two snapshots.

The target receives exporter variables such as:

```text
OTEL_EXPORTER_OTLP_ENDPOINT=<run-local endpoint>
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
```

RunWitness also preserves existing `OTEL_RESOURCE_ATTRIBUTES` and adds exactly one:

```text
runwitness.run_id=<uuidv7>
```

When the application is instrumented and exports telemetry during the Run, `evidence.jsonl` contains schema-valid Evidence records with initial kinds:

```text
otel.span
otel.log
otel.metric
```

The Evidence schema is `schemas/evidence-v1.schema.json`.

RunWitness does not yet auto-instrument Ruby, Python, Node.js, or another runtime. Existing OpenTelemetry instrumentation or a future language adapter is responsible for emitting telemetry.

If `--otel` is explicitly requested but the backend cannot be started or observed reliably, RunWitness returns verdict `error` rather than pretending the application failed or claiming a complete observation.

## Runtime findings and gates

The first semantic rule over normalized Evidence is:

```text
otel.span status=ERROR
        ↓
runtime.error Finding
        ↓
runtime.no_errors gate
        ↓
verdict fail
```

For each normalized OpenTelemetry span whose status is `ERROR`, RunWitness records a `runtime.error` Finding in `run.json`. The Finding references the exact Evidence record that caused it and carries rule ID `otel.span.error`.

Those Findings trigger the built-in `runtime.no_errors` gate with action `fail`.

This allows process status and observed runtime behavior to remain separate. For example, a test command can exit successfully while runtime telemetry still proves an application error occurred:

```text
target exit code: 0
runtime.error findings: 1
runtime.no_errors: triggered
RunWitness verdict: fail
RunWitness CLI exit: 1
```

RunWitness does not rewrite the target exit code. A RunWitness or instrumentation failure still has higher priority and produces verdict `error` with CLI exit `2`.

## Stable Finding identity

v0.0.4 makes Finding identity stable across independent Runs.

The same logical runtime problem produces the same `finding_id` even when its Run ID, Evidence ID, trace ID, span ID, and timestamps are different. For the current `otel.span.error` rule, the identity is derived from stable semantic inputs: Finding kind, rule ID, OpenTelemetry service name, and span name.

Run-local Evidence references remain separate. This means two Runs can point to different Evidence records while still recognizing that they observed the same logical Finding.

That invariant is the foundation for future Run comparison semantics such as:

```text
new
unchanged
resolved
regressed
improved
```

## Git state

When the working directory belongs to a Git repository, RunWitness records the repository root, branch, HEAD SHA, dirty state, and a deterministic fingerprint before and after execution.

The fingerprint covers tracked changes plus untracked, non-ignored files. RunWitness's own `.runwitness/` artifacts are excluded so the observer does not contaminate the state it measures.

## Verdicts and exit codes

The Run model supports four verdicts:

```text
pass
warn
fail
error
```

The stable CLI mapping is:

```text
0 = pass or warn
1 = target/application or runtime quality-gate failure
2 = RunWitness/usage/instrumentation error
```

A command that cannot be started is still recorded as a Run with verdict `error` and a null target exit code.

A target command that exits non-zero remains a target failure even when OTEL Evidence collection succeeds. Conversely, an OTEL collection failure is recorded as an instrumentation error, not as an application failure.

## Contract-first development

RunWitness treats external behavior as a contract.

The project rule is:

> No implementation before executable contract. No contract changes to make an implementation pass.

Black-box acceptance tests are written and reviewed first. During the implementation phase they are locked. See `CONTRIBUTING.md` for the workflow.

## Current scope

v0.0.4 consists of the universal Runner core, the OpenTelemetry Evidence adapter, the first semantic runtime Finding and quality gate, and deterministic Finding identity across Runs.

Still deliberately deferred are error-log and exception-event Findings, configurable gate policy, baselines and Run diffs, Ruby/Rails auto-instrumentation, browser evidence, PostgreSQL analysis, MCP as a public RunWitness interface, and local-to-production correlation.

Relevant specifications live under `specs/`. The canonical Run JSON Schema is `schemas/run-v1.schema.json`, and normalized Evidence uses `schemas/evidence-v1.schema.json`.
