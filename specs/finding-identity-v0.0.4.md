# RunWitness v0.0.4 Finding Identity Contract

Status: Contract candidate

v0.0.4 closes a semantic gap in the first Findings implementation before additional framework adapters are added.

The canonical Runner specification already requires `finding_id` to be derived from a stable fingerprint of the logical problem rather than from a Run-local identifier. v0.0.3 introduced the first `runtime.error` Finding, so v0.0.4 makes that requirement executable.

## Product invariant

The same logical problem observed in different Runs MUST produce the same `finding_id`.

Run-local values MUST NOT affect Finding identity. In particular, the fingerprint MUST NOT depend on:

- `run_id`;
- `evidence_id`;
- trace ID;
- span ID;
- timestamps;
- observation order.

For the current `otel.span.error` rule, the stable identity inputs are the semantic rule plus the logical operation identity available from the normalized Evidence.

At minimum, the reference implementation SHOULD use:

- Finding kind `runtime.error`;
- rule ID `otel.span.error`;
- `service.name` when available;
- `span.name` when available.

Additional stable semantic attributes may be added later only through an explicit contract change if they are required to distinguish genuinely different logical problems.

## Required behavior

Given two independent Runs that observe the same error span semantics but have different Run IDs, trace IDs, span IDs, and Evidence IDs:

1. both Runs produce one `runtime.error` Finding;
2. the two Findings have exactly the same `finding_id`;
3. each Finding still references its own Run-local Evidence record through `evidence_refs`;
4. the quality gate in each Run references that stable Finding ID;
5. each Run still has its own UUIDv7 `run_id` and Evidence IDs.

Given a different logical operation, such as a different `span.name`, RunWitness MUST be able to produce a different Finding identity.

## Why this is required now

Stable Finding identity is the prerequisite for future baseline diffing:

```text
new
unchanged
resolved
regressed
improved
```

Without stable identity, the same runtime problem would appear new in every Run and comparison semantics would be invalid.

## Compatibility

This slice does not change:

- `run.json` schema version;
- Evidence v1 schema;
- CLI syntax;
- existing OTEL Evidence kinds;
- runtime error gate semantics;
- process exit-code preservation;
- verdict exit-code mapping.

Only the generation semantics of `finding_id` are tightened to match the already locked Runner specification.
