# RunWitness Run Schema v1

Status: Draft
Target: v0.0.1
Schema file: `schemas/run-v1.schema.json`

## 1. Purpose

`run.json` is the canonical machine-readable result of one RunWitness execution.

The contract is intentionally independent of CLI rendering, MCP, CI annotations, and any future UI. Every interface consumes the same Run model.

The v1 schema is designed to answer four questions:

1. What code state and command were executed?
2. What evidence and findings were produced?
3. What changed relative to an explicit baseline, if any?
4. Why did the Run receive its final verdict?

## 2. Canonical artifact layout

```text
.runwitness/
  runs/
    <run_id>/
      run.json
      evidence.jsonl
      stdout.log
      stderr.log
```

`run.json` contains normalized metadata, findings, diff information, adapter status, and the final verdict.

`evidence.jsonl` contains append-friendly Evidence records and may preserve source-specific payloads that should not inflate the canonical Run document.

## 3. Example `run.json`

```json
{
  "schema_version": 1,
  "run": {
    "run_id": "01993a2d-5a6b-7c31-8f22-9a2d73b90d10",
    "label": "orders request specs",
    "environment": "test",
    "started_at": "2026-08-25T00:10:00Z",
    "finished_at": "2026-08-25T00:10:04.231Z",
    "duration_ms": 4231,
    "runner_version": "0.0.1",
    "working_directory": "/workspace/app",
    "command": {
      "argv": ["bundle", "exec", "rspec", "spec/requests/orders_spec.rb"],
      "display": "bundle exec rspec spec/requests/orders_spec.rb"
    },
    "process": {
      "exit_code": 0
    },
    "repository": {
      "root": "/workspace/app",
      "identity": "github.com/example/app"
    },
    "git": {
      "branch": "fix/orders",
      "before": {
        "head_sha": "0123456789abcdef0123456789abcdef01234567",
        "dirty": true,
        "diff_hash": "sha256:8bca..."
      },
      "after": {
        "head_sha": "0123456789abcdef0123456789abcdef01234567",
        "dirty": true,
        "diff_hash": "sha256:8bca..."
      }
    }
  },
  "baseline": {
    "run_id": "01993a10-e26c-71b7-bb42-51ecaa770e20"
  },
  "adapters": [
    {
      "name": "otel",
      "status": "ok",
      "version": "1"
    },
    {
      "name": "ruby",
      "status": "ok",
      "version": "1"
    }
  ],
  "summary": {
    "evidence_count": 153,
    "finding_count": 1
  },
  "findings": [
    {
      "finding_id": "rwf_7fb31b0c",
      "kind": "ruby.allocations_regression",
      "severity": "warning",
      "rule_id": "ruby.allocations.delta_percent",
      "sources": ["ruby"],
      "summary": "Ruby object allocations increased by 75.8%",
      "evidence_refs": ["ev_01993a2d_00041", "ev_01993a2d_00118"],
      "confidence": 1.0,
      "comparison": {
        "baseline": 8421,
        "current": 14802,
        "delta": 6381,
        "delta_percent": 75.78,
        "unit": "objects"
      }
    }
  ],
  "diff": {
    "new": ["rwf_7fb31b0c"],
    "resolved": [],
    "unchanged": [],
    "regressed": [],
    "improved": []
  },
  "verdict": {
    "status": "warn",
    "gates": [
      {
        "rule_id": "ruby.allocations.delta_percent",
        "action": "warn",
        "outcome": "triggered",
        "finding_ids": ["rwf_7fb31b0c"],
        "message": "Allocation regression exceeded 50%"
      }
    ]
  }
}
```

## 4. Run identity

`run.run_id` MUST be a UUIDv7.

The Run ID identifies an execution, not a source-code revision. Two Runs against the same commit and dirty diff remain distinct executions and therefore receive different Run IDs.

The combination of repository identity, git HEAD SHA, dirty state, and diff hash describes the code state that was executed.

