# RunWitness

RunWitness is a local execution witness for developers, CI systems, and coding agents.

It runs a command inside an explicit Run boundary and records what actually happened: process outcome, stdout/stderr, repository state before and after execution, and a versioned machine-readable result.

The product direction is broader runtime evidence, findings, before/after comparison, quality gates, and agent integrations. v0.0.1 deliberately starts with a small deterministic core.

## Why

A test suite can be green while a code change still introduces runtime regressions or hidden problems. RunWitness creates a stable execution identity and evidence boundary that future adapters can attach to.

The core question is:

> What actually changed in application behavior when this code was executed?

## v0.0.1

Build locally:

```bash
go build -o bin/runwitness ./cmd/runwitness
```

Or install from the module after a tagged release:

```bash
go install github.com/sergii/runwitness/cmd/runwitness@v0.0.1
```

Check the version:

```bash
runwitness --version
# RunWitness v0.0.1
```

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

The stable v0.0.1 CLI mapping is:

```text
0 = pass or warn
1 = target/application failure
2 = RunWitness/usage error
```

A command that cannot be started is still recorded as a Run with verdict `error` and a null target exit code.

## Contract-first development

RunWitness treats external behavior as a contract.

The project rule is:

> No implementation before executable contract. No contract changes to make an implementation pass.

Black-box acceptance tests are written and reviewed first. During the implementation phase they are locked. See `CONTRIBUTING.md` for the workflow.

## Current scope

v0.0.1 is the universal Runner core. It does not yet implement OpenTelemetry ingestion, Ruby/Rails evidence, database analysis, browser evidence, MCP, baselines, or quality gates.

Those features are intended to be adapters or later contract slices rather than reasons to make the core framework-specific.

Relevant specifications live under `specs/` and the canonical Run JSON Schema is `schemas/run-v1.schema.json`.
