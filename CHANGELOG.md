# Changelog

## v0.0.2

Adds the first runtime Evidence adapter while preserving the v0.0.1 Runner core contract.

### Added

- opt-in `runwitness run --otel -- <command>` observation mode
- Run-owned `otlp-mcp` backend lifecycle over MCP stdio
- OTLP endpoint discovery and start/end snapshot boundaries
- target exporter environment injection
- `runwitness.run_id` OpenTelemetry resource correlation
- preservation of existing `OTEL_RESOURCE_ATTRIBUTES`
- Evidence v1 JSON Schema
- normalized `otel.span`, `otel.log`, and `otel.metric` records in `evidence.jsonl`
- OpenTelemetry adapter status and evidence counts in `run.json`
- deterministic required-adapter failure semantics
- upstream `otlp-mcp` interoperability release gate

### Changed

- `runwitness --version` now reports `RunWitness v0.0.2`
- CI is pinned to Ubuntu 24.04 rather than the moving `ubuntu-latest` alias

### Deferred

OpenTelemetry Findings, quality gates, baseline diffing, Ruby/Rails-specific evidence, browser correlation, database analysis, public RunWitness MCP tools, and production correlation remain future contract slices.

## v0.0.1

Initial stable Runner core.

### Added

- universal `runwitness run -- <command>` execution boundary
- UUIDv7 Run identity and `RUNWITNESS_RUN_ID` propagation
- schema-valid `run.json`
- stdout/stderr capture
- deterministic pass/fail/error CLI exit mapping
- optional Run labels
- Git before/after state with tracked and untracked working-tree fingerprints
- exclusion of `.runwitness/` observer artifacts from measured Git state
- contract-first black-box acceptance suite
- `runwitness --version`

### Deferred

OpenTelemetry, Ruby/Rails adapters, findings, baselines, quality gates, MCP, browser evidence, and database analysis are intentionally outside v0.0.1.
