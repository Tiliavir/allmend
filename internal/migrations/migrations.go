// Package migrations lists every schema step of Allmend in order.
//
// Rules: never change or remove a step that was released; add a new one.
// Until 0.3.0 steps 3, 4, 6 and 7 also imported the old almanac data; that
// import was removed (#8), the tables they create are unchanged.
package migrations

import "github.com/tiliavir/allmend/internal/db"

// All returns the ordered migration list.
func All() []db.Migration {
	return []db.Migration{
		{Version: 1, Name: "accounts_sessions_links", Up: db.SQL(v1)},
		{Version: 2, Name: "pages", Up: db.SQL(v2)},
		{Version: 3, Name: "commute", Up: db.SQL(v3)},
		{Version: 4, Name: "velo", Up: db.SQL(v4)},
		{Version: 5, Name: "schiffe", Up: db.SQL(v5)},
		{Version: 6, Name: "places", Up: db.SQL(v6)},
		{Version: 7, Name: "finanzen", Up: db.SQL(v7)},
		{Version: 8, Name: "ausgaben", Up: db.SQL(v8)},
		{Version: 9, Name: "page_tree", Up: db.SQL(v9)},
		{Version: 10, Name: "sea_mines", Up: db.SQL(v10)},
		{Version: 11, Name: "account_theme", Up: db.SQL(v11)},
		{Version: 12, Name: "expense_categories", Up: v12},
	}
}

const v1 = `
CREATE TABLE accounts (
	id                   TEXT    PRIMARY KEY,
	email                TEXT    NOT NULL UNIQUE COLLATE NOCASE,
	name                 TEXT    NOT NULL DEFAULT '',
	is_admin             INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
	status               TEXT    NOT NULL CHECK (status IN ('invited', 'active', 'disabled')),
	google_sub           TEXT    UNIQUE,
	password_hash        TEXT,
	password_must_change INTEGER NOT NULL DEFAULT 0 CHECK (password_must_change IN (0, 1)),
	created_at           TEXT    NOT NULL,
	updated_at           TEXT    NOT NULL
) STRICT;

CREATE TABLE account_modules (
	account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	module     TEXT NOT NULL,
	PRIMARY KEY (account_id, module)
) STRICT;

CREATE TABLE sessions (
	token_hash TEXT PRIMARY KEY,
	account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
) STRICT;
CREATE INDEX sessions_account ON sessions(account_id);

CREATE TABLE login_flows (
	state         TEXT PRIMARY KEY,
	nonce         TEXT NOT NULL,
	code_verifier TEXT NOT NULL,
	created_at    TEXT NOT NULL
) STRICT;

CREATE TABLE links (
	id          TEXT    PRIMARY KEY,
	name        TEXT    NOT NULL,
	url         TEXT    NOT NULL,
	description TEXT    NOT NULL DEFAULT '',
	icon        TEXT    NOT NULL DEFAULT 'link',
	public      INTEGER NOT NULL DEFAULT 0 CHECK (public IN (0, 1)),
	position    INTEGER NOT NULL,
	created_at  TEXT    NOT NULL,
	updated_at  TEXT    NOT NULL
) STRICT;
`

const v2 = `
CREATE TABLE pages (
	id         TEXT    PRIMARY KEY,
	slug       TEXT    NOT NULL UNIQUE,
	title      TEXT    NOT NULL,
	body       TEXT    NOT NULL,
	version    INTEGER NOT NULL CHECK (version >= 1),
	owner_id   TEXT    NOT NULL REFERENCES accounts(id),
	read_all   INTEGER NOT NULL CHECK (read_all IN (0, 1)),
	write_all  INTEGER NOT NULL CHECK (write_all IN (0, 1)),
	created_at TEXT    NOT NULL,
	updated_at TEXT    NOT NULL,
	updated_by TEXT    NOT NULL REFERENCES accounts(id),
	deleted_at TEXT,
	deleted_by TEXT    REFERENCES accounts(id)
) STRICT;

CREATE TABLE page_access (
	page_id    TEXT NOT NULL REFERENCES pages(id),
	account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	kind       TEXT NOT NULL CHECK (kind IN ('read', 'write')),
	PRIMARY KEY (page_id, account_id, kind)
) STRICT;

CREATE TABLE page_revisions (
	page_id    TEXT    NOT NULL REFERENCES pages(id),
	version    INTEGER NOT NULL,
	title      TEXT    NOT NULL,
	body       TEXT    NOT NULL,
	author_id  TEXT    NOT NULL REFERENCES accounts(id),
	created_at TEXT    NOT NULL,
	PRIMARY KEY (page_id, version)
) STRICT;
`

