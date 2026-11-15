package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tiliavir/allmend/internal/places"
)

// Places implements places.Repository.
type Places struct{ s *Store }

// Places returns the Orte repository.
func (s *Store) Places() *Places { return &Places{s} }

// pointTime is the fixed-width UTC format of place_points.at and
// place_stays.*_at, so text order is time order.
const pointTime = "2006-01-02T15:04:05.000Z"

func fmtPoint(t time.Time) string { return t.UTC().Format(pointTime) }

// ── trips ─────────────────────────────────────────────

const listCols = `l.id, l.owner_id, l.name, l.description, l.date_from, l.date_to, l.source_file, COALESCE(l.sha256, ''),
	(SELECT COUNT(*) FROM place_list_places p WHERE p.list_id = l.id), (SELECT COUNT(*) FROM place_track t WHERE t.list_id = l.id)`

func scanList(row interface{ Scan(...any) error }) (places.List, error) {
	var l places.List
	err := row.Scan(&l.ID, &l.OwnerID, &l.Name, &l.Description, &l.DateFrom, &l.DateTo, &l.SourceFile, &l.SHA256, &l.PlaceCount, &l.TrackCount)
	return l, err
}

// Lists returns an owner's trips, newest range first; trips without range last.
func (r *Places) Lists(ctx context.Context, owner string) ([]places.List, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+listCols+` FROM place_lists l WHERE l.owner_id = ?
ORDER BY l.date_from = '', l.date_from DESC, l.name COLLATE NOCASE`, owner)
	if err != nil {
		return nil, fmt.Errorf("store.Places.Lists: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []places.List
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// List returns one trip of an owner with its places.
func (r *Places) List(ctx context.Context, owner, id string) (places.List, []places.Place, error) {
	l, err := scanList(r.s.db.QueryRowContext(ctx, `SELECT `+listCols+` FROM place_lists l WHERE l.owner_id = ? AND l.id = ?`, owner, id))
	if errors.Is(err, sql.ErrNoRows) {
		return l, nil, places.ErrNotFound
	}
	if err != nil {
		return l, nil, fmt.Errorf("store.Places.List: %w", err)
	}
	rows, err := r.s.db.QueryContext(ctx, `SELECT p.id, p.owner_id, p.name, p.description, p.latitude, p.longitude
FROM places p JOIN place_list_places lp ON lp.place_id = p.id WHERE lp.list_id = ? ORDER BY p.rowid`, id)
	if err != nil {
		return l, nil, err
	}
	defer func() { _ = rows.Close() }()
	var ps []places.Place
	for rows.Next() {
		var p places.Place
		if err := rows.Scan(&p.ID, &p.OwnerID, &p.Name, &p.Description, &p.Latitude, &p.Longitude); err != nil {
			return l, nil, err
		}
		ps = append(ps, p)
	}
	return l, ps, rows.Err()
}

// Track returns a trip's track in order.
func (r *Places) Track(ctx context.Context, owner, listID string) ([]places.TrackPoint, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT t.at, t.latitude, t.longitude FROM place_track t JOIN place_lists l ON l.id = t.list_id
WHERE l.owner_id = ? AND t.list_id = ? ORDER BY t.seq`, owner, listID)
	if err != nil {
		return nil, fmt.Errorf("store.Places.Track: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []places.TrackPoint
	for rows.Next() {
		var p places.TrackPoint
		var at sql.NullString
		if err := rows.Scan(&at, &p.Latitude, &p.Longitude); err != nil {
			return nil, err
		}
		if at.Valid {
			if p.At, err = time.Parse(time.RFC3339Nano, at.String); err != nil {
				return nil, fmt.Errorf("store.Places.Track: bad time %q: %w", at.String, err)
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// InsertTrip stores a trip with its places and track in one transaction.
func (r *Places) InsertTrip(ctx context.Context, l places.List, ps []places.Place, track []places.TrackPoint) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var dup int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM place_lists WHERE owner_id = ? AND sha256 = ?`, l.OwnerID, l.SHA256).Scan(&dup); err != nil {
		return err
	}
	if dup > 0 {
		return places.ErrDuplicate
	}
	now := fmtTime(time.Now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO place_lists (id, owner_id, name, description, date_from, date_to, source_file, sha256, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`, l.ID, l.OwnerID, l.Name, l.Description, l.DateFrom, l.DateTo, l.SourceFile, l.SHA256, now, now); err != nil {
		return fmt.Errorf("store.Places.InsertTrip: %w", err)
	}
	for _, p := range ps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO places (id, owner_id, name, description, latitude, longitude, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			p.ID, l.OwnerID, p.Name, p.Description, p.Latitude, p.Longitude, now, now); err != nil {
			return fmt.Errorf("store.Places.InsertTrip place: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO place_list_places (list_id, place_id) VALUES (?, ?)`, l.ID, p.ID); err != nil {
			return err
		}
	}
	for i, p := range track {
		var at any
		if !p.At.IsZero() {
			at = fmtPoint(p.At)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO place_track (list_id, seq, at, latitude, longitude) VALUES (?,?,?,?,?)`, l.ID, i, at, p.Latitude, p.Longitude); err != nil {
			return fmt.Errorf("store.Places.InsertTrip track: %w", err)
		}
	}
	return tx.Commit()
}

// UpdateTrip changes name, description and range.
func (r *Places) UpdateTrip(ctx context.Context, l places.List) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE place_lists SET name = ?, description = ?, date_from = ?, date_to = ?, updated_at = ? WHERE owner_id = ? AND id = ?`,
		l.Name, l.Description, l.DateFrom, l.DateTo, fmtTime(time.Now()), l.OwnerID, l.ID)
	return affected(res, err, places.ErrNotFound)
}

// DeleteTrip removes a trip, its track and the places of no other trip.
func (r *Places) DeleteTrip(ctx context.Context, owner, id string) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM place_lists WHERE owner_id = ? AND id = ?`, owner, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return places.ErrNotFound
	}
	rows, err := tx.QueryContext(ctx, `SELECT place_id FROM place_list_places WHERE list_id = ?
AND place_id NOT IN (SELECT place_id FROM place_list_places WHERE list_id != ?)`, id, id)
	if err != nil {
		return err
	}
	var orphans []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			_ = rows.Close()
			return err
		}
		orphans = append(orphans, p)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// The trip first: it cascades to its track and its links to places.
	if _, err := tx.ExecContext(ctx, `DELETE FROM place_lists WHERE owner_id = ? AND id = ?`, owner, id); err != nil {
		return fmt.Errorf("store.Places.DeleteTrip: %w", err)
	}
	for _, p := range orphans {
		if _, err := tx.ExecContext(ctx, `DELETE FROM places WHERE owner_id = ? AND id = ?`, owner, p); err != nil {
			return fmt.Errorf("store.Places.DeleteTrip place: %w", err)
		}
	}
	return tx.Commit()
}

// ── location history ──────────────────────────────────

// Mark is the newest time of the owner's points and stay starts.
func (r *Places) Mark(ctx context.Context, owner string) (time.Time, bool, error) {
	var p, s sql.NullString
	if err := r.s.db.QueryRowContext(ctx, `SELECT (SELECT MAX(at) FROM place_points WHERE owner_id = ?), (SELECT MAX(start_at) FROM place_stays WHERE owner_id = ?)`, owner, owner).Scan(&p, &s); err != nil {
		return time.Time{}, false, fmt.Errorf("store.Places.Mark: %w", err)
	}
	m := p.String
	if s.String > m {
		m = s.String
	}
	if m == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339Nano, m)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store.Places.Mark: bad time %q: %w", m, err)
	}
	return t, true, nil
}

// InsertImport stores an import in one transaction; fill writes the rows
// through the sink while it reads them, then the import gets its counts.
func (r *Places) InsertImport(ctx context.Context, imp places.Import, fill func(places.ImportSink) (places.Import, error)) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO place_imports (id, owner_id, kind, file_name, imported_at, points, stays, skipped) VALUES (?,?,?,?,?,0,0,0)`,
		imp.ID, imp.OwnerID, imp.Kind, imp.FileName, fmtTime(imp.At)); err != nil {
		return fmt.Errorf("store.Places.InsertImport: %w", err)
	}
	sink := &importSink{owner: imp.OwnerID, imp: imp.ID}
	if sink.pts, err = tx.PrepareContext(ctx, `INSERT INTO place_points (owner_id, import_id, at, utc_offset_min, day, latitude, longitude) VALUES (?,?,?,?,?,?,?)`); err != nil {
		return err
	}
	defer func() { _ = sink.pts.Close() }()
	if sink.stays, err = tx.PrepareContext(ctx, `INSERT INTO place_stays (owner_id, import_id, start_at, end_at, utc_offset_min, day, end_day, latitude, longitude, semantic_type)
VALUES (?,?,?,?,?,?,?,?,?,?)`); err != nil {
		return err
	}
	defer func() { _ = sink.stays.Close() }()
	sink.ctx = ctx
	done, err := fill(sink)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE place_imports SET points = ?, stays = ?, skipped = ? WHERE id = ?`, done.Points, done.Stays, done.Skipped, imp.ID); err != nil {
		return fmt.Errorf("store.Places.InsertImport counts: %w", err)
	}
	return tx.Commit()
}

type importSink struct {
	ctx        context.Context
	owner, imp string
	pts, stays *sql.Stmt
}

func (s *importSink) Point(p places.Point) error {
	var off any
	if p.Offset != nil {
		off = *p.Offset
	}
	if _, err := s.pts.ExecContext(s.ctx, s.owner, s.imp, fmtPoint(p.At), off, p.Day, p.Latitude, p.Longitude); err != nil {
		return fmt.Errorf("store.Places.InsertImport point: %w", err)
	}
	return nil
}

func (s *importSink) Stay(st places.Stay) error {
	if _, err := s.stays.ExecContext(s.ctx, s.owner, s.imp, fmtPoint(st.Start), fmtPoint(st.End), st.Offset, st.Day, st.EndDay, st.Latitude, st.Longitude, st.SemanticType); err != nil {
		return fmt.Errorf("store.Places.InsertImport stay: %w", err)
	}
	return nil
}

// Imports lists an owner's imports, newest first.
func (r *Places) Imports(ctx context.Context, owner string) ([]places.Import, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT id, owner_id, kind, file_name, imported_at, points, stays, skipped FROM place_imports
WHERE owner_id = ? ORDER BY imported_at DESC, rowid DESC`, owner)
	if err != nil {
		return nil, fmt.Errorf("store.Places.Imports: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []places.Import
	for rows.Next() {
		var i places.Import
		var at string
		if err := rows.Scan(&i.ID, &i.OwnerID, &i.Kind, &i.FileName, &at, &i.Points, &i.Stays, &i.Skipped); err != nil {
			return nil, err
		}
		if i.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// DeleteImport removes an import with its points and stays.
func (r *Places) DeleteImport(ctx context.Context, owner, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM place_imports WHERE owner_id = ? AND id = ?`, owner, id)
	return affected(res, err, places.ErrNotFound)
}

// Days lists the days with points or stays.
func (r *Places) Days(ctx context.Context, owner string) ([]string, error) {
	return r.strings(ctx, `SELECT DISTINCT day FROM place_points WHERE owner_id = ?
UNION SELECT DISTINCT day FROM place_stays WHERE owner_id = ? ORDER BY 1`, owner, owner)
}

func (r *Places) strings(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := r.s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store.Places: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PointsOn returns the points of one local day, by time.
func (r *Places) PointsOn(ctx context.Context, owner, day string) ([]places.Point, error) {
	return r.points(ctx, `owner_id = ? AND day = ? ORDER BY at`, owner, day)
}

// PointsBetween returns the points of the local days from..to, by time.
func (r *Places) PointsBetween(ctx context.Context, owner, fromDay, toDay string) ([]places.Point, error) {
	return r.points(ctx, `owner_id = ? AND day BETWEEN ? AND ? ORDER BY at`, owner, fromDay, toDay)
}

// points runs a SELECT with a fixed WHERE clause; where is always a
// constant from this file, values go through args.
func (r *Places) points(ctx context.Context, where string, args ...any) ([]places.Point, error) {
	//nolint:gosec // where is a constant of this file, values are bound
	rows, err := r.s.db.QueryContext(ctx, `SELECT at, utc_offset_min, day, latitude, longitude FROM place_points WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("store.Places points: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []places.Point
	for rows.Next() {
		var p places.Point
		var at string
		var off sql.NullInt64
		if err := rows.Scan(&at, &off, &p.Day, &p.Latitude, &p.Longitude); err != nil {
			return nil, err
		}
		if p.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("store.Places: bad point time %q: %w", at, err)
		}
		if off.Valid {
			o := int(off.Int64)
			p.Offset = &o
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// StaysOn returns the stays that touch one local day, by start.
func (r *Places) StaysOn(ctx context.Context, owner, day string) ([]places.Stay, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT start_at, end_at, utc_offset_min, day, end_day, latitude, longitude, semantic_type
FROM place_stays WHERE owner_id = ? AND day <= ? AND end_day >= ? ORDER BY start_at`, owner, day, day)
	if err != nil {
		return nil, fmt.Errorf("store.Places.StaysOn: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []places.Stay
	for rows.Next() {
		var s places.Stay
		var st, en string
		if err := rows.Scan(&st, &en, &s.Offset, &s.Day, &s.EndDay, &s.Latitude, &s.Longitude, &s.SemanticType); err != nil {
			return nil, err
		}
		if s.Start, err = time.Parse(time.RFC3339Nano, st); err != nil {
			return nil, err
		}
		if s.End, err = time.Parse(time.RFC3339Nano, en); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ── heatmap ───────────────────────────────────────────

// heatSource is everything the heatmap counts: points, track points and
// places of the owner.
const heatSource = `SELECT latitude, longitude FROM place_points WHERE owner_id = ?1
UNION ALL SELECT t.latitude, t.longitude FROM place_track t JOIN place_lists l ON l.id = t.list_id WHERE l.owner_id = ?1
UNION ALL SELECT latitude, longitude FROM places WHERE owner_id = ?1`

// Heat counts per cell of size deg inside b. Cells are anchored at -90/-180
// so they do not move with the view.
func (r *Places) Heat(ctx context.Context, owner string, deg float64, b places.Box) ([]places.Cell, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT CAST((latitude + 90) / ?2 AS INTEGER) i, CAST((longitude + 180) / ?2 AS INTEGER) j, COUNT(*)
FROM (`+heatSource+`)
WHERE latitude BETWEEN ?3 AND ?4 AND longitude BETWEEN ?5 AND ?6 GROUP BY i, j`, owner, deg, b.South, b.North, b.West, b.East)
	if err != nil {
		return nil, fmt.Errorf("store.Places.Heat: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []places.Cell
	for rows.Next() {
		var i, j int64
		var c places.Cell
		if err := rows.Scan(&i, &j, &c.N); err != nil {
			return nil, err
		}
		c.Latitude, c.Longitude = (float64(i)+0.5)*deg-90, (float64(j)+0.5)*deg-180
		out = append(out, c)
	}
	return out, rows.Err()
}

// Bounds is the box around everything Heat counts.
func (r *Places) Bounds(ctx context.Context, owner string) (places.Box, bool, error) {
	var s, w, n, e sql.NullFloat64
	err := r.s.db.QueryRowContext(ctx, `SELECT MIN(latitude), MIN(longitude), MAX(latitude), MAX(longitude) FROM (`+heatSource+`)`, owner).Scan(&s, &w, &n, &e)
	if err != nil {
		return places.Box{}, false, fmt.Errorf("store.Places.Bounds: %w", err)
	}
	if !s.Valid {
		return places.Box{}, false, nil
	}
	return places.Box{South: s.Float64, West: w.Float64, North: n.Float64, East: e.Float64}, true, nil
}
