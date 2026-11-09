# ADR-20261109: Ausgaben (shared expenses, Splitwise replacement)

Status: Proposed

## Decision
- **Groups.** A group ("Gruppe", e.g. household, holidays) has a
  name and a fixed set of at least two members, chosen at creation from the
  active accounts with module access. New data scope **members**: only the
  members see and edit the group and everything in it; the repository checks
  membership in SQL on every read and write. Members cannot be changed later.
- **Entries.** An expense records date, description, optional category (free
  text), amount (`int64` minor units), currency, who paid and every member's
  share; the shares add up to the amount. The form offers "gleich" (equal
  among the chosen people; remainder units go first to those who did not pay)
  or fixed amounts. A payment ("Zahlung") is the same with exactly one share:
  the receiver. Any member may edit or delete any entry (optimistic version
  check); deleting keeps the row (`deleted_at`, `deleted_by`).
- **Balances** per currency, never converted: paid minus owed. Settle-up
  proposals: the biggest debtor pays the biggest creditor until even; "Als
  bezahlt erfassen" opens a prefilled payment form to confirm.
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
  export who paid them. Categories are kept as exported (English).
- Out for now: receipts, currency conversion, other repeat intervals, adding
  or removing members, rows with several payers (refused on import).

## Why
Old issue "Ausgaben teilen": much simpler than Splitwise. The family uses two groups: a small
set of recurring costs and one for everything else, entered by hand
(stated). Members-only scope fits money shared between some
of the family, not all of it. Validating the totals of the export makes the
one-time import provably complete.