// v3 creates the Arbeitswege tables.
const v3 = `
CREATE TABLE commute_years (
	id              TEXT    PRIMARY KEY,
	owner_id        TEXT    NOT NULL REFERENCES accounts(id),
	year            INTEGER NOT NULL CHECK (year BETWEEN 1900 AND 2200),
	version         INTEGER NOT NULL CHECK (version >= 1),
	car_dist_km     REAL    NOT NULL CHECK (car_dist_km >= 0),
	train_dist_km   REAL    NOT NULL CHECK (train_dist_km >= 0),
	bike_dist_km    REAL    NOT NULL CHECK (bike_dist_km >= 0),
	rate_low_cents  REAL,
	rate_high_cents REAL,
	threshold_km    REAL,
	created_at      TEXT    NOT NULL,
	updated_at      TEXT    NOT NULL,
	UNIQUE (owner_id, year)
) STRICT;

CREATE TABLE commute_months (
	year_id      TEXT    NOT NULL REFERENCES commute_years(id) ON DELETE CASCADE,
	month        INTEGER NOT NULL CHECK (month BETWEEN 1 AND 12),
	workdays     REAL    NOT NULL DEFAULT 0,
	holidays     REAL    NOT NULL DEFAULT 0,
	kurs         REAL    NOT NULL DEFAULT 0,
	kompensation REAL    NOT NULL DEFAULT 0,
	unfall       REAL    NOT NULL DEFAULT 0,
	krank        REAL    NOT NULL DEFAULT 0,
	bike         REAL    NOT NULL DEFAULT 0,
	car_train    REAL    NOT NULL DEFAULT 0,
	home_office  REAL    NOT NULL DEFAULT 0,
	PRIMARY KEY (year_id, month)
) STRICT;
`

const v5 = `
CREATE TABLE games (
	id         TEXT    PRIMARY KEY,
	player_a   TEXT    NOT NULL REFERENCES accounts(id),
	player_b   TEXT    NOT NULL REFERENCES accounts(id) CHECK (player_b != player_a),
	variant    TEXT    NOT NULL CHECK (variant IN ('klassisch', 'treffer-serie', 'salve', 'grosses-meer')),
	touching   INTEGER NOT NULL CHECK (touching IN (0, 1)),
	auto_water INTEGER NOT NULL CHECK (auto_water IN (0, 1)),
	status     TEXT    NOT NULL CHECK (status IN ('aufstellung', 'laeuft', 'beendet')),
	turn       TEXT    REFERENCES accounts(id),
	winner     TEXT    REFERENCES accounts(id),
	version    INTEGER NOT NULL,
	created_at TEXT    NOT NULL,
	updated_at TEXT    NOT NULL
) STRICT;
CREATE INDEX games_a ON games(player_a);
CREATE INDEX games_b ON games(player_b);

CREATE TABLE game_ships (
	game_id    TEXT    NOT NULL REFERENCES games(id),
	player_id  TEXT    NOT NULL REFERENCES accounts(id),
	idx        INTEGER NOT NULL,
	size       INTEGER NOT NULL CHECK (size BETWEEN 2 AND 5),
	x          INTEGER NOT NULL CHECK (x BETWEEN 0 AND 11),
	y          INTEGER NOT NULL CHECK (y BETWEEN 0 AND 11),
	horizontal INTEGER NOT NULL CHECK (horizontal IN (0, 1)),
	PRIMARY KEY (game_id, player_id, idx)
) STRICT;

CREATE TABLE game_shots (
	game_id TEXT    NOT NULL REFERENCES games(id),
	seq     INTEGER NOT NULL,
	shooter TEXT    NOT NULL REFERENCES accounts(id),
	x       INTEGER NOT NULL CHECK (x BETWEEN 0 AND 11),
	y       INTEGER NOT NULL CHECK (y BETWEEN 0 AND 11),
	result  TEXT    NOT NULL CHECK (result IN ('wasser', 'treffer', 'versenkt')),
	at      TEXT    NOT NULL,
	PRIMARY KEY (game_id, seq)
) STRICT;
`
