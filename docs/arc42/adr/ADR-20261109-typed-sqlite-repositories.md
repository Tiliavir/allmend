# ADR-20261109: Typed repositories on SQLite

Status: Accepted

## Decision
- Each module owns its tables and a typed repository (Go structs, explicit SQL).
  The generic `Record` (`map[string]any`), JSON-Schema CRUD UI and per-connector
  config are removed.
- One SQLite database file (`modernc.org/sqlite`, pure Go, WAL). The
  filesystem-JSON backend is removed.
- Ports and adapters stay: domain code depends on repository interfaces;
  only `cmd/allmend` wires the SQLite implementations. Enforced by `test/arch`.
- Record IDs: existing IDs are kept; new IDs are 128-bit random hex
  (`crypto/rand`). Drops `google/uuid` and `santhosh-tekuri/jsonschema`.

## Why
The generic model was an abstraction without real value (stated). Typed code
is explicit, checked by the compiler and lets SQLite enforce constraints.
