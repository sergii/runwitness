# Release Tagging Example

A release merge that changes `const Version = "0.0.4"` to `const Version = "0.0.5"` and passes `main` CI publishes `v0.0.5` at that exact merge commit.

A later merge that leaves the Runner version unchanged publishes no tag.
