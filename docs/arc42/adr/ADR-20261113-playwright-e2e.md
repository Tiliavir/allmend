# ADR-20261113: End-to-end tests with Playwright

Status: Accepted

## Context
The offline mode (ADR offline) lives in a service worker. Go tests cover the
server side; what the browser does with cache, outbox and transfer needs a
real browser.

## Decision
- Browser tests use `@playwright/test` (pinned in `package.json`, dev only;
  nothing of it ships in the image). Specs in `tests/e2e/`.
- The tests start the real binary on a temporary data directory and stop it
  to make the server unreachable, the way a NAS that is off behaves.
- Locally the installed Chrome is used (`PLAYWRIGHT_CHANNEL` overrides);
  CI runs them in the check workflow.
- `github.com/go-rod/rod`, listed for browser tests in the ADR on
  configuration and dependencies, was never used and stays out.

## Why
Playwright is what haken already uses (stated, 2026-11-13); it controls
service workers and can wait on what the user sees.
