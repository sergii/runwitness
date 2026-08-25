# RunWitness

RunWitness is a local execution witness for developers, CI systems, and coding agents.

It runs a command inside an explicit Run boundary and records what actually happened: process outcome, stdout/stderr, repository state, versioned runtime Evidence, semantic Findings, deterministic quality gates, and explicit Run-to-Run Finding diffs.

The core question is:

> What actually changed in application behavior when this code was executed?

## Install

RunWitness requires Go 1.23 or newer when installed from source/module:

```bash
go install github.com/sergii/runwitness/cmd/runwitness@v0.0.7
```

Check the version:

```bash
runwitness --version
# RunWitness v0.0.7
```

## Run commands

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

## Compare with a baseline Run

Select an existing local Run explicitly:

```bash
runwitness run --baseline <run_id> -- bundle exec rspec
```

RunWitness resolves the selected baseline from the current local Run store, executes and observes the current command, then compares finalized semantic Finding sets by stable `finding_id`.

```text
new       = current - baseline
resolved  = baseline - current
unchanged = current ∩ baseline
```

The current `run.json` records the baseline and deterministic diff:

```json
{
  "baseline": {
    "run_id": "0198f5f0-0000-7000-8000-000000000001"
  },
  "diff": {
    "new": ["rwf_..."],
    "resolved": ["rwf_..."],
    "unchanged": ["rwf_..."],
    "regressed": [],
    "improved": []
  }
}
```

Lists are unique and lexicographically sorted. `regressed` and `improved` are reserved for later metric-aware comparison.

Baseline selection is explicit and local. If the requested baseline is missing, unreadable, malformed, or does not match its requested Run ID, RunWitness exits `2` before executing the target and before creating a new Run.

If current observation ends with verdict `error`, RunWitness does not claim Findings were resolved. The selected baseline may be recorded, but `diff` is omitted.

## Gate only on new Findings

v0.0.7 makes baseline comparison actionable with an explicit Finding gate scope:

```bash
runwitness run \
  --baseline <run_id> \
  --gate-scope new \
  -- bundle exec rspec
```

Supported scopes are:

```text
all
new
```

The default remains `all`, preserving the absolute gate behavior from earlier releases. An unchanged runtime problem therefore still fails unless `--gate-scope new` is explicitly selected.

With `new` scope, RunWitness keeps the complete current Finding set and diff, but existing Finding-based gates consider only Finding IDs classified in `diff.new`:

```text
eligible finding_ids = original finding_ids ∩ diff.new
```

That allows a patch-validation workflow such as:

```text
baseline: runtime.error A
current:  runtime.error A
                    ↓
               unchanged
                    ↓
          --gate-scope new
                    ↓
                  PASS
```

The known problem is still present in `findings` and `diff.unchanged`; RunWitness does not hide it. It simply does not attribute it to the current change.

A newly introduced problem still fails:

```text
baseline: clean
current:  runtime.error B
                    ↓
                   new
                    ↓
          --gate-scope new
                    ↓
                  FAIL
```

When the scope is explicitly supplied, it is recorded with the baseline metadata:

```json
{
  "baseline": {
    "run_id": "0198f5f0-0000-7000-8000-000000000001",
    "finding_gate_scope": "new"
  }
}
```

Gate scoping never weakens non-Finding failures. A non-zero target exit remains `fail`, and RunWitness or required-adapter failures remain `error`. Invalid gate-scope usage is rejected before the Run boundary.

## Rails runtime evidence

Observe Rails runtime errors explicitly:

```bash
runwitness run --rails -- bundle exec rspec
```

The Rails adapter subscribes through the standard `Rails.error` reporter interface. Handled Rails error reports are normalized as `rails.error` Evidence and converted into deterministic `runtime.handled_error` Findings.

A successful test process can therefore still fail the runtime gate:

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

If `--rails` is explicitly requested but RunWitness cannot confirm Rails error observation, the Run ends with verdict `error` and RunWitness exits `2`.

The release gate executes this behavior against real Rails 8.1 on Ruby 3.4.

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

If the binary lives elsewhere:

```bash
RUNWITNESS_OTLP_MCP_BIN=/path/to/otlp-mcp \
  runwitness run --otel -- bundle exec rspec
```

RunWitness starts an isolated `otlp-mcp` process, discovers its Run-local OTLP endpoint, creates start/end snapshots, injects exporter environment variables, executes the target, and normalizes telemetry between the snapshots.

Normalized Evidence kinds currently include:

```text
otel.span
otel.log
otel.metric
rails.error
```

The Evidence schema is `schemas/evidence-v1.schema.json`.

## Runtime Findings and gates

OpenTelemetry error spans produce the first universal runtime Finding:

```text
otel.span status=ERROR
        ↓
runtime.error Finding
        ↓
runtime.no_errors gate
        ↓
verdict fail
```

Rails handled errors feed the same gate:

```text
Rails.error handled=true
        ↓
runtime.handled_error Finding
        ↓
runtime.no_errors gate
        ↓
verdict fail
```

This keeps process status and observed runtime behavior separate. A target can exit successfully while runtime evidence proves that the change is not behaviorally clean.

## Stable Finding identity

Finding identity is stable across independent Runs.

The same logical problem produces the same `finding_id` even when Run IDs, Evidence IDs, trace/span IDs, and timestamps differ. Run-local Evidence references remain separate.

That stable identity drives baseline classification and baseline-aware gate scoping.

## Git state

When the working directory belongs to a Git repository, RunWitness records repository root, branch, HEAD SHA, dirty state, and a deterministic fingerprint before and after execution.

The fingerprint covers tracked changes plus untracked, non-ignored files. RunWitness's own `.runwitness/` artifacts are excluded so the observer does not contaminate the state it measures.

## Verdicts and exit codes

```text
0 = pass or warn
1 = target/application or runtime quality-gate failure
2 = RunWitness/usage/instrumentation error
```

The Run model supports `pass`, `warn`, `fail`, and `error`.

## Contract-first development

RunWitness treats external behavior as a contract:

> No implementation before executable contract. No contract changes to make an implementation pass.

Black-box acceptance tests are written and reviewed first. During implementation they are locked. See `CONTRIBUTING.md` for the workflow.

## Current scope

v0.0.7 includes:

- universal Runner core;
- Git and process evidence;
- OpenTelemetry Evidence through `otlp-mcp`;
- Rails `Rails.error` Evidence through `--rails`;
- `runtime.error` and `runtime.handled_error` Findings;
- deterministic Finding identity;
- the built-in `runtime.no_errors` quality gate;
- explicit local baseline selection through `--baseline <run_id>`;
- deterministic `new`, `resolved`, and `unchanged` Finding comparison;
- explicit baseline-aware Finding gate scope through `--gate-scope all|new`;
- real upstream OTEL and real Rails interoperability gates.

Still deliberately deferred are automatic baseline selection, metric-aware `regressed`/`improved` comparison, configurable per-rule policies, ActiveRecord query regression/N+1 analysis, browser evidence, deeper PostgreSQL analysis, a public RunWitness MCP interface, and local-to-production correlation.

Relevant specifications live under `specs/`. The canonical Run JSON Schema is `schemas/run-v1.schema.json`, and normalized Evidence uses `schemas/evidence-v1.schema.json`.
