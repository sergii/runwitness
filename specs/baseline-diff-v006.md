# RunWitness Baseline Diff v0.0.6 Contract

Status: Acceptance contract

## Purpose

v0.0.6 introduces the first explicit Run-to-Run comparison surface.

The goal is to answer a deterministic question over semantic Findings:

> Which logical Findings are new, resolved, or unchanged compared with an explicitly selected prior Run?

This slice deliberately compares stable Finding identity only. Metric-aware regression and improvement semantics are deferred until a later contract defines comparable values and thresholds.

## CLI

The explicit baseline form is:

```text
runwitness run --baseline <run_id> -- <command> [args...]
```

`--baseline` is a RunWitness option and MUST NOT be forwarded to the target command.

Baseline selection remains explicit only. Automatic selectors such as `last`, `main`, `gold`, branch-aware baselines, or remote baseline lookup are out of scope.

The supplied baseline ID MUST identify an existing local Run in the current Run store:

```text
.runwitness/runs/<run_id>/run.json
```

A missing, unreadable, or malformed requested baseline is a RunWitness error. The target command MUST NOT execute and a new Run MUST NOT be created.

## Run artifact

When a baseline is requested and successfully resolved, the current `run.json` MUST include:

```json
{
  "baseline": {
    "run_id": "<baseline-run-id>"
  }
}
```

A Run executed without `--baseline` MUST preserve existing behavior and MUST NOT be required to emit `baseline` or `diff`.

## Finding identity comparison

Comparison is source-agnostic and uses only stable `finding_id` identity.

Let:

```text
B = unique Finding IDs in the baseline Run
C = unique Finding IDs in the current Run
```

The diff is:

```text
new       = C - B
resolved  = B - C
unchanged = B ∩ C
```

Each list MUST contain unique Finding IDs in deterministic lexicographic order.

The current Run MUST emit:

```json
{
  "diff": {
    "new": [],
    "resolved": [],
    "unchanged": [],
    "regressed": [],
    "improved": []
  }
}
```

For v0.0.6, `regressed` and `improved` MUST be empty. They are reserved for a later contract that defines comparable values and thresholds.

Baseline Findings MUST NOT be copied into the current Run's `findings` array merely because they are resolved. `diff.resolved` carries the stable IDs of baseline-only logical Findings.

## Verdict semantics

Baseline comparison is descriptive in v0.0.6. It MUST NOT weaken, suppress, or rewrite existing quality gates.

Examples:

- an unchanged current `runtime.error` still triggers the existing `runtime.no_errors` gate;
- a resolved baseline error does not trigger a current runtime gate when the current Run contains no error Finding;
- a newly introduced current error behaves exactly as an absolute current error already behaves.

Baseline-aware policies such as "fail only on new Findings" are deferred.

## Reliable comparison boundary

A `diff` is meaningful only when the current Run completed observation reliably.

If the current Run ends with verdict `error` because RunWitness or a required adapter could not complete observation, RunWitness MUST NOT claim Findings were resolved. The current Run MAY retain its selected `baseline`, but `diff` MUST be omitted.

Target process failure alone is not a RunWitness observation error. A reliably observed Run with verdict `fail` MAY still produce a baseline diff.

## Compatibility

This contract does not change:

- Run schema version 1;
- Evidence schema version 1;
- stable Finding identity rules;
- existing Finding or gate semantics;
- target process exit-code preservation;
- CLI exit mapping;
- OpenTelemetry or Rails adapter contracts.

The existing Run v1 schema already reserves optional `baseline` and `diff` fields for this surface.

## Deferred

The following are explicitly out of scope for v0.0.6:

- automatic baseline selection;
- baseline lookup outside the current local Run store;
- `regressed` / `improved` metric semantics;
- percentage or threshold comparison;
- baseline-aware quality-gate policy;
- Git-aware baseline selection;
- browser, database, or production baseline correlation;
- CLI human-readable diff rendering beyond the machine-readable Run artifact.

## Acceptance gates

The executable contract MUST prove at least:

1. the same logical Finding in baseline and current Runs is classified `unchanged`;
2. a current-only Finding is classified `new`;
3. a baseline-only Finding is classified `resolved`;
4. replacing one logical Finding with another produces one `resolved` and one `new` ID;
5. `regressed` and `improved` are empty in this slice;
6. baseline selection is recorded in `run.json` and is not forwarded to the target;
7. a missing requested baseline prevents target execution and Run creation;
8. existing current-run gates and verdict semantics remain unchanged.
