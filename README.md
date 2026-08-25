# RunWitness

RunWitness is a local execution witness for developers, CI systems, and coding agents.

It runs a command inside an explicit Run boundary and records what actually happened: process outcome, stdout/stderr, repository state before and after execution, versioned runtime Evidence, semantic Findings, and deterministic quality gates.

The core question is:

> What actually changed in application behavior when this code was executed?

## Install

RunWitness requires Go 1.23 or newer when installed from source/module:

```bash
go install github.com/sergii/runwitness/cmd/runwitness@v0.0.5
```

Check the version:

```bash
runwitness --version
# RunWitness v0.0.5
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

## Rails runtime evidence

v0.0.5 adds explicit Rails runtime observation:

```bash
runwitness run --rails -- bundle exec rspec
```

The Rails adapter subscribes through the standard `Rails.error` reporter interface. Handled Rails error reports are normalized as `rails.error` Evidence and converted into `runtime.handled_error` Findings.

A successful test process can therefore still fail the RunWitness runtime gate:

```text
tests / target exit        0
Rails.error handled        1
          ↓
runtime.handled_error
          ↓
runtime.no_errors          FAIL
          ↓
RunWitness CLI exit        1
```

RunWitness preserves the target process exit code. The quality gate changes the RunWitness verdict, not the target result.

If `--rails` is explicitly requested but RunWitness cannot confirm that a Rails error reporter was observed, the Run ends with verdict `error` and RunWitness exits `2` rather than claiming a complete observation.

The release gate runs this behavior against real Rails 8.1 on Ruby 3.4, not only a Rails-compatible fixture.

## OpenTelemetry evidence

Observe a command with OpenTelemetry:

```bash
runwitness run --otel -- bundle exec rspec
```

RunWitness deliberately does not implement its own OTLP collector. The reference adapter uses [`tobert/otlp-mcp`](https://github.com/tobert/otlp-mcp) as a local, Run-owned backend.

Install `otlp-mcp` separately and make sure it is available on `PATH`:

```bash
go install github.com/tobert/otlp-mcp/cmd/otlp-mcp@latest
```

The current upstream module requires Go 1.25. If the binary lives elsewhere:

```bash
RUNWITNESS_OTLP_MCP_BIN=/path/to/otlp-mcp \
  runwitness run --otel -- bundle exec rspec
```

For an observed Run, RunWitness starts an isolated `otlp-mcp` process, discovers its Run-local OTLP endpoint, creates start/end snapshots, injects exporter environment variables, executes the target, and normalizes telemetry between the two snapshots.

The target receives exporter variables such as:

```text
OTEL_EXPORTER_OTLP_ENDPOINT=<run-local endpoint>
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
```

RunWitness preserves existing `OTEL_RESOURCE_ATTRIBUTES` and adds:

```text
runwitness.run_id=<uuidv7>
```

Normalized Evidence kinds currently include:

```text
otel.span
otel.log
otel.metric
rails.error
```

The Evidence schema is `schemas/evidence-v1.schema.json`.

## Runtime findings and gates

The first OpenTelemetry semantic rule is:

```text
otel.span status=ERROR
        ↓
runtime.error Finding
        ↓
runtime.no_errors gate
        ↓
verdict fail
```

The Rails rule introduced in v0.0.5 feeds the same gate:

```text
Rails.error handled=true
        ↓
runtime.handled_error Finding
        ↓
runtime.no_errors gate
        ↓
verdict fail
```

This keeps process status and observed runtime behavior separate. A target can exit successfully while runtime evidence still proves that the change is not behaviorally clean.

RunWitness or instrumentation failures retain higher priority and produce verdict `error` with CLI exit `2`.

## Stable Finding identity

Finding identity is stable across independent Runs.

The same logical problem produces the same `finding_id` even when Run IDs, Evidence IDs, trace/span IDs, and timestamps differ. Run-local Evidence references remain separate.

This invariant is the foundation for future Run comparison semantics:

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

## Contract-first development

RunWitness treats external behavior as a contract.

The project rule is:

> No implementation before executable contract. No contract changes to make an implementation pass.

Black-box acceptance tests are written and reviewed first. During implementation they are locked. See `CONTRIBUTING.md` for the workflow.

## Current scope

v0.0.5 includes:

- universal Runner core;
- Git and process evidence;
- OpenTelemetry Evidence through `otlp-mcp`;
- Rails `Rails.error` Evidence through `--rails`;
- `runtime.error` and `runtime.handled_error` Findings;
- deterministic Finding identity;
- the built-in `runtime.no_errors` quality gate;
- real upstream OTEL and real Rails interoperability gates.

Still deliberately deferred are baseline Run comparison, ActiveRecord query regression/N+1 analysis, browser evidence, deeper PostgreSQL analysis, a public RunWitness MCP interface, and local-to-production correlation.

Relevant specifications live under `specs/`. The canonical Run JSON Schema is `schemas/run-v1.schema.json`, and normalized Evidence uses `schemas/evidence-v1.schema.json`.
