package migrations

// v8 creates the Ausgaben tables. The module is new: no legacy data.
// Members of a group are fixed; every payer and share references a member
// (composite foreign key), so nothing can point outside the group.
const v8 = `
CREATE TABLE expense_groups (
	id         TEXT NOT NULL PRIMARY KEY,
	name       TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
	created_by TEXT NOT NULL REFERENCES accounts(id),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE expense_members (
	group_id   TEXT    NOT NULL REFERENCES expense_groups(id),
	account_id TEXT    NOT NULL REFERENCES accounts(id),
	position   INTEGER NOT NULL CHECK (position >= 0),
	PRIMARY KEY (group_id, account_id),
	UNIQUE (group_id, position)
) STRICT;
CREATE INDEX expense_members_account ON expense_members(account_id);

CREATE TABLE expense_recurring (
	id          TEXT    NOT NULL PRIMARY KEY,
	group_id    TEXT    NOT NULL REFERENCES expense_groups(id),
	description TEXT    NOT NULL CHECK (length(description) BETWEEN 1 AND 120),
	category    TEXT    NOT NULL CHECK (length(category) <= 60),
	amount      INTEGER NOT NULL CHECK (amount > 0),
	currency    TEXT    NOT NULL CHECK (length(currency) = 3 AND currency GLOB '[A-Z][A-Z][A-Z]'),
	paid_by     TEXT    NOT NULL,
	day         INTEGER NOT NULL CHECK (day BETWEEN 1 AND 31),
	next_date   TEXT    NOT NULL CHECK (next_date GLOB '[0-9][0-9][0-9][0-9]-[0-1][0-9]-[0-3][0-9]'),
	version     INTEGER NOT NULL CHECK (version >= 1),
	created_by  TEXT    NOT NULL REFERENCES accounts(id),
	created_at  TEXT    NOT NULL,
	updated_at  TEXT    NOT NULL,
	FOREIGN KEY (group_id, paid_by) REFERENCES expense_members(group_id, account_id)
) STRICT;

CREATE TABLE expense_recurring_shares (
	recurring_id TEXT    NOT NULL REFERENCES expense_recurring(id) ON DELETE CASCADE,
	group_id     TEXT    NOT NULL,
	account_id   TEXT    NOT NULL,
	amount       INTEGER NOT NULL CHECK (amount >= 0),
	PRIMARY KEY (recurring_id, account_id),
	FOREIGN KEY (group_id, account_id) REFERENCES expense_members(group_id, account_id)
) STRICT;

CREATE TABLE expense_entries (
	id           TEXT    NOT NULL PRIMARY KEY,
	group_id     TEXT    NOT NULL REFERENCES expense_groups(id),
	kind         TEXT    NOT NULL CHECK (kind IN ('ausgabe', 'zahlung')),
	date         TEXT    NOT NULL CHECK (date GLOB '[0-9][0-9][0-9][0-9]-[0-1][0-9]-[0-3][0-9]'),
	description  TEXT    NOT NULL CHECK (length(description) <= 120),
	category     TEXT    NOT NULL CHECK (length(category) <= 60),
	amount       INTEGER NOT NULL CHECK (amount > 0),
	currency     TEXT    NOT NULL CHECK (length(currency) = 3 AND currency GLOB '[A-Z][A-Z][A-Z]'),
	paid_by      TEXT,
	recurring_id TEXT    REFERENCES expense_recurring(id) ON DELETE SET NULL,
	version      INTEGER NOT NULL CHECK (version >= 1),
	created_by   TEXT    NOT NULL REFERENCES accounts(id),
	created_at   TEXT    NOT NULL,
	updated_by   TEXT    NOT NULL REFERENCES accounts(id),
	updated_at   TEXT    NOT NULL,
	deleted_at   TEXT,
	deleted_by   TEXT    REFERENCES accounts(id),
	CHECK (paid_by IS NOT NULL OR kind = 'ausgabe'),
	CHECK ((deleted_at IS NULL) = (deleted_by IS NULL)),
	FOREIGN KEY (group_id, paid_by) REFERENCES expense_members(group_id, account_id)
) STRICT;
CREATE INDEX expense_entries_group ON expense_entries(group_id, date);

CREATE TABLE expense_shares (
	entry_id   TEXT    NOT NULL REFERENCES expense_entries(id),
	group_id   TEXT    NOT NULL,
	account_id TEXT    NOT NULL,
	amount     INTEGER NOT NULL CHECK (amount >= 0),
	PRIMARY KEY (entry_id, account_id),
	FOREIGN KEY (group_id, account_id) REFERENCES expense_members(group_id, account_id)
) STRICT;
`
