package migrations

import (
	"context"
	"fmt"
	"strings"

	"github.com/tiliavir/allmend/internal/db"
)

const v4Schema = `
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

// v4 creates the Velo tables and imports the old bike data. Unexpected data
// (invalid dates, unknown bikes, wrong types, a currency other than the base
// currency) stops the migration and names the rows: nothing is guessed.
// Amounts are in the base currency (ALLMEND_BASE_CURRENCY).
func v4(ctx context.Context, tx db.Tx, env db.MigrationEnv) error {
	if _, err := tx.ExecContext(ctx, v4Schema); err != nil {
		return err
	}
	has := map[string]bool{}
	for _, t := range []string{"bikes", "tours", "repairs", "bike_year_comments", "bike_challenges"} {
		ok, err := legacyTable(ctx, tx, env, t)
		if err != nil {
			return err
		}
		has[t] = ok
	}
	if !has["bikes"] && !has["tours"] && !has["repairs"] && !has["bike_year_comments"] && !has["bike_challenges"] {
		return nil
	}
	owner, err := adminID(ctx, tx, env)
	if err != nil {
		return err
	}
	cur := env.BaseCurrency
	if cur == "" {
		cur = "EUR"
	}
	j := func(f string) string { return "json_extract(data, '$." + f + "')" }
	txt := func(f string) string { return "COALESCE(CAST(" + j(f) + " AS TEXT), '')" }
	isInt := func(f string) string { return "json_type(data, '$." + f + "') = 'integer'" }
	isNum := func(f string) string { return "json_type(data, '$." + f + "') IN ('integer', 'real')" }
	optInt := func(f string) string { return "(" + j(f) + " IS NULL OR " + isInt(f) + ")" }
	optCur := func(f string) string { return "COALESCE(" + j(f) + ", '') IN ('', '" + cur + "')" }

	if has["bikes"] {
		if err := checkDates(ctx, tx, "legacy.bikes", j("acquisition_date"), true); err != nil {
			return err
		}
		if err := reject(ctx, tx, "legacy.bikes", "name missing, amount not an integer in minor units, or currency other than "+cur,
			"trim("+txt("name")+") = '' OR NOT "+optInt("purchase_price_minor")+" OR NOT "+optInt("accessories_price_minor")+
				" OR NOT "+optCur("purchase_price_currency")+" OR NOT "+optCur("accessories_price_currency")+
				" OR NOT ("+txt("color")+" = '' OR lower("+txt("color")+") GLOB '#[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]')"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO velo_bikes (id, owner_id, name, brand, model, acquired, purchase_cents, accessories_cents, notes, color, version, created_at, updated_at)
SELECT id, ?, `+txt("name")+`, `+txt("brand")+`, `+txt("model")+`, `+txt("acquisition_date")+`,
	COALESCE(`+j("purchase_price_minor")+`, 0), COALESCE(`+j("accessories_price_minor")+`, 0),
	`+txt("notes")+`, lower(`+txt("color")+`), 1, created_at, updated_at
FROM legacy.bikes`, owner); err != nil {
			return fmt.Errorf("bikes: %w", err)
		}
		if err := sameCount(ctx, tx, "bikes", "legacy.bikes", "velo_bikes WHERE owner_id = ?", owner); err != nil {
			return err
		}
	}
	if has["tours"] {
		if err := checkDates(ctx, tx, "legacy.tours", j("date"), false); err != nil {
			return err
		}
		if err := reject(ctx, tx, "legacy.tours", "distance missing or negative, elevation not a whole number, or bike unknown",
			"NOT "+isNum("distance_km")+" OR "+j("distance_km")+" < 0 OR NOT "+optInt("elevation_m")+
				" OR (COALESCE("+j("bike_id")+", '') != '' AND "+j("bike_id")+" NOT IN (SELECT id FROM velo_bikes))"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO velo_tours (id, owner_id, bike_id, date, distance_km, elevation_m, title, description, source_file, version, created_at, updated_at)
SELECT id, ?, NULLIF(`+txt("bike_id")+`, ''), `+j("date")+`, `+j("distance_km")+`,
	COALESCE(`+j("elevation_m")+`, 0), `+txt("title")+`, `+txt("description")+`, `+txt("source_file")+`,
	1, created_at, updated_at
FROM legacy.tours`, owner); err != nil {
			return fmt.Errorf("tours: %w", err)
		}
		if err := sameCount(ctx, tx, "tours", "legacy.tours", "velo_tours WHERE owner_id = ?", owner); err != nil {
			return err
		}
	}
	if has["repairs"] {
		if err := checkDates(ctx, tx, "legacy.repairs", j("date"), false); err != nil {
			return err
		}
		if err := reject(ctx, tx, "legacy.repairs", "bike unknown, title missing, cost not an integer in minor units, currency other than "+cur+", or odometer not a number",
			j("bike_id")+" IS NULL OR "+j("bike_id")+" NOT IN (SELECT id FROM velo_bikes) OR trim("+txt("title")+") = ''"+
				" OR NOT "+isInt("cost_minor")+" OR "+j("cost_minor")+" < 0 OR NOT "+optCur("cost_currency")+
				" OR NOT ("+j("odometer_km")+" IS NULL OR "+isNum("odometer_km")+")"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO velo_repairs (id, owner_id, bike_id, date, cost_cents, title, comment, odometer_km, version, created_at, updated_at)
SELECT id, ?, `+j("bike_id")+`, `+j("date")+`, `+j("cost_minor")+`, `+txt("title")+`, `+txt("comment")+`,
	CAST(`+j("odometer_km")+` AS REAL), 1, created_at, updated_at
FROM legacy.repairs`, owner); err != nil {
			return fmt.Errorf("repairs: %w", err)
		}
		if err := sameCount(ctx, tx, "repairs", "legacy.repairs", "velo_repairs WHERE owner_id = ?", owner); err != nil {
			return err
		}
	}
	if has["bike_year_comments"] {
		if err := reject(ctx, tx, "legacy.bike_year_comments", "year is not a four-digit year",
			"NOT ("+txt("year")+" GLOB '[12][0-9][0-9][0-9]')"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO velo_year_comments (owner_id, year, comment, updated_at)
SELECT ?, CAST(`+j("year")+` AS INTEGER), `+txt("comment")+`, updated_at FROM legacy.bike_year_comments`, owner); err != nil {
			return fmt.Errorf("year comments: %w", err)
		}
		if err := sameCount(ctx, tx, "year comments", "legacy.bike_year_comments", "velo_year_comments WHERE owner_id = ?", owner); err != nil {
			return err
		}
	}
	if has["bike_challenges"] {
		// A target of 0 meant "no goal" in the old app; such rows are not goals.
		if err := reject(ctx, tx, "legacy.bike_challenges", "year, month (1–12) or target (number ≥ 0) invalid",
			"NOT ("+txt("year")+" GLOB '[12][0-9][0-9][0-9]') OR CAST("+j("month")+" AS INTEGER) NOT BETWEEN 1 AND 12"+
				" OR CAST(CAST("+j("month")+" AS INTEGER) AS TEXT) != ltrim("+txt("month")+", '0')"+
				" OR NOT "+isNum("target_km")+" OR "+j("target_km")+" < 0"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO velo_goals (owner_id, year, month, target_km, updated_at)
SELECT ?, CAST(`+j("year")+` AS INTEGER), CAST(`+j("month")+` AS INTEGER), CAST(`+j("target_km")+` AS REAL), updated_at
FROM legacy.bike_challenges WHERE `+j("target_km")+` > 0`, owner); err != nil {
			return fmt.Errorf("goals: %w", err)
		}
		if err := sameCount(ctx, tx, "goals", "legacy.bike_challenges WHERE json_extract(data, '$.target_km') > 0", "velo_goals WHERE owner_id = ?", owner); err != nil {
			return err
		}
	}
	return nil
}

