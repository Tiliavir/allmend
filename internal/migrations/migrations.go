// Package migrations lists every schema step of Allmend in order.
//
// Rules: never change or remove a step that was released; add a new one.
// A step that creates a module's tables also imports that module's legacy
// data (old almanach.db, attached read-only as "legacy") in the same step.
package migrations

import (
	"context"

	"github.com/tiliavir/allmend/internal/db"
)

// All returns the ordered migration list.
func All() []db.Migration {
	return []db.Migration{
		{Version: 1, Name: "accounts_sessions_links", Up: db.SQL(v1)},
		{Version: 2, Name: "pages", Up: db.SQL(v2)},
		{Version: 3, Name: "commute", Up: v3},
		{Version: 4, Name: "velo", Up: v4},
		{Version: 5, Name: "schiffe", Up: db.SQL(v5)},
		{Version: 6, Name: "places", Up: v6},
		{Version: 7, Name: "finanzen", Up: v7},
		{Version: 8, Name: "ausgaben", Up: db.SQL(v8)},
		{Version: 9, Name: "page_tree", Up: db.SQL(v9)},
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

const v3Schema = `
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

// v3 creates the commute tables and imports legacy commute_years. The old app
// applied defaults on read (distances 7.6/15/18 km when 0, statutory rates
// when both rates were 0, threshold 20 when 0); the import stores the values
// the old app showed.
func v3(ctx context.Context, tx db.Tx, env db.MigrationEnv) error {
	if _, err := tx.ExecContext(ctx, v3Schema); err != nil {
		return err
	}
	ok, err := legacyTable(ctx, tx, env, "commute_years")
	if err != nil || !ok {
		return err
	}
	dist := func(f string) string {
		return "(json_extract(data, '$." + f + "') IS NULL OR json_type(data, '$." + f + "') IN ('integer', 'real'))"
	}
	if err := reject(ctx, tx, "legacy.commute_years", "year missing, or a distance or rate is not a number",
		"json_type(data, '$.year') IS NOT 'integer' OR NOT "+dist("car_dist_km")+" OR NOT "+dist("train_dist_km")+" OR NOT "+dist("bike_dist_km")+
			" OR NOT "+dist("pendler_low_cents")+" OR NOT "+dist("pendler_high_cents")+" OR NOT "+dist("pendler_threshold_km")); err != nil {
		return err
	}
	owner, err := adminID(ctx, tx, env)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO commute_years (id, owner_id, year, version, car_dist_km, train_dist_km, bike_dist_km,
	rate_low_cents, rate_high_cents, threshold_km, created_at, updated_at)
SELECT id, ?, CAST(json_extract(data, '$.year') AS INTEGER), 1,
	COALESCE(NULLIF(json_extract(data, '$.car_dist_km'), 0), 7.6),
	COALESCE(NULLIF(json_extract(data, '$.train_dist_km'), 0), 15.0),
	COALESCE(NULLIF(json_extract(data, '$.bike_dist_km'), 0), 18.0),
	CASE WHEN COALESCE(json_extract(data, '$.pendler_low_cents'), 0) = 0 AND COALESCE(json_extract(data, '$.pendler_high_cents'), 0) = 0
		THEN NULL ELSE CAST(COALESCE(json_extract(data, '$.pendler_low_cents'), 0) AS REAL) END,
	CASE WHEN COALESCE(json_extract(data, '$.pendler_low_cents'), 0) = 0 AND COALESCE(json_extract(data, '$.pendler_high_cents'), 0) = 0
		THEN NULL ELSE CAST(COALESCE(json_extract(data, '$.pendler_high_cents'), 0) AS REAL) END,
	CAST(NULLIF(json_extract(data, '$.pendler_threshold_km'), 0) AS REAL),
	created_at, updated_at
FROM legacy.commute_years`, owner); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
WITH m(i) AS (VALUES (0),(1),(2),(3),(4),(5),(6),(7),(8),(9),(10),(11))
INSERT INTO commute_months (year_id, month, workdays, holidays, kurs, kompensation, unfall, krank, bike, car_train, home_office)
SELECT y.id, m.i + 1,
	COALESCE(json_extract(y.data, '$.workdays[' || m.i || ']'), 0),
	COALESCE(json_extract(y.data, '$.holidays[' || m.i || ']'), 0),
	COALESCE(json_extract(y.data, '$.kurs[' || m.i || ']'), 0),
	COALESCE(json_extract(y.data, '$.kompensation[' || m.i || ']'), 0),
	COALESCE(json_extract(y.data, '$.unfall[' || m.i || ']'), 0),
	COALESCE(json_extract(y.data, '$.krank[' || m.i || ']'), 0),
	COALESCE(json_extract(y.data, '$.bike[' || m.i || ']'), 0),
	COALESCE(json_extract(y.data, '$.car_train[' || m.i || ']'), 0),
	COALESCE(json_extract(y.data, '$.ho[' || m.i || ']'), 0)
FROM legacy.commute_years y CROSS JOIN m`); err != nil {
		return err
	}
	return sameCount(ctx, tx, "commute_years", "legacy.commute_years", "commute_years WHERE owner_id = ?", owner)
}

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
