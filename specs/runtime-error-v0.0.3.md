# RunWitness Runtime Error Finding v0.0.3 Contract

Status: Contract

This slice introduces the first semantic Finding and quality gate derived from normalized runtime Evidence.

The goal is to prove the core RunWitness product loop:

```text
execution -> Evidence -> Finding -> Gate -> Verdict
```

A target process can exit successfully while runtime telemetry still proves that an application error occurred. RunWitness must be able to fail that Run without confusing application process status with the RunWitness verdict.

## Scope

This contract applies only when OpenTelemetry observation is explicitly enabled with:

```text
runwitness run --otel -- <command> [args...]
```

The first built-in rule handles OpenTelemetry span errors only.

## Finding rule

For every normalized `otel.span` Evidence record whose `attributes["span.status"]` equals `ERROR`, case-insensitively, RunWitness MUST create one Finding with:

- `kind`: `runtime.error`
- `severity`: `error`
- `rule_id`: `otel.span.error`
- `sources`: exactly `["otel"]`
- `evidence_refs`: exactly the Evidence ID of the triggering span
- `summary`: a non-empty human-readable description that includes the span name when `span.name` is available
- `finding_id`: a valid stable RunWitness Finding ID matching the Run v1 schema

The rule consumes normalized Evidence only. It MUST NOT depend directly on `otlp-mcp` wire payloads.

Non-error spans MUST NOT create this Finding.

## Gate

When at least one `otel.span.error` Finding exists, RunWitness MUST add a gate result with:

- `rule_id`: `runtime.no_errors`
- `action`: `fail`
- `outcome`: `triggered`
- `finding_ids`: all Finding IDs produced by this rule for the Run

The gate changes the Run verdict to `fail` unless a higher-priority RunWitness instrumentation/runner error already requires verdict `error`.

## Process status versus verdict

A runtime error Finding does not rewrite target process status.

If the target exits `0` and an `otel.span.error` Finding is produced:

- `run.process.exit_code` remains `0`
- `verdict.status` becomes `fail`
- the RunWitness CLI exits `1`

This distinction is intentional: the command succeeded as a process, but the observed runtime behavior failed the quality gate.

If the target itself exits non-zero, the existing target failure semantics remain intact and the verdict remains `fail`.

## Summary counts

`summary.evidence_count` remains the number of Evidence records written to `evidence.jsonl`.

`summary.finding_count` MUST equal the number of Findings written to `run.json`.

## Compatibility

This slice MUST preserve all v0.0.1 and v0.0.2 behavior outside the new semantic rule.

In particular:

- Runs without `--otel` are unchanged.
- OTEL Runs with no error span remain compatible with v0.0.2.
- OTEL adapter failures still produce verdict `error`, not `fail`.
- Existing Evidence v1 records remain unchanged.
- No baseline or before/after comparison is introduced.

## Explicitly deferred

This contract does not add:

- error-log Findings;
- exception-event Findings;
- N+1 or query-performance Findings;
- warning-only rules;
- user-configurable gates;
- baselines or regression comparison;
- Ruby/Rails-specific rules;
- browser or PostgreSQL Findings.

Those should be separate contract slices.

## Acceptance case

The locked acceptance test MUST prove the important case where:

1. the target process exits `0`;
2. OTEL Evidence contains one span with status `ERROR`;
3. Evidence remains schema-valid and is written normally;
4. exactly one `runtime.error` Finding references that Evidence record;
5. `runtime.no_errors` is triggered with action `fail`;
6. the Run verdict is `fail`;
7. RunWitness exits `1` while preserving target exit code `0`.
