package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/tiliavir/allmend/internal/commute"
)

// Commute implements commute.Repository.
type Commute struct{ s *Store }

// Commute returns the commute repository.
func (s *Store) Commute() *Commute { return &Commute{s} }

// Years lists an owner's years, newest first.
func (r *Commute) Years(ctx context.Context, ownerID string) ([]int, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT year FROM commute_years WHERE owner_id = ? ORDER BY year DESC`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("store.Commute.Years: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []int
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		out = append(out, y)
	}
	return out, rows.Err()
}

const yearCols = `id, owner_id, year, version, car_dist_km, train_dist_km, bike_dist_km, rate_low_cents, rate_high_cents, threshold_km, created_at, updated_at`

func (r *Commute) one(ctx context.Context, where string, args ...any) (commute.Year, error) {
	var y commute.Year
	var low, high, thr sql.NullFloat64
	var c, u string
	err := r.s.db.QueryRowContext(ctx, `SELECT `+yearCols+` FROM commute_years WHERE `+where, args...).
		Scan(&y.ID, &y.OwnerID, &y.Year, &y.Version, &y.CarDistKm, &y.TrainDistKm, &y.BikeDistKm, &low, &high, &thr, &c, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return y, commute.ErrNotFound
	}
	if err != nil {
		return y, fmt.Errorf("store.Commute: %w", err)
	}
	y.RateLowCents, y.RateHighCents, y.ThresholdKm = ptr(low), ptr(high), ptr(thr)
	if y.CreatedAt, err = parseTime(c); err != nil {
		return y, err
	}
	if y.UpdatedAt, err = parseTime(u); err != nil {
		return y, err
	}
	rows, err := r.s.db.QueryContext(ctx, `SELECT month, workdays, holidays, kurs, kompensation, unfall, krank, bike, car_train, home_office FROM commute_months WHERE year_id = ?`, y.ID)
	if err != nil {
		return y, fmt.Errorf("store.Commute months: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var mo int
		var m commute.Month
		if err := rows.Scan(&mo, &m.Workdays, &m.Holidays, &m.Kurs, &m.Kompensation, &m.Unfall, &m.Krank, &m.Bike, &m.CarTrain, &m.HomeOffice); err != nil {
			return y, err
		}
		y.Months[mo-1] = m
	}
	return y, rows.Err()
}

func ptr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

// Get loads one year of an owner.
func (r *Commute) Get(ctx context.Context, ownerID string, year int) (commute.Year, error) {
	return r.one(ctx, `owner_id = ? AND year = ?`, ownerID, year)
}

// Latest loads an owner's most recent year.
func (r *Commute) Latest(ctx context.Context, ownerID string) (commute.Year, error) {
	return r.one(ctx, `owner_id = ? ORDER BY year DESC LIMIT 1`, ownerID)
}

func writeMonths(ctx context.Context, tx *sql.Tx, y commute.Year) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM commute_months WHERE year_id = ?`, y.ID); err != nil {
		return err
	}
	for i, m := range y.Months {
		if _, err := tx.ExecContext(ctx, `INSERT INTO commute_months (year_id, month, workdays, holidays, kurs, kompensation, unfall, krank, bike, car_train, home_office) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			y.ID, i+1, m.Workdays, m.Holidays, m.Kurs, m.Kompensation, m.Unfall, m.Krank, m.Bike, m.CarTrain, m.HomeOffice); err != nil {
			return err
		}
	}
	return nil
}

func nullF(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// Insert stores a new year.
func (r *Commute) Insert(ctx context.Context, y commute.Year) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO commute_years (`+yearCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		y.ID, y.OwnerID, y.Year, y.Version, y.CarDistKm, y.TrainDistKm, y.BikeDistKm, nullF(y.RateLowCents), nullF(y.RateHighCents), nullF(y.ThresholdKm), fmtTime(y.CreatedAt), fmtTime(y.UpdatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return commute.ErrExists
		}
		return fmt.Errorf("store.Commute.Insert: %w", err)
	}
	if err := writeMonths(ctx, tx, y); err != nil {
		return fmt.Errorf("store.Commute.Insert months: %w", err)
	}
	return tx.Commit()
}

// Update saves a year if the stored version is want.
func (r *Commute) Update(ctx context.Context, y commute.Year, want int) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE commute_years SET version=?, car_dist_km=?, train_dist_km=?, bike_dist_km=?, rate_low_cents=?, rate_high_cents=?, threshold_km=?, updated_at=? WHERE id=? AND owner_id=? AND version=?`,
		y.Version, y.CarDistKm, y.TrainDistKm, y.BikeDistKm, nullF(y.RateLowCents), nullF(y.RateHighCents), nullF(y.ThresholdKm), fmtTime(y.UpdatedAt), y.ID, y.OwnerID, want)
	if err != nil {
		return fmt.Errorf("store.Commute.Update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return commute.ErrConflict
	}
	if err := writeMonths(ctx, tx, y); err != nil {
		return fmt.Errorf("store.Commute.Update months: %w", err)
	}
	return tx.Commit()
}

// Delete removes an owner's year and its months.
func (r *Commute) Delete(ctx context.Context, ownerID string, year int) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM commute_years WHERE owner_id = ? AND year = ?`, ownerID, year)
	if err != nil {
		return fmt.Errorf("store.Commute.Delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return commute.ErrNotFound
	}
	return nil
}
