# RunWitness

RunWitness is a local execution witness for developers, CI systems, and coding agents.

It runs a command inside an explicit Run boundary and records what actually happened: process outcome, stdout/stderr, repository state before and after execution, and a versioned machine-readable result. v0.0.2 adds opt-in OpenTelemetry Evidence collection without turning RunWitness into another telemetry backend.

The core question is:

> What actually changed in application behavior when this code was executed?

## Install

RunWitness requires Go 1.23 or newer when installed from source/module:

```bash
go install github.com/sergii/runwitness/cmd/runwitness@v0.0.2
```

Check the version:

```bash
runwitness --version
# RunWitness v0.0.2
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

v0.0.2 adds the first runtime Evidence adapter:

```bash
runwitness run --otel -- bundle exec rspec
```

RunWitness deliberately does not implement its own OTLP collector. The v0.0.2 reference adapter uses [`tobert/otlp-mcp`](https://github.com/tobert/otlp-mcp) as a local, Run-owned backend.

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

RunWitness does not auto-instrument Ruby, Python, Node.js, or another runtime in v0.0.2. Existing OpenTelemetry instrumentation or a future language adapter is responsible for emitting telemetry.

If `--otel` is explicitly requested but the backend cannot be started or observed reliably, RunWitness returns verdict `error` rather than pretending the application failed or claiming a complete observation.

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
1 = target/application failure
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

v0.0.2 consists of the universal Runner core plus the first OpenTelemetry Evidence adapter.

Still deliberately deferred are Ruby/Rails-specific evidence, Findings, baselines and Run diffs, quality gates, browser evidence, PostgreSQL analysis, MCP as a public RunWitness interface, and local-to-production correlation.

Relevant specifications live under `specs/`. The canonical Run JSON Schema is `schemas/run-v1.schema.json`, and normalized Evidence uses `schemas/evidence-v1.schema.json`.
