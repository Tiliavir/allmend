# CLAUDE.md — Allmend

Family workbench (Go, server-rendered HTML). Architecture: `docs/arc42/`,
decisions: `docs/arc42/09-architecture-decisions.md`. Open points: `OPEN.md`.

## Non-negotiables

- **No data loss.** Schema changes only as a new step in
  `internal/migrations` (never edit a released step). Every step runs in the
  one migration transaction after the automatic backup. Test the step with
  `allmend migrate --dry-run` against a copy of real data before release.
- **No data leak.** Owned rows carry `owner_id`; repositories filter by the
  acting account in SQL. Access per module is checked by `Server.Module`.
  No PII in logs (account ids only, never e-mail or names).
- **HTML and CSS first.** Plain forms with POST/redirect/GET. No inline
  styles or scripts (CSP). JS only where HTML cannot do it, as small native
  ES modules served from `ui/static`. UI text is German.
- **Never "shit in, shit out".** Unexpected input is always an error —
  never guessed, defaulted or dropped. Validate at the interface where data
  enters (HTTP forms, env, file imports, external APIs) and back it with
  database constraints, so everything stored is known to be valid.
  Unexpected errors go to `s.fail` (logged, generic page), never swallowed.
- **Few dependencies.** Runtime: `modernc.org/sqlite`, `golang.org/x/crypto`,
  later `goldmark`. A new one needs an ADR.
- **Offline** (ADR offline): the forms that work offline are listed in
  `offlineWrites` (`internal/webui/offline.go`) and nowhere else; they must
  stay POST/redirect/GET and reject stale versions. A page that shows a
  one-time message is `no-store`. Change `ui/static/sw.js` only together
  with `tests/e2e`.
- Money as `int64` minor units; timestamps stored UTC (RFC 3339), shown in
  `Europe/Zurich`.

## Layout

```
cmd/allmend          wiring, subcommands (serve, migrate, hashpw, health, demo)
internal/<module>    domain types, services, repository interfaces
internal/store       SQLite repositories (only cmd + tests import it)
internal/db          open, migrate (backup, one transaction), counts
internal/migrations  the ordered list of schema steps
internal/webui       routing, middleware, handlers
internal/demo        made-up data and static export for the public demo
ui/templates, ui/static  (ui/static/sw.js: service worker, served as /sw.js)
tests/e2e            Playwright browser tests against the real binary
```

Layer rules are enforced by `internal/archtest`.

## Commands

```sh
make check      # lint + test + W3C validation (as CI)
make run        # local server on :8080 (needs ALLMEND_ADMIN_PASSWORD_HASH)
make demo       # static demo site in .cache/demo
go run ./cmd/allmend migrate --dry-run
npm ci && npx playwright test   # browser tests (installed Chrome)
```

## Adding a module

1. `internal/<module>`: types, service, repository interface; scope rules
   from ADR access-and-data-scope.
2. `internal/store/<module>.go` + tests against a real database.
3. Migration step: the module's tables.
4. Handlers in `internal/webui/<module>.go`, routes in `Server.Handler`
   wrapped with `s.Module(...)`; templates in `ui/templates/<module>/`.
5. Set `Ready: true` in `account.Modules`; add pages to `TestPagesRender`.
6. Add demo data in `internal/demo` (seeded through the service).
