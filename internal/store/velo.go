package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tiliavir/allmend/internal/velo"
)

// Velo implements velo.Repository.
type Velo struct{ s *Store }

// Velo returns the Velo repository.
func (s *Store) Velo() *Velo { return &Velo{s} }

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return velo.ErrNotFound
	}
	return err
}

func affected(res sql.Result, err error, none error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return none
	}
	return nil
}

// ── bikes ─────────────────────────────────────────────

const bikeCols = `id, owner_id, name, brand, model, acquired, purchase_cents, accessories_cents, notes, color, version, created_at, updated_at`

func scanBike(row interface{ Scan(...any) error }) (velo.Bike, error) {
	var b velo.Bike
	var c, u string
	if err := row.Scan(&b.ID, &b.OwnerID, &b.Name, &b.Brand, &b.Model, &b.Acquired, &b.PurchaseCents, &b.AccessoriesCents, &b.Notes, &b.Color, &b.Version, &c, &u); err != nil {
		return b, err
	}
	return b, times(&b.CreatedAt, &b.UpdatedAt, c, u)
}

func times(cr, up *time.Time, c, u string) error {
	var err error
	if *cr, err = parseTime(c); err != nil {
		return err
	}
	*up, err = parseTime(u)
	return err
}

// Bikes lists an owner's bikes by name.
func (r *Velo) Bikes(ctx context.Context, owner string) ([]velo.Bike, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+bikeCols+` FROM velo_bikes WHERE owner_id = ? ORDER BY name COLLATE NOCASE`, owner)
	if err != nil {
		return nil, fmt.Errorf("store.Velo.Bikes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []velo.Bike
	for rows.Next() {
		b, err := scanBike(rows)
		if err != nil {
			return nil, fmt.Errorf("store.Velo.Bikes: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Bike loads one bike of an owner.
func (r *Velo) Bike(ctx context.Context, owner, id string) (velo.Bike, error) {
	b, err := scanBike(r.s.db.QueryRowContext(ctx, `SELECT `+bikeCols+` FROM velo_bikes WHERE owner_id = ? AND id = ?`, owner, id))
	return b, notFound(err)
}

// InsertBike stores a new bike.
func (r *Velo) InsertBike(ctx context.Context, b velo.Bike) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO velo_bikes (`+bikeCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.OwnerID, b.Name, b.Brand, b.Model, b.Acquired, b.PurchaseCents, b.AccessoriesCents, b.Notes, b.Color, b.Version, fmtTime(b.CreatedAt), fmtTime(b.UpdatedAt))
	return err
}

// UpdateBike saves a bike if the stored version is want.
func (r *Velo) UpdateBike(ctx context.Context, b velo.Bike, want int) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE velo_bikes SET name=?, brand=?, model=?, acquired=?, purchase_cents=?, accessories_cents=?, notes=?, color=?, version=?, updated_at=? WHERE owner_id=? AND id=? AND version=?`,
		b.Name, b.Brand, b.Model, b.Acquired, b.PurchaseCents, b.AccessoriesCents, b.Notes, b.Color, b.Version, fmtTime(b.UpdatedAt), b.OwnerID, b.ID, want)
	return affected(res, err, velo.ErrConflict)
}

// DeleteBike removes a bike without tours or repairs.
func (r *Velo) DeleteBike(ctx context.Context, owner, id string) error {
	var n int
	if err := r.s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM velo_tours WHERE bike_id = ?) + (SELECT COUNT(*) FROM velo_repairs WHERE bike_id = ?)`, id, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return velo.ErrInUse
	}
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM velo_bikes WHERE owner_id = ? AND id = ?`, owner, id)
	return affected(res, err, velo.ErrNotFound)
}

// ── tours ─────────────────────────────────────────────

const tourCols = `id, owner_id, COALESCE(bike_id, ''), date, distance_km, elevation_m, title, description, version, created_at, updated_at`

func scanTour(row interface{ Scan(...any) error }) (velo.Tour, error) {
	var t velo.Tour
	var c, u string
	if err := row.Scan(&t.ID, &t.OwnerID, &t.BikeID, &t.Date, &t.DistanceKm, &t.ElevationM, &t.Title, &t.Description, &t.Version, &c, &u); err != nil {
		return t, err
	}
	return t, times(&t.CreatedAt, &t.UpdatedAt, c, u)
}

// Tours lists an owner's tours, newest first.
func (r *Velo) Tours(ctx context.Context, owner string) ([]velo.Tour, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+tourCols+` FROM velo_tours WHERE owner_id = ? ORDER BY date DESC, created_at DESC`, owner)
	if err != nil {
		return nil, fmt.Errorf("store.Velo.Tours: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []velo.Tour
	for rows.Next() {
		t, err := scanTour(rows)
		if err != nil {
			return nil, fmt.Errorf("store.Velo.Tours: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Tour loads one tour of an owner.
func (r *Velo) Tour(ctx context.Context, owner, id string) (velo.Tour, error) {
	t, err := scanTour(r.s.db.QueryRowContext(ctx, `SELECT `+tourCols+` FROM velo_tours WHERE owner_id = ? AND id = ?`, owner, id))
	return t, notFound(err)
}

// InsertTour stores a new tour.
func (r *Velo) InsertTour(ctx context.Context, t velo.Tour) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO velo_tours (id, owner_id, bike_id, date, distance_km, elevation_m, title, description, version, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.OwnerID, nullStr(t.BikeID), t.Date, t.DistanceKm, t.ElevationM, t.Title, t.Description, t.Version, fmtTime(t.CreatedAt), fmtTime(t.UpdatedAt))
	return err
}

// UpdateTour saves a tour if the stored version is want.
func (r *Velo) UpdateTour(ctx context.Context, t velo.Tour, want int) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE velo_tours SET bike_id=?, date=?, distance_km=?, elevation_m=?, title=?, description=?, version=?, updated_at=? WHERE owner_id=? AND id=? AND version=?`,
		nullStr(t.BikeID), t.Date, t.DistanceKm, t.ElevationM, t.Title, t.Description, t.Version, fmtTime(t.UpdatedAt), t.OwnerID, t.ID, want)
	return affected(res, err, velo.ErrConflict)
}

// DeleteTour removes a tour.
func (r *Velo) DeleteTour(ctx context.Context, owner, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM velo_tours WHERE owner_id = ? AND id = ?`, owner, id)
	return affected(res, err, velo.ErrNotFound)
}

// ── repairs ───────────────────────────────────────────

const repairCols = `id, owner_id, bike_id, date, cost_cents, title, comment, odometer_km, version, created_at, updated_at`

func scanRepair(row interface{ Scan(...any) error }) (velo.Repair, error) {
	var x velo.Repair
	var odo sql.NullFloat64
	var c, u string
	if err := row.Scan(&x.ID, &x.OwnerID, &x.BikeID, &x.Date, &x.CostCents, &x.Title, &x.Comment, &odo, &x.Version, &c, &u); err != nil {
		return x, err
	}
	x.OdometerKm = ptr(odo)
	return x, times(&x.CreatedAt, &x.UpdatedAt, c, u)
}

// Repairs lists the repairs of one bike, newest first.
func (r *Velo) Repairs(ctx context.Context, owner, bikeID string) ([]velo.Repair, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+repairCols+` FROM velo_repairs WHERE owner_id = ? AND bike_id = ? ORDER BY date DESC`, owner, bikeID)
	if err != nil {
		return nil, fmt.Errorf("store.Velo.Repairs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []velo.Repair
	for rows.Next() {
		x, err := scanRepair(rows)
		if err != nil {
			return nil, fmt.Errorf("store.Velo.Repairs: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Repair loads one repair of an owner.
func (r *Velo) Repair(ctx context.Context, owner, id string) (velo.Repair, error) {
	x, err := scanRepair(r.s.db.QueryRowContext(ctx, `SELECT `+repairCols+` FROM velo_repairs WHERE owner_id = ? AND id = ?`, owner, id))
	return x, notFound(err)
}

// InsertRepair stores a new repair.
func (r *Velo) InsertRepair(ctx context.Context, x velo.Repair) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO velo_repairs (`+repairCols2+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		x.ID, x.OwnerID, x.BikeID, x.Date, x.CostCents, x.Title, x.Comment, nullF(x.OdometerKm), x.Version, fmtTime(x.CreatedAt), fmtTime(x.UpdatedAt))
	return err
}

const repairCols2 = `id, owner_id, bike_id, date, cost_cents, title, comment, odometer_km, version, created_at, updated_at`

// UpdateRepair saves a repair if the stored version is want.
func (r *Velo) UpdateRepair(ctx context.Context, x velo.Repair, want int) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE velo_repairs SET date=?, cost_cents=?, title=?, comment=?, odometer_km=?, version=?, updated_at=? WHERE owner_id=? AND id=? AND version=?`,
		x.Date, x.CostCents, x.Title, x.Comment, nullF(x.OdometerKm), x.Version, fmtTime(x.UpdatedAt), x.OwnerID, x.ID, want)
	return affected(res, err, velo.ErrConflict)
}

// DeleteRepair removes a repair.
func (r *Velo) DeleteRepair(ctx context.Context, owner, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM velo_repairs WHERE owner_id = ? AND id = ?`, owner, id)
	return affected(res, err, velo.ErrNotFound)
}

// ── comments, goals ───────────────────────────────────

// Comment returns an owner's comment for year ("" if none).
func (r *Velo) Comment(ctx context.Context, owner string, year int) (string, error) {
	var c string
	err := r.s.db.QueryRowContext(ctx, `SELECT comment FROM velo_year_comments WHERE owner_id = ? AND year = ?`, owner, year).Scan(&c)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return c, err
}

// SetComment upserts (or with "" deletes) a year comment.
func (r *Velo) SetComment(ctx context.Context, owner string, year int, text string, at time.Time) error {
	if text == "" {
		_, err := r.s.db.ExecContext(ctx, `DELETE FROM velo_year_comments WHERE owner_id = ? AND year = ?`, owner, year)
		return err
	}
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO velo_year_comments (owner_id, year, comment, updated_at) VALUES (?,?,?,?)
ON CONFLICT (owner_id, year) DO UPDATE SET comment = excluded.comment, updated_at = excluded.updated_at`, owner, year, text, fmtTime(at))
	return err
}

// Goal returns an owner's target km for a month (0 if none).
func (r *Velo) Goal(ctx context.Context, owner string, year, month int) (float64, error) {
	var km float64
	err := r.s.db.QueryRowContext(ctx, `SELECT target_km FROM velo_goals WHERE owner_id = ? AND year = ? AND month = ?`, owner, year, month).Scan(&km)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return km, err
}

// SetGoal upserts (or with 0 deletes) a monthly target.
func (r *Velo) SetGoal(ctx context.Context, owner string, year, month int, km float64, at time.Time) error {
	if km == 0 {
		_, err := r.s.db.ExecContext(ctx, `DELETE FROM velo_goals WHERE owner_id = ? AND year = ? AND month = ?`, owner, year, month)
		return err
	}
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO velo_goals (owner_id, year, month, target_km, updated_at) VALUES (?,?,?,?,?)
ON CONFLICT (owner_id, year, month) DO UPDATE SET target_km = excluded.target_km, updated_at = excluded.updated_at`, owner, year, month, km, fmtTime(at))
	return err
}
