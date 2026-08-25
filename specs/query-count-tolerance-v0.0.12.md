# Query Count Tolerance Contract v0.0.12

Status: Contract candidate

## Purpose

v0.0.12 adds an explicit absolute query-count tolerance to the opt-in Rails query regression gate introduced in v0.0.11.

The semantic comparison remains unchanged: any increase in a stable Rails query-count Finding is still classified as `regressed`. The tolerance changes only whether that regression is eligible to trigger the configured fail gate.

This preserves the separation:

```text
observed metric change -> Finding/diff semantics -> gate policy
```

and specifically:

```text
regressed != automatically failed
```

## CLI surface

The new option is:

```text
--query-count-tolerance <queries>
```

Example:

```bash
runwitness run \
  --rails \
  --baseline <run_id> \
  --fail-on-query-regression \
  --query-count-tolerance 1 \
  -- bundle exec rspec
```

The value is the maximum absolute increase in executions of the same stable `database.query_count` Finding that the requested query regression gate will tolerate.

The value must be a base-10 non-negative integer.

## Preconditions

`--query-count-tolerance` is meaningful only as a parameter of the explicit query regression fail policy.

Therefore:

1. it requires `--fail-on-query-regression`;
2. `--fail-on-query-regression` continues to require `--baseline`;
3. duplicate `--query-count-tolerance` options are rejected;
4. a missing value, empty value, negative value, decimal value, or non-numeric value is rejected;
5. invalid usage exits `2` before the Run boundary and creates no `.runwitness/` directory.

## Semantic comparison remains unchanged

Suppose the baseline and current Runs contain the same stable Finding:

```text
kind     = database.query_count
rule_id  = rails.sql.query_count
```

and the comparison is:

```text
baseline = 5
current  = 6
delta    = 1
```

The Finding remains:

```text
diff.regressed
severity = warning
```

regardless of the configured tolerance.

Tolerance does not rewrite `diff.regressed` to `unchanged` or `improved` and does not change the Finding's comparison payload.

## Gate eligibility

For an eligible query-count Finding:

```text
gate eligible when comparison.delta > tolerance
```

The comparison is strict greater-than.

Examples:

```text
baseline 5 -> current 6, tolerance 0 -> trigger (delta 1 > 0)
baseline 5 -> current 6, tolerance 1 -> pass    (delta 1 > 1 is false)
baseline 5 -> current 7, tolerance 1 -> trigger (delta 2 > 1)
baseline 5 -> current 7, tolerance 2 -> pass    (delta 2 > 2 is false)
```

The gate retains the v0.0.11 identity:

```text
rule_id = database.no_query_count_regressions
action  = fail
```

If at least one eligible regressed query-count Finding exceeds tolerance:

```text
outcome = triggered
verdict = fail
CLI exit = 1
```

If none exceeds tolerance:

```text
outcome = passed
```

and the gate does not manufacture a failure.

## Gate parameters

When `--query-count-tolerance` is explicitly supplied, the gate result records the effective policy in machine-readable form:

```json
{
  "rule_id": "database.no_query_count_regressions",
  "action": "fail",
  "outcome": "passed",
  "finding_ids": [],
  "parameters": {
    "max_delta": 1,
    "unit": "queries"
  }
}
```

`parameters.max_delta` is exactly the validated CLI integer.

`parameters.unit` is exactly `queries`.

The Run v1 schema is extended with an optional generic `gate_result.parameters` object whose values are scalar comparison values. Existing gate artifacts without `parameters` remain schema-valid.

## Backward compatibility

If `--query-count-tolerance` is omitted, v0.0.11 semantics are preserved exactly:

```text
effective tolerance = 0
```

but no new `parameters` field is required to be written for the omitted option.

Thus an explicitly requested strict query regression gate continues to fail any positive query-count delta.

If `--fail-on-query-regression` itself is omitted, query-count regression remains descriptive exactly as before and no database regression gate is added.

## Interaction with `--gate-scope new`

Tolerance does not change the v0.0.11 rule that metric regression policy is independent of Finding-newness policy.

A stable Finding can be:

```text
diff.new        = []
diff.regressed  = [finding_id]
```

and still trigger `database.no_query_count_regressions` if its delta exceeds tolerance, even when `--gate-scope new` is active.

## Multiple query-count Findings

When multiple query-count Findings are regressed:

1. each Finding is evaluated independently against the same absolute tolerance;
2. `gate.finding_ids` contains only regressed query-count Finding IDs whose `comparison.delta` exceeds tolerance;
3. the list is unique and lexicographically sorted;
4. regressed Findings within tolerance remain visible in `findings` and `diff.regressed` but are omitted from `gate.finding_ids`.

## Error and target semantics

The tolerance policy must not weaken existing invariants:

- target process exit codes are preserved;
- a non-zero target exit remains a Run failure independently of query policy;
- RunWitness/adapter/observation `error` verdicts are never downgraded to `fail`;
- a malformed comparison needed to evaluate an explicitly requested tolerance is a RunWitness policy error, not a fabricated pass.

## Explicitly deferred

v0.0.12 does not add:

- percentage tolerance;
- per-query-pattern tolerances;
- configurable policy files;
- N+1 classification;
- query-duration policy;
- SQL AST/literal normalization;
- automatic baselines;
- browser or production correlation.
