# 8 Cross-cutting concepts

## Valid data only

Never "shit in, shit out": unexpected input is an error, never guessed,
defaulted or silently dropped. Validation happens at the interface where data
enters Allmend — HTTP forms, environment, the legacy import, external APIs —
and is backed by database constraints (`STRICT` tables, `CHECK`, foreign
keys). Hence everything stored is valid, and code reading the database does
not re-check it.

## No data loss

Backup before every migration, one transaction per start, optimistic locking
(`version`) on every editable record, confirmations for deletes, revisions for
pages. See the ADR on migrations and backup.

## Access

Module access per account; data scope per module fixed in code; repositories
filter by owner in SQL. See the ADR on access and data scope.
