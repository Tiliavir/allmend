package migrations

// v10 adds the "Seeminen" variant of Schiffe versenken: the variant and the
// shot result "mine" join the CHECK constraints, a shot names the firing
// ship, and each player's mines are stored. Existing rows are copied
// unchanged; their shots name no ship.
//
// SQLite changes CHECK constraints only by rebuilding a table, and foreign
// keys stay on inside the migration transaction. So games and both child
// tables are rebuilt together: the new children reference the new games
// table, the old tables are dropped children first, and the renames point
// every reference back to "games". No foreign key is violated at any step.
const v10 = `
CREATE TABLE games_v10 (
	id         TEXT    PRIMARY KEY,
	player_a   TEXT    NOT NULL REFERENCES accounts(id),
	player_b   TEXT    NOT NULL REFERENCES accounts(id) CHECK (player_b != player_a),
	variant    TEXT    NOT NULL CHECK (variant IN ('klassisch', 'treffer-serie', 'salve', 'grosses-meer', 'seeminen')),
	touching   INTEGER NOT NULL CHECK (touching IN (0, 1)),
	auto_water INTEGER NOT NULL CHECK (auto_water IN (0, 1)),
	status     TEXT    NOT NULL CHECK (status IN ('aufstellung', 'laeuft', 'beendet')),
	turn       TEXT    REFERENCES accounts(id),
	winner     TEXT    REFERENCES accounts(id),
	version    INTEGER NOT NULL,
	created_at TEXT    NOT NULL,
	updated_at TEXT    NOT NULL
) STRICT;

CREATE TABLE game_ships_v10 (
	game_id    TEXT    NOT NULL REFERENCES games_v10(id),
	player_id  TEXT    NOT NULL REFERENCES accounts(id),
	idx        INTEGER NOT NULL,
	size       INTEGER NOT NULL CHECK (size BETWEEN 2 AND 5),
	x          INTEGER NOT NULL CHECK (x BETWEEN 0 AND 11),
	y          INTEGER NOT NULL CHECK (y BETWEEN 0 AND 11),
	horizontal INTEGER NOT NULL CHECK (horizontal IN (0, 1)),
	PRIMARY KEY (game_id, player_id, idx)
) STRICT;

CREATE TABLE game_shots_v10 (
	game_id TEXT    NOT NULL REFERENCES games_v10(id),
	seq     INTEGER NOT NULL,
	shooter TEXT    NOT NULL REFERENCES accounts(id),
	x       INTEGER NOT NULL CHECK (x BETWEEN 0 AND 11),
	y       INTEGER NOT NULL CHECK (y BETWEEN 0 AND 11),
	ship    INTEGER CHECK (ship BETWEEN 0 AND 9),
	result  TEXT    NOT NULL CHECK (result IN ('wasser', 'treffer', 'versenkt', 'mine')),
	at      TEXT    NOT NULL,
	PRIMARY KEY (game_id, seq),
	CHECK (result != 'mine' OR ship IS NOT NULL)
) STRICT;

INSERT INTO games_v10 (id, player_a, player_b, variant, touching, auto_water, status, turn, winner, version, created_at, updated_at)
	SELECT id, player_a, player_b, variant, touching, auto_water, status, turn, winner, version, created_at, updated_at FROM games;
INSERT INTO game_ships_v10 (game_id, player_id, idx, size, x, y, horizontal)
	SELECT game_id, player_id, idx, size, x, y, horizontal FROM game_ships;
INSERT INTO game_shots_v10 (game_id, seq, shooter, x, y, ship, result, at)
	SELECT game_id, seq, shooter, x, y, NULL, result, at FROM game_shots;

DROP TABLE game_shots;
DROP TABLE game_ships;
DROP TABLE games;
ALTER TABLE games_v10 RENAME TO games;
ALTER TABLE game_ships_v10 RENAME TO game_ships;
ALTER TABLE game_shots_v10 RENAME TO game_shots;
CREATE INDEX games_a ON games(player_a);
CREATE INDEX games_b ON games(player_b);

CREATE TABLE game_mines (
	game_id   TEXT    NOT NULL REFERENCES games(id),
	player_id TEXT    NOT NULL REFERENCES accounts(id),
	idx       INTEGER NOT NULL CHECK (idx BETWEEN 0 AND 2),
	x         INTEGER NOT NULL CHECK (x BETWEEN 0 AND 11),
	y         INTEGER NOT NULL CHECK (y BETWEEN 0 AND 11),
	PRIMARY KEY (game_id, player_id, idx),
	UNIQUE (game_id, player_id, x, y)
) STRICT;
`
