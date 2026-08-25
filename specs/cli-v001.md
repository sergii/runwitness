# RunWitness CLI Contract v0.0.1

Status: Draft
Target: v0.0.1

## Commands

The v0.0.1 public CLI exposes two stable entry points:

```text
runwitness --version
runwitness run [options] -- <command> [args...]
```

`--version` MUST print exactly one human-readable version line and exit successfully without creating a Run.

For v0.0.1 the release version line is:

```text
RunWitness v0.0.1
```

## Run options

The v0.0.1 `run` command supports:

```text
--label <name>
```

The label is optional. When present it MUST be non-empty and MUST be recorded as `run.label` without becoming part of the target command argv.

The `--` separator terminates RunWitness option parsing. Every argument after it belongs to the target command and MUST be preserved exactly.

Unknown RunWitness options MUST be rejected with CLI exit code `2` before a Run is created.

## Outcomes

A target process that exits normally with status zero produces verdict `pass` and RunWitness exits `0`.

A target process that starts successfully but exits non-zero produces verdict `fail`; the target exit code is preserved in `run.process.exit_code`, and RunWitness exits `1`.

If RunWitness cannot start the target command, the attempt is still a Run because the execution boundary was created and the failure is part of the observation. RunWitness MUST write the normal Run artifacts, record verdict `error`, leave `run.process.exit_code` null, and exit `2`.

CLI usage or option-parsing errors occur before a Run boundary exists. They MUST exit `2` and MUST NOT create `.runwitness/` artifacts.

## Deferred options

`--baseline`, automatic baseline selection, strict warning handling, JSON stdout mode, and arbitrary metadata flags are not part of the stable v0.0.1 CLI contract. They remain reserved for later contract slices.
