# Contributing

- **No data loss, no data leak.** See the rules in [CLAUDE.md](CLAUDE.md) —
  they apply to humans too.
- **Never include real data** (server URLs, credentials, exports, personal
  entries) in code, tests, screenshots or issues.
- Keep pull requests small and focused; describe what and why. `make check`
  runs what CI runs.

## Releasing

In one pull request: rename *Unreleased* in [CHANGELOG.md](CHANGELOG.md) to
`## [x.y.z] - YYYY-MM-DD`. Merge it. The release workflow sees the untagged
version, runs the full check, publishes `ghcr.io/tiliavir/allmend:x.y.z` and
`:latest` (amd64, arm64), and creates the tag `vx.y.z` with a GitHub release
of the changelog section. Do not push tags by hand.

A release with a new migration step needs the dry run from
[deployment](docs/arc42/07-deployment.md#upgrade) first.
