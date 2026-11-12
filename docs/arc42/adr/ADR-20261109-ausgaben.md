# ADR-20261109: Ausgaben (shared expenses, Splitwise replacement)

Status: Accepted

## Decision
- **Groups.** A group ("Gruppe", e.g. household, holidays) has a
  name and a set of at least two members, chosen from the active accounts
  with module access. New data scope **members**: only the members see and
  edit the group and everything in it; the repository checks membership in
  SQL on every read and write. Any member may rename the group and **add**
  members; a new member sees the whole history and starts at balance 0.
  Nothing is ever removed: neither members nor groups.
- **Entries.** An expense records date, description, category, amount (`int64` minor units), currency, who paid and every member's
  share; the shares add up to the amount. The form offers "gleich" (equal
  among the chosen people; remainder units go first to those who did not pay)
  or fixed amounts. A payment ("Zahlung") is the same with exactly one share:
  the receiver. Any member may edit or delete any entry (optimistic version
  check); deleting keeps the row (`deleted_at`, `deleted_by`).
- **Categories** are a fixed list (the subcategories Splitwise uses, under
  seven main categories: Freizeit, Essen & Trinken, Wohnen, Leben, Verkehr,
  Nebenkosten, Allgemein). Only the subcategory key is stored (e.g.
  `groceries`, `food_other`); the main category is derived from it. Keys are
  English, display names German. Each subcategory has its own line icon
  (own SVG, no emoji), tinted with its main category's colour. The form picks
  one in an expandable list grouped by main category, with a small optional
  search script. Payments have no category. Existing free-text categories are
  mapped by a migration step with a fixed table; an unmapped value aborts the
  migration (checked with `migrate --dry-run` on a copy of real data first).
- **Balances** per currency, never converted: paid minus owed. Settle-up
  proposals: the biggest debtor pays the biggest creditor until even; "Als
  bezahlt erfassen" opens a prefilled payment form to confirm.
- **Settled entries** are hidden per currency: the group page shows the
  entries after the last point at which that currency's balance was zero for
  everyone (entries in date, then creation order), followed by a divider row
  "ausgeglichen am …" linking to an archive view with a year filter. The
  point is computed on every request, never stored, so a back-dated entry
  moves it automatically. The page filters by currency (all or one).
- **Statistics** per group on their own page: a donut chart (inline SVG,
  no JS; colours via CSS classes, as the CSP forbids inline styles) of
  expenses by main category, filters (links) for year
  (or all), group total or own share, and currency (never converted); a
  main category breaks down into its subcategories. Payments do not count;
  unsplit imported entries count in the group total, not in the own share.
- **Recurring** (monthly only): description, amount, payer, shares, day of
  month and next date. Due months are booked when someone opens the group
  (no scheduler; at most 24 months at once); a short month uses its last day.
  Ending it keeps the booked entries.
- **Splitwise import**, once per group, only into a group without entries:
  the CSV export of one Splitwise group; each person column is mapped to a
  member by name. Every row is validated (format, nets add up to zero, one
  payer) and the file's "Total balance" rows must equal the computed
  balances, so a truncated file is refused. Any problem aborts the whole
  import. Rows without effect on anyone (all nets 0) are imported as
  expenses without payer and shares ("nicht geteilt"): Splitwise does not
  export who paid them. Categories are mapped to the fixed list; an unknown
  one aborts the import.
- Out for now: receipts, currency conversion, other repeat intervals,
  rows with several payers (refused on import), removing members or groups.

## Why
Old issue "Ausgaben teilen": much simpler than Splitwise. The family uses two groups: a small
set of recurring costs and one for everything else, entered by hand
(stated). Members-only scope fits money shared between some
of the family, not all of it. Validating the totals of the export makes the
one-time import provably complete.
