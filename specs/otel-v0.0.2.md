# RunWitness OpenTelemetry Adapter v0.0.2

Status: Contract draft
Target: v0.0.2

## 1. Purpose

v0.0.2 adds the first runtime Evidence adapter to the stable Runner core: OpenTelemetry.

The goal is not to implement another OTLP collector. RunWitness coordinates an existing local OpenTelemetry backend, creates a Run-scoped observation boundary, injects the backend endpoint into the target process, and normalizes telemetry produced during that Run into RunWitness Evidence.

The first reference backend is `otlp-mcp`.

## 2. CLI contract

OpenTelemetry observation is opt-in:

```text
runwitness run --otel -- <command> [args...]
```

Existing v0.0.1 behavior without `--otel` MUST remain unchanged.

When `--otel` is requested, the reference Runner looks for an `otlp-mcp` executable on `PATH`.

For deterministic testing and explicit local overrides, the executable path MAY be overridden with:

```text
RUNWITNESS_OTLP_MCP_BIN=/path/to/otlp-mcp
```

The override names an executable, not an arbitrary shell command.

## 3. Backend ownership

For v0.0.2 the Runner starts a dedicated `otlp-mcp` process for each Run.

The reference invocation is conceptually:

```text
otlp-mcp serve --transport stdio --otlp-port 0
```

A dedicated backend gives each Run an isolated telemetry buffer and avoids accidental mixing with unrelated local processes or another Run.

RunWitness communicates with the backend through MCP as an adapter implementation detail. MCP is still not part of the universal Runner core or RunWitness public data model.

The backend lifecycle is owned by the Run and MUST be terminated when the Run completes or aborts.

## 4. Observation lifecycle

When `--otel` is enabled, the lifecycle is:

```text
create Run + UUIDv7
        |
start OTEL backend
        |
connect to backend
        |
get_otlp_endpoint
        |
create start snapshot
        |
inject OTEL environment + Run identity
        |
execute target command
        |
create end snapshot
        |
get_snapshot_data(start, end)
        |
normalize Evidence
        |
write evidence.jsonl + run.json
        |
stop backend
```

The start snapshot MUST be created before the target process starts.

The end snapshot MUST be created after the target process exits and before snapshot data is read.

## 5. Target environment

The Runner MUST preserve the v0.0.1 environment contract:

```text
RUNWITNESS_RUN_ID=<uuidv7>
```

When `--otel` is enabled, environment variables suggested by the backend's `get_otlp_endpoint` response are applied to the target process. For the `otlp-mcp` reference backend this normally includes:

```text
OTEL_EXPORTER_OTLP_ENDPOINT=<run-local endpoint>
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
```

Backend-provided exporter variables take precedence over inherited exporter variables because `--otel` explicitly requests routing telemetry into the Run-scoped backend.

The Runner MUST additionally correlate emitted telemetry with the Run by ensuring the target process has:

```text
OTEL_RESOURCE_ATTRIBUTES=...,runwitness.run_id=<uuidv7>
```

Existing `OTEL_RESOURCE_ATTRIBUTES` MUST be preserved. `runwitness.run_id` MUST occur exactly once and MUST describe the current Run.

## 6. Evidence artifact

`evidence.jsonl` becomes an active artifact in v0.0.2.

Each non-empty line MUST be one JSON object conforming to:

```text
schemas/evidence-v1.schema.json
```

The first normalized OpenTelemetry kinds are:

```text
otel.span
otel.log
otel.metric
```

Every Evidence record MUST contain:

- schema version
- Evidence ID unique inside the Run
- Run ID
- source
- kind
- observed timestamp
- normalized attributes object
- source payload object

The source for the first adapter is:

```text
otel
```

Source payload preserves the backend summary for auditability. RunWitness normalization MUST NOT convert telemetry into policy decisions or Findings in v0.0.2.

## 7. Run summary and adapter status

When the adapter succeeds, `run.json` contains an adapter entry with:

```json
{
  "name": "otel",
  "status": "ok"
}
```

`summary.evidence_count` MUST equal the number of records written to `evidence.jsonl`.

`summary.finding_count` remains `0` in this slice because the Findings engine is still deferred.

A valid OpenTelemetry-enabled Run with no emitted telemetry is allowed and has `evidence_count = 0`.

## 8. Failure semantics

OpenTelemetry is optional when not requested and required when `--otel` is explicitly requested.

If the backend cannot be started or initialized before the target process starts:

- a schema-valid Run MUST still be written;
- the target process MUST NOT execute;
- `run.process.exit_code` is `null`;
- the `otel` adapter status is `unavailable` or `error` as appropriate;
- the overall verdict is `error`;
- the RunWitness CLI exits `2`.

If the target process executes successfully but required telemetry collection fails afterward, the target process result MUST remain recorded while the overall Run verdict becomes `error` because RunWitness cannot claim a reliable observed Run.

If the target process fails and the OTEL adapter succeeds:

- the target exit code is preserved;
- telemetry is still normalized and stored;
- the overall verdict remains `fail`;
- the RunWitness CLI exits `1`.

Instrumentation failure MUST NOT be misreported as an application failure.

## 9. Non-goals for v0.0.2

This slice does not add:

- an OTLP receiver implemented by RunWitness
- a persistent telemetry backend
- OpenTelemetry auto-instrumentation for application languages
- Findings derived from spans, logs, or metrics
- quality gates over telemetry
- baseline comparison
- browser correlation
- production correlation
- support for long-running servers not launched by the Runner

Application instrumentation remains the responsibility of the application/runtime adapter or existing OpenTelemetry tooling.

## 10. Acceptance contract

The locked black-box acceptance suite MUST prove at least:

1. `--otel` injects backend exporter variables into the target process;
2. existing `OTEL_RESOURCE_ATTRIBUTES` are preserved and `runwitness.run_id` is added exactly once;
3. span, log, and metric backend data become schema-valid Evidence records;
4. `summary.evidence_count` matches `evidence.jsonl`;
5. successful OTEL collection records adapter status `ok`;
6. an explicitly requested but unavailable backend produces an error Run and does not execute the target;
7. a failing target still preserves its target exit code and collected Evidence when the adapter succeeds;
8. all stable v0.0.1 behavior without `--otel` continues to pass unchanged.

As with every RunWitness feature, implementation MUST follow only after this contract is accepted and merged.
