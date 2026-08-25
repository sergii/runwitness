# Release Tagging

RunWitness release tags are immutable publication boundaries.

The automated release-tag workflow MUST:

- run only after the `CI` workflow for `main` completes successfully;
- inspect the exact successful commit reported by that workflow run;
- compare the Runner semantic version in that commit with its first parent;
- create a tag only when the Runner version changed;
- name the tag `v<runner_version>`;
- point the tag to the exact successful `main` commit;
- do nothing when the Runner version did not change;
- never move an existing tag;
- fail if the expected tag already exists at a different commit.

A contract-only merge whose CI is intentionally red MUST NOT create a release tag.

For example, a green release merge that changes `0.0.4` to `0.0.5` creates `v0.0.5` at that merge commit. A later documentation-only merge that leaves the Runner at `0.0.5` creates no tag.
