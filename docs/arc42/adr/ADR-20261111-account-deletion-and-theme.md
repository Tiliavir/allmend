# ADR-20261111: Deleting accounts, colour scheme per account

Status: Proposed

Issue #17. Extends [Access per module](ADR-20261109-access-and-data-scope.md)
and [Frontend](ADR-20261109-frontend.md).

## Decision

- **Admins delete only accounts without data.** Deleting removes the
  account, its sessions, module grants and page grants. Any other row that
  refers to it (owned data, pages written, games, expense groups) is a
  foreign key without cascade, so SQLite refuses and the admin sees "Sperre
  es stattdessen". Nothing is deleted implicitly; the use case is an
  invitation to a wrong address. To remove a person who has data, disable
  the account (all data stays).
- Admins cannot delete themselves; the last active admin always stays.
  The form needs a ticked confirmation box (no JS dialog).
- **Darstellung per account:** Automatisch (default, follows the device),
  Hell, Dunkel. Stored in `accounts.theme` (schema step 11, CHECK on the
  three values). Logged-out pages follow the device.
- Dark colours move to `ui/static/dark.css`. The layout links it with
  `media="(prefers-color-scheme: dark)"` (Automatisch), `media="all"`
  (Dunkel) or not at all (Hell). No JS, no duplicated colour block.
  `light-dark()` was not used: scripts read colours with
  `getComputedStyle`, which returns the unresolved function for custom
  properties.

## Why

No data loss is a non-negotiable; deleting a person's Velo, Finanzen or
shared pages as a side effect of "delete" would break it. Deleting a person
together with all their data can come later as its own decision.
