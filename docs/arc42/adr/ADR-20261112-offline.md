# ADR-20261112: Offline with a service worker and an outbox

Status: Accepted

## Context
Allmend should keep working when the server is unreachable, e.g. away from
the home network (#7). Forms stay plain HTML (ADR frontend); the server stays
the only place where input is validated (CLAUDE.md, "never shit in, shit
out"). haken stays a separate app and is not integrated; Allmend borrows its
ideas, not its code (CalDAV state sync is a different problem). (stated)

## Decision
- **One service worker for the whole app** (`ui/static/sw.js`, native ES,
  no dependency). Which routes work offline is an explicit list (scope below).
- **Reading offline:** GET pages are fetched network first; on failure
  the last cached copy is shown with a banner
  „Offline – Stand vom …". Cache only same-origin HTML, CSS, JS and icons, and only
  pages the account has opened before (no prefetching).
- **Writing offline (outbox):** when a POST of a form in the write scope fails because
  the network is down, the service worker stores the form data in an
  IndexedDB outbox and answers with a page „Offline gespeichert – wird
  übertragen". The form itself needs no JS.
  - Every entry carries a client-generated id (UUID) as idempotency key and
    the account id it was made with. The server remembers processed keys, so
    a replay never creates a duplicate.
  - Replay in the original order, one entry at a time, on the `online` event
    and on every page load (Background Sync is missing in Firefox and Safari).
  - The server validates a replayed request exactly like a live one,
    including page versions (optimistic locking, ADR pages). There is no
    separate sync API.
  - A rejected entry is never dropped silently. It stays in the outbox with
    the server's error and is listed on a page „Nicht übertragen", where the
    user can open, correct and resend it or discard it.
  - Session expired: replay stops, the user logs in again, the entries stay.
    An entry of another account is never sent with the current session.
- **Logout clears the cache and the outbox** (after warning if the outbox is
  not empty), so a shared device leaks no data.
- **Schiffe versenken has no offline mode.** Without a connection it shows
  an error („Schiffe versenken braucht eine Verbindung"); no move is queued.
- **Scope (stated, 2026-11-13):**

  | Offline | What |
  | --- | --- |
  | Read + write | Ausgaben: add an entry. Arbeitswege: change. Velo: add a tour. |
  | Read only | Start page and navigation, and every other module and page (incl. the shopping list page, #7). |
  | Nothing | Schiffe versenken (error, see above). |

  Arbeitswege edits carry the year's version (optimistic locking), so a
  stale offline change is rejected and shown, never overwrites.

## Why
- One mechanism instead of one per module or one app per module: accounts,
  permissions and design stay in one place. (stated)
- Replaying the original form requests reuses all existing validation and
  conflict handling (versions), so offline input cannot bypass the rules.
- Queuing commands, not state, avoids a client-side merge model.

## Consequences
- New: one service worker, a small outbox page, an idempotency table
  (migration step), and a list of offline routes (read, write) kept next to
  the routes in `Server.Handler`.
- Offline entries are not shown in lists until they are transferred (no
  client rendering); the outbox page and a counter in the header show them.
- The service worker must be versioned with the release so old caches are
  dropped on upgrade.
