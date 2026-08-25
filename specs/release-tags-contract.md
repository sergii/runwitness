# Release Tag Safety Contract

Release tags are immutable publication boundaries.

The automated release-tag workflow MUST:

- run only after the `CI` workflow for `main` completes successfully;
- inspect the exact successful commit reported by that workflow run;
- create a tag only when the Runner semantic version changed relative to that commit's first parent;
- name the tag `v<runner_version>`;
- point the tag to the exact successful `main` commit;
- do nothing when the Runner version did not change;
- never move an existing tag;
- fail if the expected tag already exists at a different commit.

A contract-only merge whose CI is intentionally red MUST NOT create a release tag.
