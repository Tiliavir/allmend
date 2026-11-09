# ADR-20261109: Finance prices fetched once a day

Status: Accepted

## Decision
- A job at 05:00 Europe/Zurich fetches the latest prices (Alpha Vantage) and
  exchange rates (self-hosted Frankfurter) into SQLite. Pages read only the
  stored values and show their date.
- Fewer than 25 securities fit the Alpha Vantage free tier (25 requests/day).
  A failed fetch keeps the old values and shows the error on the finance page.
- Manual "Kurse jetzt laden" for admins.

## Why
Fast pages, predictable API usage (stated).

## Implementation (2026-11-09)
- Each daily close is stored once (`fin_prices`, insert-or-ignore) and never
  changed or fetched again (stated: history does not change). The job asks
  for the compact series (last ~100 days), which covers missed days; a newly
  added security starts with that history. The old price and exchange-rate
  caches are imported.
- Budget: one call per active symbol, then the oldest fund profile
  (composition) older than 30 days with what is left of the 25 calls.
  2 s pause between calls. A symbol that fails keeps its data; the error is
  shown with the last run on the finance overview.
- Exchange rates: one stored rate per currency and day; pages convert with
  the newest. Charts use today's rate for all days (no rate history kept).
- Missing rate or price: the position is shown without base value and left
  out of the total, with a notice. Nothing is estimated.
- The old composition cache is not imported; profiles are refetched.
