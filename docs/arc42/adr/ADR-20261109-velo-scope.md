# ADR-20261109: Velo scope

Status: Accepted

## Decision
Velo (formerly Rad/Randdistanz) keeps: year overview with key figures, year
comment, cumulative year chart with filter per bike, comparison with other
years, personal records, monthly goal, ride heatmap, tours (create, list,
detail), bikes and repairs.

Removed: milestones/badges, week and day streaks, records per bike, most-used
bike, ODS import.

## Why
Agreed in/out list (stated); less code, calmer page.

## Implementation notes

- Data model and legacy import: tables `velo_*`, imported in migration step 4
  (bikes, tours, repairs, year comments, monthly goals). Any unexpected legacy
  value stops the migration with the affected ids.
- All amounts are in the base currency (`ALLMEND_BASE_CURRENCY`, EUR); there
  is no currency per amount.
- The chart (Chart.js, vendored gzipped) shows cumulative km of the selected
  year and the previous one; the bike filter is a plain GET form, so it works
  without JavaScript. The year table carries the same numbers.

