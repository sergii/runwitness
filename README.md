# RunWitness

RunWitness is a local execution witness for developers, CI systems, and coding agents.

It runs a command inside an explicit Run boundary and records what actually happened: process outcome, stdout/stderr, repository state, versioned runtime Evidence, semantic Findings, deterministic quality gates, and explicit Run-to-Run behavioral diffs.

The core question is:

> What actually changed in application behavior when this code was executed?

## Install

RunWitness requires Go 1.23 or newer when installed from source/module:

```bash
go install github.com/sergii/runwitness/cmd/runwitness@v0.0.11
```

Check the version:

```bash
runwitness --version
# RunWitness v0.0.11
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

## MCP for coding agents

RunWitness exposes the canonical local Run store and exact normalized Evidence records through a read-only MCP stdio server:

```bash
runwitness mcp
```

The public MCP tool surface contains exactly:

```text
list_runs
get_run
get_evidence
```

`list_runs` returns deterministic newest-first Run summaries. `get_run` returns the exact canonical decoded `run.json` document for a UUIDv7 Run ID. `get_evidence` accepts a Run ID plus an Evidence ID and returns the exact decoded normalized Evidence record from that Run's `evidence.jsonl`.

Successful calls expose machine-readable `structuredContent`, so an agent does not need to scrape terminal prose:

```text
get_run
   |
   v
Finding.evidence_refs[]
   |
   v
get_evidence(run_id, evidence_id)
   |
   v
exact normalized Evidence
```

The CLI, CI, and MCP adapter use the same canonical Run and Evidence models. The MCP surface is deliberately local and read-only. It does not create Runs, execute target commands, bind a network listener, expose arbitrary filesystem reads, follow Run-store symlink escapes, or mutate Run artifacts.

The server reads Runs only from:

```text
<cwd>/.runwitness/runs/
```

An absent store is a valid empty store and querying it does not create `.runwitness/`.

## Compare with a baseline Run

Select an existing local Run explicitly:

```bash
runwitness run --baseline <run_id> -- bundle exec rspec
```

RunWitness resolves the selected baseline from the current local Run store, executes and observes the current command, then compares finalized semantic Findings by stable `finding_id`.

The general set classifications are:

```text
new       = present only in current
resolved  = present only in baseline
unchanged = same stable Finding present on both sides with no metric change
```

Metric-aware Findings can additionally be classified as `regressed` or `improved`. Rails SQL query counts use these classes.

The current `run.json` records the selected baseline and deterministic diff:

```json
{
  "baseline": {
    "run_id": "0198f5f0-0000-7000-8000-000000000001"
  },
  "diff": {
    "new": [],
    "resolved": [],
    "unchanged": [],
    "regressed": ["rwf_..."],
    "improved": []
  }
}
```

Diff lists are unique and lexicographically sorted.

Baseline selection is explicit and local. If the requested baseline is missing, unreadable, malformed, or does not match its requested Run ID, RunWitness exits `2` before executing the target and before creating a new Run.

If current observation ends with verdict `error`, RunWitness does not claim Findings were resolved. The selected baseline may be recorded, but `diff` is omitted.

## Gate only on new Findings

Use an explicit Finding gate scope when validating a patch against a baseline:

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

The default remains `all`. With `new` scope, RunWitness keeps the complete current Finding set and diff, but existing Finding-based gates consider only Finding IDs classified in `diff.new`.

This does not weaken non-Finding failures. A non-zero target exit remains `fail`, and RunWitness or required-adapter failures remain `error`.

## Rails runtime evidence

Observe Rails behavior explicitly:

```bash
runwitness run --rails -- bundle exec rspec
```

### Rails.error

The Rails adapter subscribes through the standard `Rails.error` reporter interface. Handled Rails error reports are normalized as `rails.error` Evidence and converted into deterministic `runtime.handled_error` Findings.

A successful test process can therefore still fail the runtime gate:

```text
tests / target exit        0
Rails.error handled        1
          |
          v
runtime.handled_error
          |
          v
runtime.no_errors          FAIL
          |
          v
RunWitness CLI exit        1
```

RunWitness preserves the target process exit code. The quality gate changes the RunWitness verdict, not the target result.

### ActiveRecord SQL query counts

When `--rails` is enabled, relevant standard `sql.active_record` notifications become normalized `rails.sql` Evidence.

RunWitness normalizes SQL whitespace, ignores cached queries, blank SQL, and `SCHEMA` / `TRANSACTION` notification noise, then groups identical normalized statements into deterministic Findings:

```text
kind      database.query_count
rule_id   rails.sql.query_count
severity  info
```

For example, if the same normalized query is observed once in a baseline Run and three times in the current Run, the stable Finding receives comparison data:

```json
{
  "comparison": {
    "baseline": 1,
    "current": 3,
    "delta": 2,
    "delta_percent": 200.0,
    "unit": "queries"
  }
}
```

and is classified in `diff.regressed`. A lower count is `improved`; an equal count is `unchanged`.

```text
baseline query count       1
current query count        3
          |
          v
