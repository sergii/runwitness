# RunWitness Acceptance Contract

Status: cumulative locked black-box contract

These tests define externally observable RunWitness behavior.

They are intentionally black-box and implementation-language independent. The harness invokes a RunWitness binary, speaks public protocols where applicable, and inspects public artifacts only. It does not import Go implementation packages.

## Stable contract history

The acceptance suite currently covers the cumulative public surface introduced through the stable releases and the next reviewed contract slice:

```text
v0.0.1  Runner, CLI, process/Git boundary
v0.0.2  OpenTelemetry Evidence boundary
v0.0.3  runtime Findings and deterministic quality gate
v0.0.4  stable cross-Run Finding identity
v0.0.5  Rails.error runtime observation
v0.0.6  explicit baseline Finding diff
v0.0.7  baseline-aware Finding gate scope
v0.0.8  first read-only MCP Run surface (contract candidate)
```

The first Runner contract still includes UUIDv7 Run identity, exact target argv preservation, stdout/stderr capture, `RUNWITNESS_RUN_ID` propagation, deterministic exit semantics, non-Git behavior, and Git before/after state without RunWitness self-noise.

Later contract files extend that same public boundary rather than replacing it.

## v0.0.8 MCP contract candidate

The next contract adds a local stdio MCP adapter:

```text
runwitness mcp
```

Its first read-only tools are:

```text
list_runs
get_run
```

The MCP contract proves that agents can discover and retrieve the same canonical local Run model without creating Runs, executing arbitrary commands, mutating artifacts, or receiving a second semantic representation. See `specs/mcp-v0.0.8.md` and `test_mcp_v008.py`.

## Running the contract

The default binary path is:

```text
./bin/runwitness
```

A different conforming implementation can be tested by setting:

```text
RUNWITNESS_BIN=/absolute/path/to/runwitness
```

Install the test-only dependency and execute the cumulative suite:

```text
python3 -m pip install -r tests/acceptance/requirements.txt
python3 -m unittest discover -s tests/acceptance -p 'test_*.py' -v
```

A newly added contract slice is expected to be RED until its implementation exists.

## Lock rule

Once a contract PR is accepted, the corresponding acceptance behavior is locked during implementation. Contract tests must not be changed merely to make an implementation pass.

If the contract itself is wrong, follow the explicit contract-change procedure in `CONTRIBUTING.md` before changing it.
