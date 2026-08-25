# RunWitness Git State Contract

Status: Draft
Target: v0.0.1

## Purpose

RunWitness records repository state before and after a Run so application changes can be compared without confusing observer-generated artifacts with changes made by the target command.

## Observer-owned paths

The repository-local `.runwitness/` directory is reserved for RunWitness artifacts.

RunWitness MUST exclude `.runwitness/` and all descendants from the git dirty-state and diff-hash observations that it records for a Run.

Creating Run artifacts such as:

```text
.runwitness/runs/<run_id>/run.json
.runwitness/runs/<run_id>/evidence.jsonl
.runwitness/runs/<run_id>/stdout.log
.runwitness/runs/<run_id>/stderr.log
```

MUST NOT cause a repository that was otherwise clean to appear dirty in `run.git.after`.

The exclusion applies to both before and after observations so previous RunWitness artifacts also remain observer noise rather than application state.

`.runwitness/` is therefore a reserved namespace. Target commands that intentionally write application data inside this directory should not expect those changes to be represented in RunWitness git-state evidence.

## Contract example

Given a clean git repository and a target command that does not modify repository files:

```text
runwitness run -- echo hello
```

RunWitness MUST record:

```text
git.before.dirty = false
git.after.dirty  = false
```

and neither state should contain a `diff_hash`.

The existence of RunWitness artifacts on disk does not change that result.

## Future canonicalization

The complete canonicalization algorithm for dirty working-tree fingerprints, including untracked application files, will be specified separately. This contract only establishes that RunWitness's own reserved artifact namespace cannot influence observed application git state.
