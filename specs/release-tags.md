# Release Tagging

RunWitness release tags are immutable and are created only after the corresponding `main` CI run succeeds.

The release-tag workflow is triggered by a completed `CI` workflow on `main`. It checks the exact successful commit and compares the Runner version in `internal/runner/runner.go` with the first parent of that commit.

A tag is created only when that commit changes the Runner semantic version.

For example, a successful merge that changes:

```text
0.0.4 -> 0.0.5
```

creates:

```text
v0.0.5
```

at the exact successful `main` commit.

A later documentation or CI-only merge that leaves the Runner version at `0.0.5` does not create or move a tag.

If the expected tag already exists at a different commit, automation fails rather than moving the tag.

This keeps release identity tied to a green `main` commit and prevents a red contract-only merge from becoming an installable release.
