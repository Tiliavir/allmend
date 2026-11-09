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

// visitTime is the fixed-width timestamp format of place_visits.at.
const visitTime = "2006-01-02T15:04:05.000Z"

// Lists returns an owner's trips with their place counts, by name.
func (r *Places) Lists(ctx context.Context, owner string) ([]places.List, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT l.id, l.owner_id, l.name, l.description, l.date_from, l.date_to, l.source_file,
	(SELECT COUNT(*) FROM place_list_places p WHERE p.list_id = l.id)
FROM place_lists l WHERE l.owner_id = ? ORDER BY l.date_from DESC, l.name COLLATE NOCASE`, owner)
	if err != nil {
		return nil, fmt.Errorf("store.Places.Lists: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []places.List
	for rows.Next() {
		var l places.List
		if err := rows.Scan(&l.ID, &l.OwnerID, &l.Name, &l.Description, &l.DateFrom, &l.DateTo, &l.SourceFile, &l.PlaceCount); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// List returns one trip of an owner with its places.
func (r *Places) List(ctx context.Context, owner, id string) (places.List, []places.Place, error) {
	var l places.List
	err := r.s.db.QueryRowContext(ctx, `SELECT id, owner_id, name, description, date_from, date_to, source_file FROM place_lists WHERE owner_id = ? AND id = ?`, owner, id).
		Scan(&l.ID, &l.OwnerID, &l.Name, &l.Description, &l.DateFrom, &l.DateTo, &l.SourceFile)
	if errors.Is(err, sql.ErrNoRows) {
		return l, nil, places.ErrNotFound
	}
	if err != nil {
		return l, nil, fmt.Errorf("store.Places.List: %w", err)
	}
	rows, err := r.s.db.QueryContext(ctx, `SELECT p.id, p.owner_id, p.name, p.description, p.latitude, p.longitude
FROM places p JOIN place_list_places lp ON lp.place_id = p.id WHERE lp.list_id = ? ORDER BY p.name COLLATE NOCASE`, id)
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
	l.PlaceCount = len(ps)
	return l, ps, rows.Err()
}

const visitSelect = `SELECT id, owner_id, COALESCE(place_id, ''), latitude, longitude, at, source_file FROM place_visits WHERE `

// visits runs visitSelect with a fixed WHERE clause; where is always a
// constant from this file, values go through args.
func (r *Places) visits(ctx context.Context, where string, args ...any) ([]places.Visit, error) {
	rows, err := r.s.db.QueryContext(ctx, visitSelect+where, args...)

	if err != nil {
		return nil, fmt.Errorf("store.Places visits: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []places.Visit
	for rows.Next() {
		var v places.Visit
		var at string
		if err := rows.Scan(&v.ID, &v.OwnerID, &v.PlaceID, &v.Latitude, &v.Longitude, &at, &v.SourceFile); err != nil {
			return nil, err
		}
		if v.Timestamp, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("store.Places: bad visit time %q: %w", at, err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// VisitsBySource returns the GPS track of an imported file, by time.
func (r *Places) VisitsBySource(ctx context.Context, owner, source string) ([]places.Visit, error) {
	return r.visits(ctx, `owner_id = ? AND source_file = ? ORDER BY julianday(at)`, owner, source)
}

// VisitsOn returns visits in [from, to), by time.
func (r *Places) VisitsOn(ctx context.Context, owner string, from, to time.Time) ([]places.Visit, error) {
	return r.visits(ctx, `owner_id = ? AND julianday(at) >= julianday(?) AND julianday(at) < julianday(?) ORDER BY julianday(at)`,
		owner, from.UTC().Format(visitTime), to.UTC().Format(visitTime))
}

// VisitDays lists the UTC days with visits.
func (r *Places) VisitDays(ctx context.Context, owner string) ([]string, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT DISTINCT date(at) FROM place_visits WHERE owner_id = ? ORDER BY 1`, owner)
	if err != nil {
		return nil, fmt.Errorf("store.Places.VisitDays: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// InsertVisits adds visits in one transaction, skipping existing ones.
func (r *Places) InsertVisits(ctx context.Context, vs []places.Visit) (int, error) {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	n := 0
	for _, v := range vs {
		res, err := tx.ExecContext(ctx, `INSERT INTO place_visits (id, owner_id, place_id, latitude, longitude, at, source_file) VALUES (?,?,?,?,?,?,?)
ON CONFLICT (owner_id, source_file, at) DO NOTHING`, v.ID, v.OwnerID, nullStr(v.PlaceID), v.Latitude, v.Longitude, v.Timestamp.UTC().Format(visitTime), v.SourceFile)
		if err != nil {
			return 0, fmt.Errorf("store.Places.InsertVisits: %w", err)
		}
		k, _ := res.RowsAffected()
		n += int(k)
	}
	return n, tx.Commit()
}
