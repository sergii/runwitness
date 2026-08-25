# RunWitness Git State Contract

Status: Draft
Target: v0.0.1

## Purpose

RunWitness records repository state before and after a Run so application changes can be compared without confusing observer-generated artifacts with changes made by the target command.

The recorded state is an observation of the user's repository, not of RunWitness's own local artifacts.

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

Ignored files remain governed by Git ignore rules and are not part of the observed state.

## Dirty state

`git.before.dirty` and `git.after.dirty` indicate whether the repository has observable changes relative to `HEAD`.

Observable changes include:

- tracked modifications;
- staged changes;
- tracked deletions;
- untracked, non-ignored files.

If no observable changes exist, `dirty` MUST be `false` and `diff_hash` SHOULD be absent or null.

## Diff hash

When `dirty` is `true`, `diff_hash` MUST deterministically fingerprint the complete observable working-tree state relative to `HEAD`.

The fingerprint MUST account for both tracked changes and untracked, non-ignored files. For untracked files, both path identity and file content are part of the observable state.

Therefore:

- changing the bytes of an untracked file MUST change `diff_hash`;
- adding or removing an untracked file MUST change `diff_hash`;
- renaming an untracked file MUST change `diff_hash`, even if its bytes are unchanged;
- changes only inside `.runwitness/` MUST NOT change `diff_hash`;
- ignored files MUST NOT change `diff_hash`.

The exact internal hashing algorithm is an implementation detail. The public contract is semantic: equal observable Git states should produce equal fingerprints, and materially different observable Git states must not collapse to the same fingerprint merely because Git's normal tracked-file diff is empty.

## Contract examples

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

Given a repository containing one untracked application file and a target command that changes only that file's bytes, both snapshots remain dirty but their fingerprints MUST differ:

```text
git.before.dirty = true
git.after.dirty  = true
git.before.diff_hash != git.after.diff_hash
```
