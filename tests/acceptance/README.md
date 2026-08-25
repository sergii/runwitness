# RunWitness v0.0.1 Acceptance Contract

Status: locked for the first Runner vertical slice

These tests define externally observable behavior for the first RunWitness Runner implementation.

They are intentionally black-box and implementation-language independent. The harness invokes a RunWitness binary and inspects public artifacts only.

## Contract under test

The first vertical slice is:

```text
runwitness run -- <command>
```

The acceptance suite locks the following behavior:

1. A successful target command produces exactly one Run directory under `.runwitness/runs/<run_id>/`.
2. `run.json`, `evidence.jsonl`, `stdout.log`, and `stderr.log` are created.
3. `run.json` validates against `schemas/run-v1.schema.json`.
4. The Run directory name equals `run.run_id`, and the Run ID is UUIDv7.
5. The original target argument vector is preserved exactly.
6. stdout and stderr are captured separately.
7. `RUNWITNESS_RUN_ID` is propagated into the target process.
8. A successful target records target exit code `0`, verdict `pass`, and RunWitness exits `0`.
9. A target that exits `7` records target exit code `7`, verdict `fail`, and RunWitness exits `1`.
10. Running outside a git repository remains valid and omits repository/git metadata.
11. Inside a git repository, RunWitness records repository root plus before/after HEAD, dirty state, and diff hashes.
12. If the target mutates the dirty working tree, before and after diff hashes differ.

This slice deliberately does not lock OpenTelemetry, Rails, database, browser, baseline, MCP, or long-term storage behavior yet.

## Running the contract

The default binary path is:

```text
./bin/runwitness
```

A different implementation can be tested by setting:

```text
RUNWITNESS_BIN=/absolute/path/to/runwitness
```

Install the test-only dependency and execute:

```text
python3 -m pip install -r tests/acceptance/requirements.txt
python3 tests/acceptance/test_runner_v001.py
```

The suite is expected to fail until a conforming Runner implementation exists.

## Lock rule

During implementation of this vertical slice, files under `tests/acceptance/` are contract files and must not be changed merely to make the implementation pass.

If the contract itself is wrong, follow the explicit contract-change procedure in `CONTRIBUTING.md` before changing these tests.
