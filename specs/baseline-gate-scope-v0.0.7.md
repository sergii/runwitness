# Baseline-aware gate scope v0.0.7

Status: Contract candidate

## Purpose

RunWitness v0.0.6 can explain whether a Finding is `new`, `resolved`, or `unchanged`, but quality gates remain absolute. That means an unchanged problem inherited from a baseline still fails the current Run.

The next contract slice makes baseline comparison actionable without changing the default behavior.

## CLI surface

A Run with an explicit baseline MAY opt into baseline-aware Finding gates:

```text
runwitness run --baseline <run_id> --gate-scope new -- <command> [args...]
```

`--gate-scope` accepts:

```text
all
new
```

The default remains `all` for backward compatibility.

`--gate-scope` MUST be rejected before the Run boundary when no `--baseline` is supplied. Unknown or empty scope values are usage errors and MUST NOT execute the target or create a Run.

## Artifact contract

When `--gate-scope` is explicitly supplied, the selected policy MUST be recorded with the baseline metadata:

```json
{
  "baseline": {
    "run_id": "<baseline-run-id>",
    "finding_gate_scope": "new"
  }
}
```

A v0.0.6-style baseline object containing only `run_id` remains valid and means the existing absolute `all` behavior.

## `all` semantics

`all` preserves the v0.0.6 behavior exactly.

Current Findings participate in existing quality gates whether they are `new` or `unchanged` relative to the baseline.

## `new` semantics

`new` changes only Finding-based quality-gate evaluation after a reliable baseline diff exists.

For every existing Finding-based gate:

```text
eligible finding_ids = original finding_ids ∩ diff.new
```

The current Finding set and the semantic diff MUST NOT be rewritten or hidden.

For the built-in `runtime.no_errors` gate:

- if one or more eligible new Findings remain, the gate stays `triggered` and contains only those new Finding IDs;
- if no eligible new Findings remain, the gate becomes `passed` with an empty `finding_ids` list;
- unchanged Findings remain visible in `findings` and `diff.unchanged` but do not fail the scoped gate;
- resolved Findings never trigger a current gate.

## Verdict semantics

Gate scoping MUST NOT weaken non-Finding failures.

- target process failure remains verdict `fail`;
- RunWitness or required-adapter failure remains verdict `error`;
- a current Run with only unchanged Finding-based failures MAY become `pass` under `--gate-scope new`;
- a current Run containing a new fail-gated Finding remains `fail`;
- default baseline runs without `--gate-scope new` retain absolute gate behavior.

The CLI mapping remains:

```text
0 = pass or warn
1 = fail
2 = RunWitness error
```

## Reliability boundary

`new` scope MUST be applied only after the finalized current Finding set and a reliable v0.0.6 diff exist.

If the current Run has verdict `error`, RunWitness MUST preserve `error` and MUST NOT turn it into a pass through gate scoping.

## Why this slice exists

The intended patch-validation workflow becomes:

```text
baseline has known problem
        +
current has same problem
        ↓
unchanged
        ↓
--gate-scope new
        ↓
no new regression -> PASS
```

while:

```text
baseline clean or different
        +
current introduces problem
        ↓
new
        ↓
runtime.no_errors -> FAIL
```

This lets RunWitness answer the practical agent question:

> Did this code change introduce a new behavioral problem?

without pretending that pre-existing problems disappeared.

## Explicitly deferred

This slice does not add:

- automatic baseline selection;
- metric-aware `regressed` or `improved` classification;
- configurable per-rule policies;
- severity-based policy expressions;
- thresholds;
- warning-only new-Finding policy;
- ActiveRecord/N+1 analysis;
- browser or PostgreSQL adapters.

## Acceptance contract

The locked black-box contract MUST prove at least:

1. the same logical Finding is `unchanged`, remains in the current Finding set, but no longer fails `runtime.no_errors` under `--gate-scope new`;
2. a current-only Finding is `new` and still fails `runtime.no_errors` under `--gate-scope new`;
3. the selected scope is recorded in baseline metadata;
4. `--gate-scope new` without a baseline fails before target execution and Run creation;
5. default v0.0.6 baseline gate behavior remains unchanged by this feature.
