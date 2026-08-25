# Rails Error Adapter v0.0.5 Contract

Status: Contract
Target: v0.0.5

## Purpose

The first Rails-specific RunWitness adapter observes the standard Rails error reporting interface so a successful test command cannot hide runtime errors reported through `Rails.error`.

Rails documents error subscribers as objects responding to:

```text
report(Exception, handled:, severity:, context:, source:)
```

RunWitness MUST consume that interface without replacing Rails' own error reporter.

## CLI

The adapter is explicitly enabled with:

```text
runwitness run --rails -- <command> [args...]
```

`--rails` MAY be combined with `--otel`.

When `--rails` is not requested, RunWitness MUST NOT inject Rails-specific instrumentation.

## Observation boundary

For an explicit Rails-observed Run, RunWitness MUST arrange for the launched Ruby process tree to load a RunWitness Rails bootstrap before application code executes.

The bootstrap MUST subscribe to `Rails.error` as soon as the Rails error reporter becomes available.

The bootstrap subscriber MUST NOT raise into the target application if RunWitness event serialization fails.

The adapter MUST record whether the Rails error subscriber was actually installed. If `--rails` was requested but no Rails error reporter became observable during the target execution, the Run MUST end with verdict `error`, not a false `pass`.

## Evidence

Each observed Rails error report becomes one normalized Evidence record with:

```text
source = rails
kind   = rails.error
```

The Evidence attributes MUST include at least:

```text
error.class
error.message
error.handled
error.severity
error.source
```

The exact Run-local Evidence record MUST remain auditable in `evidence.jsonl`.

For the initial contract, arbitrary Rails context is not required to participate in Finding identity.

## Finding

A handled Rails error report MUST produce:

```text
kind     = runtime.handled_error
severity = warning
rule_id  = rails.error.handled
source   = rails
```

The Finding MUST reference the exact `rails.error` Evidence record that caused it.

Finding identity MUST be deterministic and MUST NOT contain Run IDs, Evidence IDs, timestamps, or other Run-local identifiers.

The initial Rails fingerprint SHOULD use stable error semantics such as error class, error source, and a normalized application backtrace location when one is available.

## Gate and verdict

The existing built-in gate is reused:

```text
runtime.no_errors
```

A handled Rails error triggers that gate with action `fail`.

Therefore this is a valid RunWitness result:

```text
target process exit:      0
Rails.error handled:      1
runtime.handled_error:    1
runtime.no_errors:        triggered
RunWitness verdict:       fail
RunWitness CLI exit:      1
```

RunWitness MUST preserve the target process exit code as `0` in `run.json`.

## Clean Rails run

If the Rails subscriber is installed and no Rails errors are reported, the Rails adapter status is `ok` and it does not change an otherwise passing verdict.

## Required adapter failure

If `--rails` is explicitly requested but the adapter cannot prove that it subscribed to the Rails error reporter, RunWitness MUST report the Rails adapter as unavailable or errored and return verdict `error` with CLI exit `2`.

Instrumentation failure MUST NOT be represented as an application failure.

## Non-goals

v0.0.5 does not yet require:

- automatic Rails detection without `--rails`;
- Ruby allocation profiling;
- ActiveRecord N+1 detection;
- Sidekiq-specific instrumentation;
- arbitrary Rails context normalization guarantees;
- unhandled exception taxonomy beyond existing process/OTEL behavior;
- configurable Rails gate policy;
- baseline comparison.
