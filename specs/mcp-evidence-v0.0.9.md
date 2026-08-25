# RunWitness MCP Evidence Read Surface v0.0.9

Status: Contract candidate

## Purpose

v0.0.9 extends the local read-only MCP surface so a coding agent can follow a Finding's `evidence_refs` to the exact normalized Evidence record that produced it.

The intended traversal is:

```text
get_run
   |
   v
finding.evidence_refs[]
   |
   v
get_evidence(run_id, evidence_id)
   |
   v
exact normalized Evidence v1 record
```

This is a read-path extension only. It does not give MCP execution authority and it does not introduce a second Evidence model.

## Tool surface evolution

The public RunWitness MCP tool surface becomes exactly:

```text
list_runs
get_run
get_evidence
```

The v0.0.8 tools remain behaviorally compatible. The previous v0.0.8 assertion that the surface contained exactly two tools is intentionally evolved by this contract: those two tools remain required, and v0.0.9 adds exactly one new tool.

## `get_evidence`

Input:

```json
{
  "run_id": "019...",
  "evidence_id": "ev_..."
}
```

`run_id` MUST be a UUIDv7. `evidence_id` MUST satisfy the stable Evidence v1 identifier grammar:

```text
^ev_[A-Za-z0-9._-]+$
```

The tool resolves only the local Run store rooted at the MCP server working directory:

```text
<cwd>/.runwitness/runs/<run_id>/evidence.jsonl
```

The requested Run itself MUST be a valid canonical local Run according to the existing `get_run` boundary before Evidence is returned.

## Result shape

A successful call returns the exact decoded normalized Evidence object through machine-readable structured content:

```json
{
  "structuredContent": {
    "evidence": {
      "schema_version": 1,
      "evidence_id": "ev_...",
      "run_id": "019...",
      "source": "otel",
      "kind": "otel.span",
      "observed_at": "2026-08-25T00:00:00Z",
      "attributes": {},
      "payload": {}
    }
  }
}
```

The MCP adapter MUST NOT reinterpret, summarize, enrich, or omit fields from the stored Evidence record.

## Evidence-store integrity boundary

`evidence.jsonl` is treated as canonical Run-local Evidence data. `get_evidence` MUST report a tool error when:

- the requested Run does not exist or is malformed;
- `evidence.jsonl` is missing when a requested Evidence record is being resolved;
- `evidence.jsonl` is not a regular local file or is a symlink;
- any JSON object in the Evidence file is malformed;
- an Evidence record has no valid Evidence v1 identity fields;
- an Evidence record's embedded `run_id` does not match the requested Run;
- duplicate `evidence_id` values make the Evidence store ambiguous;
- the requested `evidence_id` is not present.

A tool/data error MUST NOT terminate an otherwise healthy MCP server.

## Locality and security boundary

`get_evidence` MUST NOT:

- accept path-like Run identifiers;
- accept path-like or otherwise invalid Evidence identifiers;
- read outside `<cwd>/.runwitness/runs/<run_id>/`;
- follow a Run-directory or `evidence.jsonl` symlink escape;
- mutate Run or Evidence artifacts;
- create `.runwitness/` for an absent store;
- execute a command or bind a network listener.

The MCP server remains local stdio only.

## Version boundary

This feature contract does not itself advance the stable release version. The implementation remains on the current stable version until a separate v0.0.9 release contract is merged.

## Explicitly deferred

v0.0.9 does not add:

- Evidence listing, filtering, pagination, or full-text search;
- stdout/stderr retrieval through MCP;
- an MCP tool that starts a Run;
- automatic baseline selection;
- remote Run stores;
- MCP resources or prompts;
- metric-aware `regressed` / `improved` comparison;
- ActiveRecord query regression or N+1 analysis;
- browser evidence;
- deeper PostgreSQL analysis;
- production correlation.

## Acceptance boundary

The locked black-box contract MUST prove:

1. `tools/list` exposes exactly `list_runs`, `get_run`, and `get_evidence`;
2. existing `list_runs` and `get_run` behavior remains compatible;
3. `get_evidence` returns the exact decoded canonical Evidence record;
4. the returned Evidence can be addressed directly from a Finding `evidence_refs` value;
5. invalid Run IDs and Evidence IDs are tool errors;
6. missing Evidence is a tool error;
7. malformed or cross-Run Evidence data is a tool error rather than silently ignored;
8. duplicate Evidence IDs are a tool error rather than an arbitrary first/last match;
9. Evidence errors do not terminate the MCP server;
10. MCP Evidence reads do not mutate the local Run store;
11. all unrelated pre-v0.0.9 contracts remain unchanged during implementation.
