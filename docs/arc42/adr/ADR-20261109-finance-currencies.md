# ADR-20261109: Currencies in finance, untimed tracks, corrected 0.1.0 steps

Status: Accepted

## Decision
- Finance holds several currencies (stated). A dividend keeps the currency it
  was paid in (a broker may pay a USD dividend in CHF); sums per security
  convert it with the newest rate. A year of the history has one currency for
  all its amounts; the chart uses it if all years share it, else the base
  currency at today's rate. A missing rate leaves the amount out, with a
  notice. Nothing is estimated.
- Securities have Markdown notes. The hand-written `_source` and `_note` of
  an old composition become those notes.
- A GPS point has either a time or, for tracks drawn without times (old KMZ
  lines, `line:00000123`), its position in the track (stated: the date is
  optional). Untimed points show on their trip, not on the timeline.
- These changes went into the released steps `0006_places` and
  `0007_finanzen` instead of a new step. Exception to "never edit a released
  step", decided by the owner: 0.1.0 could not import the real data, its
  transaction rolled back, so no database holds the 0.1.0 schema.

## Why
The dry run of 0.1.0 against the real `almanach.db` failed on exactly these
records. The data was correct; the model was too narrow.
