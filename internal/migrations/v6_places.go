package migrations

import (
	"context"
	"fmt"

	"github.com/tiliavir/allmend/internal/db"
)

const v6Schema = `
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
	at          TEXT NOT NULL CHECK (julianday(at) IS NOT NULL),
	source_file TEXT NOT NULL DEFAULT '',
	UNIQUE (owner_id, source_file, at)
) STRICT;
CREATE INDEX place_visits_owner_at ON place_visits(owner_id, at);
`

// v6 creates the Orte tables and imports places, lists, list membership and
// visits. Unexpected legacy data stops the migration and names the rows.
func v6(ctx context.Context, tx db.Tx, env db.MigrationEnv) error {
	if _, err := tx.ExecContext(ctx, v6Schema); err != nil {
		return err
	}
	has := map[string]bool{}
	any := false
	for _, t := range []string{"places", "place_lists", "place_list_places", "place_visits"} {
		ok, err := legacyTable(ctx, tx, env, t)
		if err != nil {
			return err
		}
		has[t], any = ok, any || ok
	}
	if !any {
		return nil
	}
	owner, err := adminID(ctx, tx, env)
	if err != nil {
		return err
	}
	j := func(f string) string { return "json_extract(data, '$." + f + "')" }
	txt := func(f string) string { return "COALESCE(CAST(" + j(f) + " AS TEXT), '')" }
	num := func(f string) string { return "json_type(data, '$." + f + "') IN ('integer', 'real')" }
	coords := "NOT " + num("latitude") + " OR NOT " + num("longitude") + " OR abs(" + j("latitude") + ") > 90 OR abs(" + j("longitude") + ") > 180"
	optDate := func(f string) string {
		return "(" + txt(f) + " != '' AND date(" + txt(f) + ") IS NOT " + txt(f) + ")"
	}
	steps := []struct {
		table, problem, cond, insert string
	}{
		{"places", "name missing or coordinates invalid", "trim(" + txt("name") + ") = '' OR " + coords,
			`INSERT INTO places (id, owner_id, name, description, latitude, longitude, created_at, updated_at)
SELECT id, ?1, ` + txt("name") + `, ` + txt("description") + `, ` + j("latitude") + `, ` + j("longitude") + `, created_at, updated_at FROM legacy.places`},
		{"place_lists", "name missing or dates invalid", "trim(" + txt("name") + ") = '' OR " + optDate("date_from") + " OR " + optDate("date_to"),
			`INSERT INTO place_lists (id, owner_id, name, description, date_from, date_to, source_file, created_at, updated_at)
SELECT id, ?1, ` + txt("name") + `, ` + txt("description") + `, ` + txt("date_from") + `, ` + txt("date_to") + `, ` + txt("source_file") + `, created_at, updated_at FROM legacy.place_lists`},
		{"place_list_places", "unknown list or place", j("place_list_id") + " NOT IN (SELECT id FROM place_lists) OR " + j("place_id") + " NOT IN (SELECT id FROM places) OR " + j("place_list_id") + " IS NULL OR " + j("place_id") + " IS NULL",
			`INSERT INTO place_list_places (list_id, place_id) SELECT ` + j("place_list_id") + `, ` + j("place_id") + ` FROM legacy.place_list_places`},
		{"place_visits", "coordinates or timestamp invalid, or unknown place", coords + " OR julianday(" + j("timestamp") + ") IS NULL OR (" + txt("place_id") + " != '' AND " + j("place_id") + " NOT IN (SELECT id FROM places))",
			`INSERT INTO place_visits (id, owner_id, place_id, latitude, longitude, at, source_file)
SELECT id, ?1, NULLIF(` + txt("place_id") + `, ''), ` + j("latitude") + `, ` + j("longitude") + `, strftime('%Y-%m-%dT%H:%M:%fZ', ` + j("timestamp") + `), ` + txt("source_file") + ` FROM legacy.place_visits`},
	}
	for _, st := range steps {
		if !has[st.table] {
			continue
		}
		if err := reject(ctx, tx, "legacy."+st.table, st.problem, st.cond); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, st.insert, owner); err != nil {
			return fmt.Errorf("%s: %w", st.table, err)
		}
		target := st.table
		if st.table == "place_list_places" {
			target = "place_list_places WHERE ?1 IS NOT NULL" // no owner column; counted in full
		} else {
			target += " WHERE owner_id = ?1"
		}
		if err := sameCount(ctx, tx, st.table, "legacy."+st.table, target, owner); err != nil {
			return err
		}
	}
	return nil
}
