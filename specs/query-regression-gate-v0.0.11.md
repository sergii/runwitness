# Query Regression Gate Contract for v0.0.11

Status: Contract

v0.0.11 adds the first explicit policy over the metric-aware query-count comparison introduced in v0.0.10.

The new policy is opt-in. RunWitness continues to separate observed facts, Finding severity, comparison classification, and gate action.

## CLI surface

The new option is:

```text
--fail-on-query-regression
```

Example:

```bash
runwitness run \
  --rails \
  --baseline <run_id> \
  --fail-on-query-regression \
  -- bundle exec rspec
```

The option is valid only before the `--` target separator and is consumed by RunWitness rather than forwarded to the target command.

`--fail-on-query-regression` requires an explicit `--baseline <run_id>`. Supplying the option without a baseline is a usage error before the Run boundary: RunWitness exits `2`, does not execute the target, and does not create `.runwitness/`.

The policy is not Rails-specific at the CLI layer. Its initial v0.0.11 implementation applies to the stable query-count Finding semantics currently produced by Rails:

```text
kind     = database.query_count
rule_id  = rails.sql.query_count
```

## Gate result

When the option is requested, the finalized Run records exactly one gate with:

```text
rule_id = database.no_query_count_regressions
action  = fail
```

If one or more eligible query-count Findings are classified in `diff.regressed`, the gate records:

```text
outcome = triggered
finding_ids = sorted regressed query-count Finding IDs
```

An otherwise passing Run becomes:

```text
verdict.status = fail
RunWitness CLI exit = 1
```

The target process exit code remains unchanged. A target that exits `0` still records `run.process.exit_code = 0`.

If no eligible query-count Finding is regressed, the requested gate remains visible and records:

```text
outcome = passed
finding_ids = []
```

The gate does not trigger for query-count Findings classified as `unchanged`, `improved`, `new`, or `resolved`.

## Opt-in compatibility

Without `--fail-on-query-regression`, v0.0.10 behavior is preserved exactly:

- a query-count regression remains a `warning` Finding;
- it remains visible in `diff.regressed`;
- no database gate is added;
- an otherwise passing Run remains `pass` with CLI exit `0`.

This preserves the invariant:

```text
Finding severity != gate action
```

A warning query-count Finding can therefore either be descriptive or fail a Run depending on explicit policy.

## Interaction with `--gate-scope`

`--gate-scope all|new` controls existing gates that operate over the current Finding set.

The query-regression gate is a comparison gate. Its eligibility is defined by `diff.regressed`, not by `diff.new`, so it is not filtered by `--gate-scope new`.

Therefore:

```text
baseline query count = 1
current query count  = 3
classification       = regressed
--gate-scope new
--fail-on-query-regression
                      -> database.no_query_count_regressions triggered
                      -> verdict fail
```

This avoids treating a worsening stable Finding as if it were unchanged merely because its `finding_id` already existed in the baseline.

## Error precedence

Existing failure semantics remain authoritative:

- target process failure remains `fail`;
- RunWitness or required-adapter failure remains `error` and CLI exit `2`;
- baseline resolution failure remains a usage/data error before current target execution;
- the query-regression gate never converts an `error` verdict into `fail`.

## Explicitly deferred

v0.0.11 does not add:

- a default database gate when the option is absent;
- percentage or absolute query-count thresholds;
- per-query allowlists or suppressions;
- configuration-file policy;
- N+1 classification;
- query-duration regression gates;
- SQL AST or literal normalization;
- automatic baseline selection.

Those require separate executable contracts.
