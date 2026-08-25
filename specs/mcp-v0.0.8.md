# RunWitness MCP Read Surface v0.0.8

Status: Contract candidate

## Purpose

v0.0.8 adds the first agent-native public interface over the existing RunWitness Run model.

The MCP surface is deliberately thin and read-only. It does not create Runs, execute target commands, reinterpret Findings, or introduce a second semantic model. It exposes the same local Run artifacts already consumed by the CLI and CI.

The architecture remains:

```text
coding agent
    |
    v
RunWitness MCP adapter
    |
    v
local Run store / canonical Run model
```

The CLI and Runner remain fully usable without MCP.

## Server command

The public command is:

```text
runwitness mcp
```

The server communicates over MCP stdio using JSON-RPC.

Starting the MCP server MUST NOT create a Run or mutate the local Run store.

## Working-directory boundary

The MCP server reads the Run store rooted at the process working directory:

```text
<cwd>/.runwitness/runs/
```

v0.0.8 does not add remote stores, repository discovery outside the current working directory, or a database/index.

## Tool surface

The first stable tool surface contains exactly two RunWitness tools:

```text
list_runs
get_run
```

The tools are read-only.

### `list_runs`

Input:

```json
{
  "limit": 20
}
```

`limit` is optional. The default is `20`. The accepted range is `1..100`.

The result contains Run summaries ordered newest first by `run.started_at`; ties are broken deterministically by descending `run_id`.

Each summary exposes only stable discovery fields:

```json
{
  "run_id": "019...",
  "started_at": "2026-08-25T00:00:00Z",
  "label": "checkout",
  "command": {
    "argv": ["bundle", "exec", "rspec"]
  },
  "verdict": {
    "status": "pass"
  },
  "summary": {
    "evidence_count": 0,
    "finding_count": 0
  }
}
```

`label` is omitted when the Run has no label.

An absent Run store is equivalent to an empty store and returns an empty Run list. Merely listing an empty store MUST NOT create `.runwitness/`.

Invalid `limit` input is a tool error and MUST NOT terminate the MCP server.

### `get_run`

Input:

```json
{
  "run_id": "019..."
}
```

`run_id` MUST be a UUIDv7 and addresses exactly:

```text
<cwd>/.runwitness/runs/<run_id>/run.json
```

The tool returns the canonical decoded `run.json` document without changing Run semantics or hiding Findings, gates, baseline metadata, or diff data.

A missing Run, malformed requested Run, mismatched embedded Run ID, or invalid Run ID is a tool error and MUST NOT terminate the MCP server.

Path traversal and arbitrary filesystem reads are therefore outside the contract.

## MCP result shape

Successful RunWitness tool calls MUST provide machine-readable structured content.

Conceptually:

```json
{
  "structuredContent": {
    "runs": []
  }
}
```

for `list_runs`, and:

```json
{
  "structuredContent": {
    "run": { "schema_version": 1 }
  }
}
```

for `get_run`.

A text fallback MAY also be present for generic MCP clients, but the contract does not require agents to parse human prose to recover the Run model.

## Reliability boundary

The MCP adapter MUST not silently manufacture semantic data.

- `get_run` returns the canonical stored Run document.
- `list_runs` derives only discovery summaries from canonical Run documents.
- tool errors remain MCP tool errors rather than RunWitness application verdicts.
- starting or using the server does not create a Run.
- a tool error does not terminate an otherwise healthy server.

For `list_runs`, malformed Run entries MUST produce a tool error rather than being silently omitted, because omission would make the local evidence index appear more complete than it is.

## Security boundary

v0.0.8 is a local stdio interface only.

It MUST NOT:

- bind a network listener;
- execute arbitrary commands through an MCP tool;
- expose arbitrary file reads;
- mutate Run artifacts;
- create or delete Runs.

## Version boundary

This feature contract does not itself advance the stable release version. The implementation remains on the current stable version until a separate v0.0.8 release contract is merged.

## Explicitly deferred

v0.0.8 does not yet add:

- a tool that starts a Run;
- raw Evidence pagination or search;
- stdout/stderr retrieval through MCP;
- automatic baseline selection;
- remote Run stores;
- MCP resources or prompts;
- CI annotations;
- browser evidence;
- ActiveRecord query regression or N+1 analysis;
- production correlation.

## Acceptance boundary

The locked black-box contract MUST prove:

1. `runwitness mcp` speaks MCP over stdio;
2. `tools/list` exposes `list_runs` and `get_run` as the public RunWitness tools;
3. starting and querying the server does not create a Run;
4. `list_runs` returns newest-first deterministic Run summaries;
5. the empty store is readable without filesystem mutation;
6. invalid list limits are tool errors without server termination;
7. `get_run` returns the exact canonical Run document for a valid Run ID;
8. invalid or missing Run IDs are tool errors without server termination;
9. a malformed Run cannot be silently omitted by `list_runs`;
10. all earlier v0.0.1-v0.0.7 contracts remain unchanged during implementation.