database.query_count
          |
          v
       regressed
```

By default this remains descriptive: a query-count regression becomes a warning Finding but does not add a database gate or change an otherwise passing verdict.

This preserves the policy boundary:

```text
Finding severity != gate action
```

### Fail on query-count regressions

v0.0.11 adds an explicit opt-in policy:

```bash
runwitness run \
  --rails \
  --baseline <run_id> \
  --fail-on-query-regression \
  -- bundle exec rspec
```

The flag requires an explicit baseline. When requested, RunWitness evaluates the finalized comparison state and adds:

```text
rule_id = database.no_query_count_regressions
action  = fail
```

Eligible `database.query_count` Findings in `diff.regressed` trigger the gate. The target process exit code is preserved, while the RunWitness verdict becomes `fail` and the CLI exits `1`.

If query counts are unchanged or improved, the requested gate remains visible with outcome `passed`.

This comparison gate is deliberately independent of `--gate-scope new`. A stable Finding that exists in both Runs but regresses numerically is still eligible even though it is not a new Finding.

The release gate executes Rails error and SQL observation against real Rails 8.1 on Ruby 3.4.

If `--rails` is explicitly requested but RunWitness cannot confirm the Rails error reporter, the Run ends with verdict `error` and RunWitness exits `2`.

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
rails.sql
```

The Evidence schema is `schemas/evidence-v1.schema.json`.

## Runtime Findings and gates

OpenTelemetry error spans produce a universal runtime Finding:

```text
otel.span status=ERROR
        |
        v
runtime.error Finding
        |
        v
runtime.no_errors gate
        |
        v
verdict fail
```

Rails handled errors feed the same gate. Rails SQL query-count Findings remain descriptive unless `--fail-on-query-regression` is explicitly selected.

This keeps process status, observed facts, semantic Findings, and policy separate. A target can exit successfully while runtime evidence proves that a configured quality gate should fail, while descriptive warning Findings can remain non-gating.

## Stable Finding identity

Finding identity is stable across independent Runs.

The same logical problem or metric produces the same `finding_id` even when Run IDs, Evidence IDs, timestamps, and observed query counts differ. Run-local Evidence references remain separate.

Stable identity drives baseline classification and baseline-aware gate policy.

## Git state

When the working directory belongs to a Git repository, RunWitness records repository root, branch, HEAD SHA, dirty state, and a deterministic fingerprint before and after execution.

The fingerprint covers tracked changes plus untracked, non-ignored files. RunWitness's own `.runwitness/` artifacts are excluded so the observer does not contaminate the state it measures.

## Verdicts and exit codes

```text
0 = pass or warn
1 = target/application or quality-gate failure
2 = RunWitness/usage/instrumentation error
```

The Run model supports `pass`, `warn`, `fail`, and `error`.

## Contract-first development

RunWitness treats external behavior as a contract:

> No implementation before executable contract. No contract changes to make an implementation pass.

Black-box acceptance tests are written and reviewed first. During implementation they are locked. See `CONTRIBUTING.md` for the workflow.

## Current scope

v0.0.11 includes:

- universal Runner core;
- Git and process evidence;
- OpenTelemetry Evidence through `otlp-mcp`;
- Rails `Rails.error` Evidence through `--rails`;
- Rails `sql.active_record` Evidence through `--rails`;
- `runtime.error` and `runtime.handled_error` Findings;
- deterministic `database.query_count` Findings for normalized Rails SQL statements;
- deterministic Finding identity;
- the built-in `runtime.no_errors` quality gate;
- explicit local baseline selection through `--baseline <run_id>`;
- deterministic `new`, `resolved`, `unchanged`, `regressed`, and `improved` Finding comparison where metric semantics are defined;
- query-count comparison payloads with baseline/current/delta/delta-percent values;
- explicit baseline-aware Finding gate scope through `--gate-scope all|new`;
- opt-in query regression fail gate through `--fail-on-query-regression`;
- local read-only MCP stdio access through `list_runs`, `get_run`, and `get_evidence`;
- exact Finding-to-Evidence dereferencing through stable `evidence_refs`;
- real upstream OTEL and real Rails interoperability gates.

Still deliberately deferred are configurable query thresholds, percentage/absolute tolerances, SQL literal scrubbing or AST normalization, N+1 classification, query-duration regression, allocation/memory regression, PostgreSQL execution-plan analysis, MCP Run execution, Evidence listing/search/pagination through MCP, stdout/stderr retrieval through MCP, automatic baseline selection, browser evidence, remote Run stores, MCP resources/prompts, and local-to-production correlation.

Relevant specifications live under `specs/`. The canonical Run JSON Schema is `schemas/run-v1.schema.json`, normalized Evidence uses `schemas/evidence-v1.schema.json`, the MCP Run read contract is `specs/mcp-v0.0.8.md`, the MCP Evidence read contract is `specs/mcp-evidence-v0.0.9.md`, and the v0.0.11 release boundary is `specs/v0.0.11.md`.
