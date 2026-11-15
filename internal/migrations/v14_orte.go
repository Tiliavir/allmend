package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // Europe/Zurich for the days of old points, also in tests

	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/id"
)

// v14 reworks Orte (ADR-20261115-orte, #6):
//
//   - place_imports records each location history import, so it can be
//     undone; place_points (location history) and place_stays (stays from
//     Timeline.json) belong to one import each.
//   - The GPS track of a trip moves from place_visits to place_track.
//   - place_lists (trips) get sha256 of the imported file (NULL for old
//     trips) and triggers: a trip imported from a file needs a full,
//     ordered range; changing the range of any trip needs one too. Old
//     trips keep an empty or incomplete range until edited.
//
// Existing data is moved, never dropped: the old visits of a trip's file
// become its track, all other visits become points of one "records" import
// per file. Their day is the day in Europe/Zurich (no offset is known).
// Anything the new model cannot hold aborts the migration with the ids.
func v14(ctx context.Context, tx db.Tx, env db.MigrationEnv) error {
	if err := v14Check(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, v14Schema); err != nil {
		return err
	}
	// Tracks of trips.
	if _, err := tx.ExecContext(ctx, `INSERT INTO place_track (list_id, seq, at, latitude, longitude)
SELECT l.id, row_number() OVER (PARTITION BY l.id ORDER BY julianday(v.at), v.seq, v.id) - 1, v.at, v.latitude, v.longitude
FROM place_lists l JOIN place_visits v ON v.owner_id = l.owner_id AND v.source_file = l.source_file
WHERE l.source_file != ''`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM place_visits WHERE EXISTS (
SELECT 1 FROM place_lists l WHERE l.owner_id = place_visits.owner_id AND l.source_file = place_visits.source_file AND l.source_file != '')`); err != nil {
		return err
	}
	var untimed int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM place_visits WHERE at IS NULL`).Scan(&untimed); err != nil {
		return err
	}
	if untimed > 0 {
		return fmt.Errorf("v14: %d points without time belong to no trip", untimed)
	}
	if err := v14Points(ctx, tx, env.Now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE place_visits`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, v14Triggers)
	return err
}

// v14Check refuses data the new model cannot hold.
func v14Check(ctx context.Context, tx db.Tx) error {
	var bad []string
	rows, err := tx.QueryContext(ctx, `SELECT id FROM place_lists
WHERE date_from != '' AND date_to != '' AND date_to < date_from ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			_ = rows.Close()
			return err
		}
		bad = append(bad, s)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("v14: trips whose range ends before it starts: %s", strings.Join(bad, ", "))
	}
	var linked int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM place_visits WHERE place_id IS NOT NULL`).Scan(&linked); err != nil {
		return err
	}
	if linked > 0 {
		return fmt.Errorf("v14: %d visits are linked to a place; the new model has no such link", linked)
	}
	return nil
}

// v14Points moves the remaining (timed) visits to place_points, one import
// per owner and file, in batches.
func v14Points(ctx context.Context, tx db.Tx, now time.Time) error {
	zurich, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		return err
	}
	type src struct{ owner, file string }
	var srcs []src
	rows, err := tx.QueryContext(ctx, `SELECT owner_id, source_file FROM place_visits GROUP BY owner_id, source_file ORDER BY owner_id, source_file`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s src
		if err := rows.Scan(&s.owner, &s.file); err != nil {
			_ = rows.Close()
			return err
		}
		srcs = append(srcs, s)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	at := now.UTC().Format(time.RFC3339Nano)
	for _, s := range srcs {
		imp := id.New()
		name := s.file
		if name == "" {
			name = "Standortverlauf"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO place_imports (id, owner_id, kind, file_name, imported_at, points, stays, skipped)
SELECT ?, ?, 'records', ?, ?, count(*), 0, 0 FROM place_visits WHERE owner_id = ? AND source_file = ?`, imp, s.owner, name, at, s.owner, s.file); err != nil {
			return err
		}
		if err := v14Copy(ctx, tx, s.owner, s.file, imp, zurich); err != nil {
			return err
		}
	}
	return nil
}

type v14Visit struct {
	rowid    int64
	at       string
	lat, lon float64
}

