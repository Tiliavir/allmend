# Changelog

All notable changes. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions: [Semantic Versioning](https://semver.org/). Releasing is described in
[CONTRIBUTING.md](CONTRIBUTING.md#releasing).

## [0.7.0] - 2026-11-13

### Added

- Offline mode (#7): pages opened before stay readable while Allmend is
  unreachable, with a banner „Offline – Stand vom …“. Adding an Ausgabe,
  changing Arbeitswege and adding a Velo tour also work offline: the entries
  wait in an outbox on the device and are transferred in order when the
  server is back; the header shows how many are waiting. What the server
  rejects (e.g. Arbeitswege changed elsewhere meanwhile) stays on the page
  „Nicht übertragen“ to correct or discard. Schiffe versenken and every other
  change need a connection and say so. Logout removes the offline copies and
  the outbox (with a warning while entries wait). Schema step 13 adds the
  table `offline_requests`.
- Browser tests with Playwright (`npm ci && npx playwright test`).

## [0.6.0] - 2026-11-13

### Added

- Ausgaben: statistics per group – a donut by main category with year,
  group total or own share and currency, plus the breakdown of one main
  category into its subcategories.
- Ausgaben: groups can be renamed and get new members; nobody is removed.
- Ausgaben: archive of settled entries with currency and year filter.

### Changed

- Ausgaben: categories are a fixed list (43 subcategories in seven main
  categories, as in Splitwise), picked from tinted cards with icons and an
  optional search. Schema step 12 maps existing categories (Splitwise names,
  German names, keys; empty becomes "Allgemein") and aborts naming every
  value it cannot map – run `allmend migrate --dry-run` on a copy first.
  The Splitwise import maps the export's categories the same way and refuses
  unknown ones.
- Ausgaben: the group page shows the entries since each currency was last
  settled, a "ausgeglichen am …" row links to the older ones; filter by
  currency instead of year.

## [0.5.0] - 2026-11-11

### Added

- Konto: "Darstellung" – Automatisch (wie das Gerät), Hell oder Dunkel (#17).
  Schema step 11 adds the setting; existing accounts stay on Automatisch.
- Verwaltung: admins can delete an account that has no data yet, e.g. an
  invitation to a wrong address. Accounts with data are disabled instead (#17).

### Changed

- Header on phones and tablets: the account menu stays top right, the
  modules wrap below it; on phones only the avatar is shown (#16).
- Start page on phones: module tiles in two compact columns (#18).

## [0.4.0] - 2026-11-11

### Added

- Schiffe versenken: new variant "Seeminen". Each side also hides three sea
  mines; you choose the ship you fire with, and a shot on a mine sinks that
  ship (#5).

### Changed

- Schiffe versenken: placing the fleet works on the board. Tap a ship to
  turn it around its middle, drag ships (and mines) to move them; the
  landing cells turn grey, overlapping or touching cells red. The list form
  stays available under "Als Liste bearbeiten".

### Removed

- The one-time import of an almanac database (`almanach.db`) and the
  variable `ALLMEND_LEGACY_DB`. Databases migrated with 0.3.0 or earlier
  keep their data and need nothing. Remove `ALLMEND_LEGACY_DB` from the
  container configuration if it is set: unknown variables stop the start.
  To switch from almanac, start 0.3.0 first.

### Fixed

- Schiffe versenken: board cells stay square on phones when it is your turn
  (the shot buttons and salvo checkboxes made the rows taller), and the
  12 × 12 board fits narrow screens.

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
