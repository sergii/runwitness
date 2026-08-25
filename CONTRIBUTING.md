# Contributing to RunWitness

RunWitness treats observable behavior as a contract.

The project therefore uses contract-first TDD for every externally visible vertical slice.

## Core rule

> No implementation before executable contract. No contract changes to make an implementation pass.

## Two classes of tests

### Acceptance contract tests

Acceptance tests describe externally observable RunWitness behavior.

They MUST:

- invoke RunWitness through a public interface, initially the CLI;
- inspect public artifacts such as `run.json`, `evidence.jsonl`, `stdout.log`, and `stderr.log`;
- assert semantics rather than implementation details;
- avoid importing internal Go packages or implementation-specific code;
- be reusable against another conforming Runner implementation where practical.

Examples of contract behavior include:

- `runwitness run -- echo hello` executes the target command;
- stdout and stderr are captured correctly;
- `run.json` is produced and conforms to the versioned schema;
- `run_id` is a UUIDv7;
- git state is captured when available;
- target exit status is represented correctly;
- RunWitness maps outcomes to the documented `pass`, `warn`, `fail`, and `error` verdict semantics;
- CLI exit codes follow the public contract.

Once a vertical slice contract is accepted, its acceptance tests are locked for the implementation phase.

### Internal implementation tests

Internal tests exercise implementation details such as Go packages, state machines, storage helpers, git inspection, or adapter internals.

They MAY change during implementation and refactoring as long as the acceptance contract remains satisfied.

## Contract-first workflow

For each externally visible vertical slice:

1. Write or update the specification.
2. Write black-box acceptance tests before production implementation.
3. Review the tests as the executable product contract.
4. Freeze the accepted contract for the implementation phase.
5. Implement until the locked acceptance tests pass.
6. Add or refactor internal tests as needed.
7. Merge only when the implementation satisfies the accepted contract.

A contract PR may be stacked ahead of its implementation PR when the contract cannot be green without a Runner binary. In that case, the contract must still be reviewed and frozen before implementation work starts, and the PRs should land in contract-then-implementation order.

## Changing an accepted contract

A failing acceptance test is not permission to rewrite the test.

If the team concludes that an accepted contract is wrong, the contract change must be explicit:

1. document why the existing specification is incorrect or incomplete;
2. update the specification;
3. update the acceptance test in a dedicated contract change;
4. review and accept the new contract;
5. only then update the implementation.

Contract changes must never be hidden inside an implementation fix.

## Implementation-language independence

Go is the reference Runner implementation for v0.0.1, but Go is not part of the RunWitness protocol.

Schemas, CLI semantics, artifact formats, adapter contracts, Findings, and Verdicts must remain language-independent.

A future Rust or other implementation should be able to run the same black-box acceptance suite and produce compatible versioned artifacts.

## Test quality

Prefer semantic assertions over brittle snapshots.

For example, test that:

- a Run ID is a valid UUIDv7;
- timestamps are valid and ordered;
- `duration_ms` is non-negative;
- `argv` preserves the original argument vector;
- the target exit code is preserved in `run.json`;
- the final verdict and RunWitness process exit code follow the specification;
- generated JSON validates against the declared schema.

Do not snapshot entire Run documents when values such as UUIDs, timestamps, absolute temporary paths, or durations are intentionally variable.

## Vertical slices

Prefer the smallest end-to-end slice that proves a product behavior.

The first planned slice is intentionally small:

```text
runwitness run -- echo hello
```

It should prove the complete spine:

```text
CLI -> Run identity -> git/process state -> target execution -> stdout/stderr -> run.json -> verdict -> RunWitness exit code
```

OpenTelemetry, Ruby/Rails, pgbot, browser evidence, MCP, and other adapters should be added only after this spine is working under the locked acceptance contract.
