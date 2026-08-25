# Changelog

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
