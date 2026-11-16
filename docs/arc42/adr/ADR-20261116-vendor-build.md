# ADR-20261116: Front-end libraries built, not checked in

Status: Proposed

## Context
Chart.js, Leaflet, leaflet.heat and Mermaid were downloaded by hand and
committed to `ui/static/vendor` (~2 MB, versions in the file names); Markmap
1.x (ADR markmap) needs an esbuild bundle anyway. Every update was manual and
grew the repository. Markus asked to load them during the build. (stated)

## Decision
- `tools/vendor/package.json` pins all front-end libraries (and esbuild) with
  a lockfile; `tools/vendor/build.mjs` fills `ui/static/vendor`: ready-made
  browser files are copied as they are, Markmap is bundled. Scripts are
  stored gzipped only. `LICENSES.txt` holds the license of every package
  that ships code to the browser.
- `ui/static/vendor` is not in git. `make vendor` builds it (rebuilt when
  `tools/vendor` changes); `build`, `test`, `run`, `demo` and `validate`
  depend on it. The Dockerfile builds it in a Node stage
  (`dhi.io/node:24-debian13-dev`, on the build platform), CI before tests,
  validation, browser tests and the demo.
- Stable file names without versions (`mermaid.min.js`, `markmap.js`, …),
  so a version bump needs no code change. All static files are revalidated
  (`no-cache` + ETag, 304 when unchanged) instead of cached as immutable.
- The binary refuses to start without them (`webui.CheckVendor`): no
  release without charts, maps and diagrams.
- Dependabot proposes updates for `tools/vendor` (one grouped PR per week),
  checked by CI like any other change.
- Replaces the "vendored, pinned" files of ADR frontend and the committed
  bundle and versioned names of ADR markmap; nothing else of either changes
  (still loaded only where needed, no runtime requests to other hosts).

## Why
Versions in one place, automatic update proposals, no large minified files
in the history. Node is only a build tool; nothing new at runtime.

## Consequences
- Every build needs the npm registry (CI, the Docker build on the NAS,
  local); a plain `go build` without `make vendor` gives a binary that does
  not start.
- Local development needs Node (24) next to Go.
