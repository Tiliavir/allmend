# ADR-20261109: Public demo as a static export on GitHub Pages

Status: Proposed

## Decision
- `allmend demo DIR` seeds a temporary database with a made-up family
  (`internal/demo`), logs in as its admin and crawls the app from `/`,
  following links and every option of the GET filter forms. Each page is
  written as static HTML; URLs with a query become directories
  (`/velo?jahr=2025` → `velo/~jahr=2025/`). Assets are copied, gzip-only
  vendor files unpacked.
- The seed goes through the domain services, so demo data passes the same
  validation as user input. Dates are relative to the build date.
- `Server.Demo` adds a banner and `demo.js`: filter forms open the exported
  page, all other forms only show that nothing is saved.
- The export is strict: any page not answering 200, any missing asset or
  any query that cannot be mapped is an error. `TestDemo` checks every link
  of the result; `make validate` runs the W3C validator over it.
- CI (`pages.yml`) builds and deploys it on push to `main` and monthly.

## Why
Show the app without running it and without real data. A static export
needs no server and no new dependency, and reuses the real templates, so
the demo cannot drift from the app.

## Consequences
- New modules add their data to `internal/demo`.
- Pages with free input (e.g. a date in Orte) are only exported for the
  values that are linked; others show the 404 page.
- Requires the repository to be public (or a paid plan) and Pages set to
  "GitHub Actions".