func v14Copy(ctx context.Context, tx db.Tx, owner, file, imp string, zurich *time.Location) error {
	var last int64 = -1
	for {
		batch, err := v14Batch(ctx, tx, owner, file, last)
		if err != nil || len(batch) == 0 {
			return err
		}
		for _, v := range batch {
			t, err := time.Parse(time.RFC3339Nano, v.at)
			if err != nil {
				return fmt.Errorf("v14: visit time %q: %w", v.at, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO place_points (owner_id, import_id, at, utc_offset_min, day, latitude, longitude) VALUES (?, ?, ?, NULL, ?, ?, ?)`,
				owner, imp, t.UTC().Format(v14Time), t.In(zurich).Format(time.DateOnly), v.lat, v.lon); err != nil {
				return err
			}
			last = v.rowid
		}
	}
}

func v14Batch(ctx context.Context, tx db.Tx, owner, file string, after int64) ([]v14Visit, error) {
	rows, err := tx.QueryContext(ctx, `SELECT rowid, at, latitude, longitude FROM place_visits
WHERE owner_id = ? AND source_file = ? AND rowid > ? ORDER BY rowid LIMIT 5000`, owner, file, after)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []v14Visit
	for rows.Next() {
		var v v14Visit
		var at sql.NullString
		if err := rows.Scan(&v.rowid, &at, &v.lat, &v.lon); err != nil {
			return nil, err
		}
		v.at = at.String
		out = append(out, v)
	}
	return out, rows.Err()
}

// v14Time is the fixed-width UTC format of place_points.at and friends, so
// text order is time order.
const v14Time = "2006-01-02T15:04:05.000Z"

const v14Schema = `
CREATE TABLE place_imports (
	id          TEXT PRIMARY KEY,
	owner_id    TEXT NOT NULL REFERENCES accounts(id),
	kind        TEXT NOT NULL CHECK (kind IN ('timeline', 'records')),
	file_name   TEXT NOT NULL CHECK (file_name != ''),
	imported_at TEXT NOT NULL,
	points      INTEGER NOT NULL CHECK (points >= 0),
	stays       INTEGER NOT NULL CHECK (stays >= 0),
	skipped     INTEGER NOT NULL CHECK (skipped >= 0)
) STRICT;
CREATE INDEX place_imports_owner ON place_imports(owner_id, imported_at);

CREATE TABLE place_points (
	id             INTEGER PRIMARY KEY,
	owner_id       TEXT NOT NULL REFERENCES accounts(id),
	import_id      TEXT NOT NULL REFERENCES place_imports(id) ON DELETE CASCADE,
	at             TEXT NOT NULL CHECK (length(at) = 24 AND julianday(at) IS NOT NULL),
	utc_offset_min INTEGER CHECK (utc_offset_min BETWEEN -840 AND 840),
	day            TEXT NOT NULL CHECK (date(day) = day),
	latitude       REAL NOT NULL CHECK (latitude BETWEEN -90 AND 90),
	longitude      REAL NOT NULL CHECK (longitude BETWEEN -180 AND 180)
) STRICT;
CREATE INDEX place_points_owner_day ON place_points(owner_id, day, at);
CREATE INDEX place_points_owner_at ON place_points(owner_id, at);
CREATE INDEX place_points_import ON place_points(import_id);

CREATE TABLE place_stays (
	id             INTEGER PRIMARY KEY,
	owner_id       TEXT NOT NULL REFERENCES accounts(id),
	import_id      TEXT NOT NULL REFERENCES place_imports(id) ON DELETE CASCADE,
	start_at       TEXT NOT NULL CHECK (length(start_at) = 24 AND julianday(start_at) IS NOT NULL),
	end_at         TEXT NOT NULL CHECK (length(end_at) = 24 AND julianday(end_at) >= julianday(start_at)),
	utc_offset_min INTEGER NOT NULL CHECK (utc_offset_min BETWEEN -840 AND 840),
	day            TEXT NOT NULL CHECK (date(day) = day),
	end_day        TEXT NOT NULL CHECK (date(end_day) = end_day AND end_day >= day),
	latitude       REAL NOT NULL CHECK (latitude BETWEEN -90 AND 90),
	longitude      REAL NOT NULL CHECK (longitude BETWEEN -180 AND 180),
	semantic_type  TEXT NOT NULL CHECK (semantic_type != '' AND semantic_type NOT GLOB '*[^A-Z_]*')
) STRICT;
CREATE INDEX place_stays_owner_day ON place_stays(owner_id, day, start_at);
CREATE INDEX place_stays_owner_start ON place_stays(owner_id, start_at);
CREATE INDEX place_stays_import ON place_stays(import_id);

CREATE TABLE place_track (
	list_id   TEXT NOT NULL REFERENCES place_lists(id) ON DELETE CASCADE,
	seq       INTEGER NOT NULL CHECK (seq >= 0),
	at        TEXT CHECK (at IS NULL OR julianday(at) IS NOT NULL),
	latitude  REAL NOT NULL CHECK (latitude BETWEEN -90 AND 90),
	longitude REAL NOT NULL CHECK (longitude BETWEEN -180 AND 180),
	PRIMARY KEY (list_id, seq)
) STRICT;

ALTER TABLE place_lists ADD COLUMN sha256 TEXT CHECK (sha256 IS NULL OR (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'));
CREATE UNIQUE INDEX place_lists_owner_sha256 ON place_lists(owner_id, sha256) WHERE sha256 IS NOT NULL;
`

const v14Triggers = `
CREATE TRIGGER place_lists_range_insert BEFORE INSERT ON place_lists
WHEN NEW.date_from = '' OR NEW.date_to = '' OR NEW.date_to < NEW.date_from
BEGIN SELECT RAISE(ABORT, 'place_lists: a new trip needs a full range'); END;

CREATE TRIGGER place_lists_range_update BEFORE UPDATE OF date_from, date_to ON place_lists
WHEN NEW.date_from = '' OR NEW.date_to = '' OR NEW.date_to < NEW.date_from
BEGIN SELECT RAISE(ABORT, 'place_lists: a changed range must be full'); END;
`