// reject fails the migration if any row of table matches cond, naming up to
// 20 ids: unexpected legacy data is an error, never guessed or dropped.
func reject(ctx context.Context, tx db.Tx, table, problem, cond string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM `+table+` WHERE `+cond+` LIMIT 20`)
	if err != nil {
		return fmt.Errorf("legacy check %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	var bad []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		bad = append(bad, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("legacy import %s: %s (fix in the old app, then retry): %s", table, problem, strings.Join(bad, ", "))
	}
	return nil
}

// checkDates fails with the offending ids if expr is not a valid YYYY-MM-DD.
func checkDates(ctx context.Context, tx db.Tx, table, expr string, optional bool) error {
	cond := expr + " IS NULL OR date(" + expr + ") IS NOT " + expr
	if optional {
		cond = "COALESCE(" + expr + ", '') != '' AND date(" + expr + ") IS NOT " + expr
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(CAST(`+expr+` AS TEXT), 'leer') FROM `+table+` WHERE `+cond+` LIMIT 20`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var bad []string
	for rows.Next() {
		var id, v string
		if err := rows.Scan(&id, &v); err != nil {
			return err
		}
		bad = append(bad, id+"="+v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("legacy import %s: invalid dates (fix them in the old app, then retry): %s", table, strings.Join(bad, ", "))
	}
	return nil
}
