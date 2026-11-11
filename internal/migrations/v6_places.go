package migrations

// v6 creates the Orte tables. Until 0.3.0 it also imported the old places,
// lists and visits (#8). Points of old KMZ lines had no time; they keep
// their position in the track as seq.
const v6 = `
CREATE TABLE places (
	id          TEXT PRIMARY KEY,
	owner_id    TEXT NOT NULL REFERENCES accounts(id),
	name        TEXT NOT NULL CHECK (name != ''),
	description TEXT NOT NULL DEFAULT '',
	latitude    REAL NOT NULL CHECK (latitude BETWEEN -90 AND 90),
	longitude   REAL NOT NULL CHECK (longitude BETWEEN -180 AND 180),
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
) STRICT;

CREATE TABLE place_lists (
	id          TEXT PRIMARY KEY,
	owner_id    TEXT NOT NULL REFERENCES accounts(id),
	name        TEXT NOT NULL CHECK (name != ''),
	description TEXT NOT NULL DEFAULT '',
	date_from   TEXT NOT NULL DEFAULT '' CHECK (date_from = '' OR date(date_from) = date_from),
	date_to     TEXT NOT NULL DEFAULT '' CHECK (date_to = '' OR date(date_to) = date_to),
	source_file TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
) STRICT;

CREATE TABLE place_list_places (
	list_id  TEXT NOT NULL REFERENCES place_lists(id) ON DELETE CASCADE,
	place_id TEXT NOT NULL REFERENCES places(id),
	PRIMARY KEY (list_id, place_id)
) STRICT;

CREATE TABLE place_visits (
	id          TEXT PRIMARY KEY,
	owner_id    TEXT NOT NULL REFERENCES accounts(id),
	place_id    TEXT REFERENCES places(id),
	latitude    REAL NOT NULL CHECK (latitude BETWEEN -90 AND 90),
	longitude   REAL NOT NULL CHECK (longitude BETWEEN -180 AND 180),
	at          TEXT CHECK (at IS NULL OR julianday(at) IS NOT NULL),
	seq         INTEGER CHECK (seq >= 0),
	source_file TEXT NOT NULL DEFAULT '',
	CHECK ((at IS NULL) != (seq IS NULL)),
	UNIQUE (owner_id, source_file, at),
	UNIQUE (owner_id, source_file, seq)
) STRICT;
CREATE INDEX place_visits_owner_at ON place_visits(owner_id, at);
`
