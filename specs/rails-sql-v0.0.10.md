# RunWitness Rails SQL Query Comparison v0.0.10

Status: Contract candidate

## Purpose

v0.0.10 adds the first database-performance evidence path for Rails and the first metric-aware Run-to-Run Finding classification.

The intended flow is:

```text
ActiveSupport::Notifications sql.active_record
                 |
                 v
            rails.sql Evidence
                 |
                 v
      database.query_count Finding
                 |
        baseline comparison
                 |
       +---------+---------+
       |                   |
       v                   v
   regressed            improved
```

This slice is deliberately descriptive. It proves that a coding agent can see that the same logical SQL query executed more or fewer times after a code change. A database regression gate and configurable thresholds are deferred to a later contract.

## Rails SQL observation

When `--rails` is enabled, the Rails adapter MUST subscribe to the standard ActiveSupport notification:

```text
sql.active_record
```

The existing `Rails.error` subscription remains required and behaviorally unchanged.

For every relevant SQL notification, RunWitness records one normalized Evidence v1 record:

```text
source = rails
kind   = rails.sql
```

Required Evidence attributes are:

```text
sql.statement
sql.name
sql.cached
sql.duration_ms
```

`sql.statement` is normalized only by trimming leading/trailing whitespace and collapsing all internal whitespace runs to one ASCII space. v0.0.10 does not parse SQL, replace literals, or otherwise reinterpret the statement.

`sql.duration_ms` is the notification duration in milliseconds and MUST be non-negative.

The adapter MUST ignore a SQL notification when any of the following is true:

- `cached` is true;
- the SQL statement is empty after whitespace normalization;
- the notification name, compared case-insensitively, is `SCHEMA`;
- the notification name, compared case-insensitively, is `TRANSACTION`.

Ignoring these events prevents Rails metadata, transaction bookkeeping, and query-cache hits from inflating application query counts.

## Query-count Findings

Within one Run, relevant `rails.sql` Evidence is grouped by normalized `sql.statement`.

Each distinct statement produces exactly one Finding:

```text
kind     = database.query_count
rule_id  = rails.sql.query_count
sources  = [rails]
severity = info
```

`evidence_refs` contains every Run-local Evidence ID for that normalized statement, in observation order. The number of references is the observed query count for that pattern.

Finding identity MUST be deterministic across Runs. The stable identity inputs are exactly:

```text
finding.v1
kind = database.query_count
rule_id = rails.sql.query_count
normalized sql.statement
```

Run ID, Evidence IDs, timestamps, duration, query name, and query count MUST NOT participate in Finding identity.

The same normalized SQL statement therefore has the same `finding_id` in independent Runs even when it executes a different number of times.

## Metric-aware baseline comparison

The existing explicit baseline syntax remains unchanged:

```text
runwitness run --rails --baseline <run_id> -- <command>
```

For matched Findings where both baseline and current Finding have:

```text
kind    = database.query_count
rule_id = rails.sql.query_count
```

RunWitness compares query count as:

```text
count = length(evidence_refs)
```

The current Finding MUST receive:

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

Because a matched query-count Finding always existed at least once in the baseline, `delta_percent` is well-defined as:

```text
((current - baseline) / baseline) * 100
```

Metric classifications are exclusive:

- current count > baseline count -> `diff.regressed`;
- current count < baseline count -> `diff.improved`;
- current count == baseline count -> `diff.unchanged`.

A matched query-count Finding MUST appear in exactly one of those three lists.

When classified as `regressed`, the current Finding severity becomes `warning`. Otherwise it remains `info`.

Existing non-metric Finding comparison semantics remain unchanged:

- current-only Finding -> `new`;
- baseline-only Finding -> `resolved`;
- same non-metric Finding ID -> `unchanged`.

All diff arrays remain unique and lexicographically sorted.

## Verdict and gate boundary

v0.0.10 does NOT add a database quality gate.

A query-count regression by itself MUST NOT change a successful Run from `pass` to `warn` or `fail`. Existing process, runtime-error, Rails.error, and gate semantics remain authoritative.

This separation is intentional:

```text
Finding severity != gate action
```

v0.0.10 establishes trustworthy query evidence and metric comparison before a later release chooses default thresholds or policy.

## Interaction with `--gate-scope`

Because v0.0.10 adds no database gate, existing `--gate-scope all|new` behavior remains unchanged. Metric `regressed` and `improved` classifications are still recorded in `diff` and are not hidden by gate scope.

## Security and data boundary

SQL Evidence remains local under the existing Run store. v0.0.10 does not add a network listener, remote storage, or production collection.

The normalized SQL statement is stored as observed except for whitespace normalization. Applications that embed sensitive literals directly in SQL should treat local Run artifacts accordingly. Literal scrubbing is explicitly deferred rather than implemented heuristically in this release.

## Real Rails interoperability

The release gate MUST exercise SQL observation against real Rails 8.1 / ActiveSupport notifications on Ruby 3.4, in addition to preserving the existing real `Rails.error` interoperability test.

A database connection is not required for the interoperability smoke test: emitting a real `sql.active_record` notification through ActiveSupport is sufficient to verify the public subscription interface.

## Explicitly deferred

v0.0.10 does not add:

- a database regression gate;
- configurable query-count thresholds;
- SQL literal scrubbing or SQL AST normalization;
- N+1 classification;
- query-duration baseline regression;
- allocation or memory regression;
- PostgreSQL execution-plan analysis;
- automatic baseline selection;
- browser correlation;
- production correlation.

## Acceptance boundary

The locked black-box contract MUST prove:

1. real Ruby target execution under `--rails` can emit `sql.active_record` events that become valid `rails.sql` Evidence;
2. cached, `SCHEMA`, `TRANSACTION`, and blank-SQL events are ignored;
3. whitespace-equivalent SQL statements normalize to one query pattern;
4. one query pattern produces one stable `database.query_count` Finding with all matching Evidence references;
5. the same query pattern keeps the same `finding_id` across Runs even when its count changes;
6. a higher current count is classified only as `regressed` and records numeric comparison data;
7. a lower current count is classified only as `improved` and records numeric comparison data;
8. an equal current count remains `unchanged`;
9. query regression alone does not change an otherwise passing verdict or CLI exit code;
10. all unrelated pre-v0.0.10 contracts remain unchanged during implementation.
