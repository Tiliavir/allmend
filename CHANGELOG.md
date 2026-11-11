# Changelog

All notable changes. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions: [Semantic Versioning](https://semver.org/). Releasing is described in
[CONTRIBUTING.md](CONTRIBUTING.md#releasing).

## [Unreleased]

### Removed

- The one-time import of an almanac database (`almanach.db`) and the
  variable `ALLMEND_LEGACY_DB`. Databases migrated with 0.3.0 or earlier
  keep their data and need nothing. Remove `ALLMEND_LEGACY_DB` from the
  container configuration if it is set: unknown variables stop the start.
  To switch from almanac, start 0.3.0 first.

## [0.3.0] - 2026-11-09

### Added

- Seiten: pages form a tree. Create a subpage from a page, see the path and
  the subpages on it, and arrange the tree in the list by drag and drop
  (mouse, or press and hold on touch). Without JavaScript, "Verschieben" on
  a page does the same. Existing pages stay on the top level, sorted by title.

### Changed

- Deleting a page moves its subpages up to its parent.

## [0.2.2] - 2026-11-09

### Security

- Finanzen: the "Kurse" card on the overview (time and result of the last
  price refresh) is shown to admins only. Its message names the price symbols
  of every account's securities, so other accounts could see which
  securities the admin holds.

## [0.2.1] - 2026-11-09

### Fixed

- Orte: the map shows OpenStreetMap tiles again. The tiles are requested with
  the site's origin as referrer; without any referrer OpenStreetMap blocks
  them (403, tile usage policy).

## [0.2.0] - 2026-11-09

Import of a real almanac database works now (the dry run of 0.1.0 failed).

### Added

- Finanzen: notes on securities (Markdown); the hand-written source of an
  old composition is imported into them.
- Finanzen: a dividend has its own payout currency.
- Finanzen: each year of the history has its own currency.
- Orte: GPS tracks without times (old KMZ lines) are imported and shown on
  their trip.

### Changed

- The released migration steps 0006 and 0007 were corrected in place
  (see ADR finance-currencies); 0.1.0 never completed an import.

## [0.1.0] - 2026-11-09

First release of Allmend, the rewrite of almanac as a family workbench.

### Added

- Accounts with Google login, per-module access and an admin role.
- Start page with links to the home network's services.
- Modules Velo, Finanzen, Arbeitswege, Orte, Ausgaben, Seiten and Schiffe versenken.
- One-time import of an existing almanac database; automatic backup before every migration.
