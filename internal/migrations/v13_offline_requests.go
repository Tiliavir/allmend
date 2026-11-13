package migrations

// v13 remembers the outbox entries the server applied, so a replay never
// applies a change twice (ADR offline). Rows are kept; they are small.
const v13 = `
CREATE TABLE offline_requests (
	key        TEXT PRIMARY KEY CHECK (length(key) = 36),
	account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	route      TEXT NOT NULL CHECK (route <> ''),
	location   TEXT NOT NULL CHECK (substr(location, 1, 1) = '/'),
	created_at TEXT NOT NULL
) STRICT;
`
