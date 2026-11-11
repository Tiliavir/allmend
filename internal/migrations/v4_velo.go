package migrations

// v4 creates the Velo tables. Until 0.3.0 it also imported the old bike
// data (#8); databases migrated then keep it.
const v4 = `
CREATE TABLE velo_bikes (
	id                   TEXT    PRIMARY KEY,
	owner_id             TEXT    NOT NULL REFERENCES accounts(id),
	name                 TEXT    NOT NULL,
	brand                TEXT    NOT NULL DEFAULT '',
	model                TEXT    NOT NULL DEFAULT '',
	acquired             TEXT    NOT NULL DEFAULT '' CHECK (acquired = '' OR date(acquired) = acquired),
	purchase_cents       INTEGER NOT NULL DEFAULT 0 CHECK (purchase_cents >= 0),
	accessories_cents    INTEGER NOT NULL DEFAULT 0 CHECK (accessories_cents >= 0),
	notes                TEXT    NOT NULL DEFAULT '',
	color                TEXT    NOT NULL DEFAULT '',
	version              INTEGER NOT NULL,
	created_at           TEXT    NOT NULL,
	updated_at           TEXT    NOT NULL
) STRICT;
CREATE INDEX velo_bikes_owner ON velo_bikes(owner_id);

CREATE TABLE velo_tours (
	id          TEXT    PRIMARY KEY,
	owner_id    TEXT    NOT NULL REFERENCES accounts(id),
	bike_id     TEXT    REFERENCES velo_bikes(id),
	date        TEXT    NOT NULL CHECK (date(date) = date),
	distance_km REAL    NOT NULL CHECK (distance_km >= 0),
	elevation_m INTEGER NOT NULL DEFAULT 0,
	title       TEXT    NOT NULL DEFAULT '',
	description TEXT    NOT NULL DEFAULT '',
	source_file TEXT    NOT NULL DEFAULT '',
	version     INTEGER NOT NULL,
	created_at  TEXT    NOT NULL,
	updated_at  TEXT    NOT NULL
) STRICT;
CREATE INDEX velo_tours_owner_date ON velo_tours(owner_id, date);

CREATE TABLE velo_repairs (
	id          TEXT    PRIMARY KEY,
	owner_id    TEXT    NOT NULL REFERENCES accounts(id),
	bike_id     TEXT    NOT NULL REFERENCES velo_bikes(id),
	date        TEXT    NOT NULL CHECK (date(date) = date),
	cost_cents  INTEGER NOT NULL CHECK (cost_cents >= 0),
	title       TEXT    NOT NULL,
	comment     TEXT    NOT NULL DEFAULT '',
	odometer_km REAL,
	version     INTEGER NOT NULL,
	created_at  TEXT    NOT NULL,
	updated_at  TEXT    NOT NULL
) STRICT;

CREATE TABLE velo_year_comments (
	owner_id   TEXT    NOT NULL REFERENCES accounts(id),
	year       INTEGER NOT NULL,
	comment    TEXT    NOT NULL,
	updated_at TEXT    NOT NULL,
	PRIMARY KEY (owner_id, year)
) STRICT;

CREATE TABLE velo_goals (
	owner_id   TEXT    NOT NULL REFERENCES accounts(id),
	year       INTEGER NOT NULL,
	month      INTEGER NOT NULL CHECK (month BETWEEN 1 AND 12),
	target_km  REAL    NOT NULL CHECK (target_km > 0),
	updated_at TEXT    NOT NULL,
	PRIMARY KEY (owner_id, year, month)
) STRICT;
`
