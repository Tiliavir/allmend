# ADR-20261109: Schema migrations, backup and legacy import

Status: Accepted

## Decision
- Versioned migrations, compiled into the binary, applied in order at startup.
  One global version sequence (a module's import may depend on accounts), table
  `schema_migrations(version, name, applied_at)`. A binary refuses a database
  with a higher version than it knows.
- Before any pending migration the server writes a consistent copy of the
  database (`VACUUM INTO data/backup/allmend-<UTC timestamp>-pre-v<N>.db`).
- All pending migrations of one start run in one transaction. Any error rolls
  back, the server logs it and exits non-zero. No downgrades; restore = backup.
- Legacy import: the step that creates a module's tables also imports that
  module's old data — `almanach.db` attached read-only as `legacy` (JSON `data`
  column; production used only SQLite connectors) — owned by the configured first admin. The old files are never modified.
  Unexpected legacy data (invalid date, unknown reference, wrong type, foreign
  currency) stops the migration and names the rows; it is fixed in the old app
  and the start retried. Only documented defaults of the old app are applied.
  The import is removed again in 2.1 (#53).
- `allmend migrate --dry-run --db <copy>` runs everything on a copy and prints
  record counts per table before and after.

## Why
Data loss is not acceptable. Every upgrade must be repeatable on a backup
first (trial run on the NAS).