## 5. Command model

The original argument vector is canonical:

```json
{
  "argv": ["bundle", "exec", "rspec", "spec/models/user_spec.rb"]
}
```

A human-oriented `display` string MAY also be stored, but implementations MUST NOT attempt to reconstruct executable arguments by parsing `display`.

## 6. Git state

Git state is recorded both before and after command execution because target commands can mutate the working tree.

Each state contains:

- `head_sha`
- `dirty`
- `diff_hash` when a deterministic working-tree diff can be produced

`diff_hash` SHOULD include a hash algorithm prefix, for example:

```text
sha256:<digest>
```

The exact canonicalization algorithm for the hashed diff will be specified separately before implementation is considered stable.

## 7. Adapter status

Adapters report whether their evidence is trustworthy for the Run.

Initial statuses:

- `ok`
- `unavailable`
- `error`
- `partial`

An adapter failure is not automatically an application failure. Required-adapter policy may cause the overall Run verdict to become `error` if RunWitness cannot make a reliable judgment.

## 8. Evidence contract

Each line in `evidence.jsonl` is one Evidence record.

Minimum conceptual shape:

```json
{
  "evidence_id": "ev_01993a2d_00001",
  "run_id": "01993a2d-5a6b-7c31-8f22-9a2d73b90d10",
  "source": "rails",
  "kind": "exception",
  "observed_at": "2026-08-25T00:10:02.012Z",
  "attributes": {
    "exception.class": "RuntimeError"
  },
  "payload": {}
}
```

Evidence is factual. It MUST NOT encode policy decisions such as `this should fail CI`.

`evidence_id` needs to be unique inside the Run. Its exact generation algorithm is implementation-specific in v0.0.1.

## 9. Finding identity

`finding_id` is a deterministic fingerprint of a logical problem.

It is not a UUID and MUST remain stable when the same logical finding appears in comparable Runs.

A rule owns the fingerprint inputs for its finding kind.

For example, an exception finding could fingerprint:

```text
runtime.unhandled_exception
OrdersController#create
RuntimeError
```

A database finding might instead use a normalized query fingerprint and operation identity.

Fingerprint algorithms MUST NOT rely on volatile values such as timestamps, random IDs, absolute temporary paths, or raw memory addresses.

## 10. Severity versus gate action

Finding severity describes the problem:

```text
info
warning
error
critical
```

Gate action describes project policy:

```text
ignore
warn
fail
```

The two are intentionally independent.

For example:

```text
finding severity: warning
project gate action: fail
```

is valid.

## 11. Baseline and diff

`baseline` is absent when no comparison Run was requested.

For v0.0.1 a baseline is selected only by explicit Run ID.

When a baseline exists, `diff` classifies stable finding fingerprints into:

- `new`
- `resolved`
- `unchanged`
- `regressed`
- `improved`

A finding may also contain a typed `comparison` object for measurable values such as duration, allocation count, query count, or memory.

The diff arrays contain finding IDs, not duplicated Finding objects.

## 12. Verdict

`verdict.status` is one of:

```text
pass
warn
fail
error
```

`verdict.gates` records the deterministic reasons behind the status so an agent or developer can audit the decision without re-running policy logic.

Gate outcomes are initially:

```text
passed
triggered
skipped
error
```

The overall verdict is computed by Runner policy, not by an LLM.

## 13. Schema evolution

`schema_version` is an integer and starts at `1`.

Consumers MUST reject unsupported schema versions rather than silently interpreting them as another version.

Backward-compatible additions may add optional fields within a schema version during early development, but once v1 is declared stable, incompatible semantic changes require a new schema version.

## 14. Deliberately deferred fields

The v1 draft does not yet standardize:

- agent identity/session metadata
- issue/task references
- HTTP/W3C Baggage correlation metadata
- production deployment identity
- browser/backend correlation IDs
- CI provider-specific metadata
- long-term storage/indexing metadata

These should be added only after a concrete workflow requires them.
