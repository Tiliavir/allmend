package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/finanzen"
)

// Finanzen implements finanzen.Repository and finanzen.Market.
type Finanzen struct{ s *Store }

// Finanzen returns the finance repository.
func (s *Store) Finanzen() *Finanzen { return &Finanzen{s} }

func finNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return finanzen.ErrNotFound
	}
	return err
}

func finAffected(res sql.Result, err error, none error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return none
	}
	return nil
}

type scanner interface{ Scan(...any) error }

// ── assets ────────────────────────────────────────────

const assetCols = `id, owner_id, name, kind, category, currency, value_cents, as_of, maturity, tax_allowance_cents, cost_cents,
	paid_in_cents, distributions_cents, comment, version, created_at, updated_at`

func scanAsset(row scanner) (finanzen.Asset, error) {
	var a finanzen.Asset
	var kind, c, u string
	if err := row.Scan(&a.ID, &a.OwnerID, &a.Name, &kind, &a.Category, &a.Currency, &a.ValueCents, &a.AsOf, &a.MaturityDate,
		&a.TaxAllowanceCents, &a.CostCents, &a.PaidInCents, &a.DistributionsCents, &a.Comment, &a.Version, &c, &u); err != nil {
		return a, err
	}
	a.Kind = finanzen.AssetKind(kind)
	return a, times(&a.CreatedAt, &a.UpdatedAt, c, u)
}

// Assets lists an owner's assets by kind and name.
func (r *Finanzen) Assets(ctx context.Context, owner string) ([]finanzen.Asset, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+assetCols+` FROM fin_assets WHERE owner_id = ?
ORDER BY CASE kind WHEN 'konto' THEN 0 WHEN 'bauspar' THEN 1 WHEN 'vorsorge' THEN 2 ELSE 3 END, name COLLATE NOCASE`, owner)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []finanzen.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Asset returns one asset.
func (r *Finanzen) Asset(ctx context.Context, owner, id string) (finanzen.Asset, error) {
	a, err := scanAsset(r.s.db.QueryRowContext(ctx, `SELECT `+assetCols+` FROM fin_assets WHERE owner_id = ? AND id = ?`, owner, id))
	return a, finNotFound(err)
}

// InsertAsset stores a new asset.
func (r *Finanzen) InsertAsset(ctx context.Context, a finanzen.Asset) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO fin_assets (`+assetCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.OwnerID, a.Name, string(a.Kind), a.Category, a.Currency, a.ValueCents, a.AsOf, a.MaturityDate, a.TaxAllowanceCents,
		a.CostCents, a.PaidInCents, a.DistributionsCents, a.Comment, a.Version, fmtTime(a.CreatedAt), fmtTime(a.UpdatedAt))
	return err
}

// UpdateAsset replaces an asset if it still has version want.
func (r *Finanzen) UpdateAsset(ctx context.Context, a finanzen.Asset, want int) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE fin_assets SET name = ?, kind = ?, category = ?, currency = ?, value_cents = ?, as_of = ?,
	maturity = ?, tax_allowance_cents = ?, cost_cents = ?, paid_in_cents = ?, distributions_cents = ?, comment = ?, version = ?, updated_at = ?
WHERE owner_id = ? AND id = ? AND version = ?`,
		a.Name, string(a.Kind), a.Category, a.Currency, a.ValueCents, a.AsOf, a.MaturityDate, a.TaxAllowanceCents, a.CostCents,
		a.PaidInCents, a.DistributionsCents, a.Comment, a.Version, fmtTime(a.UpdatedAt), a.OwnerID, a.ID, want)
	return finAffected(res, err, finanzen.ErrConflict)
}

// DeleteAsset removes an asset.
func (r *Finanzen) DeleteAsset(ctx context.Context, owner, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM fin_assets WHERE owner_id = ? AND id = ?`, owner, id)
	return finAffected(res, err, finanzen.ErrNotFound)
}

// ── securities ────────────────────────────────────────

const secCols = `id, owner_id, name, isin, ticker, kind, currency, symbol, archived, archived_at, risk_override, notes, version, created_at, updated_at`

func scanSecurity(row scanner) (finanzen.Security, error) {
	var s finanzen.Security
	var archived int
	var c, u string
	if err := row.Scan(&s.ID, &s.OwnerID, &s.Name, &s.ISIN, &s.Ticker, &s.Kind, &s.Currency, &s.Symbol, &archived, &s.ArchivedAt,
		&s.RiskOverride, &s.Notes, &s.Version, &c, &u); err != nil {
		return s, err
	}
	s.Archived = archived == 1
	return s, times(&s.CreatedAt, &s.UpdatedAt, c, u)
}

func (r *Finanzen) weights(ctx context.Context, q string, arg string) (map[string][]finanzen.Weight, error) {
	rows, err := r.s.db.QueryContext(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]finanzen.Weight{}
	for rows.Next() {
		var key string
		var w finanzen.Weight
		if err := rows.Scan(&key, &w.Dimension, &w.Label, &w.Weight); err != nil {
			return nil, err
		}
		out[key] = append(out[key], w)
	}
	return out, rows.Err()
}

const weightOrder = ` ORDER BY CASE dimension WHEN 'sector' THEN 0 WHEN 'country' THEN 1 WHEN 'currency' THEN 2 ELSE 3 END, weight DESC, label`

// Securities lists an owner's securities by name, with manual weights.
func (r *Finanzen) Securities(ctx context.Context, owner string) ([]finanzen.Security, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+secCols+` FROM fin_securities WHERE owner_id = ? ORDER BY name COLLATE NOCASE`, owner)
	if err != nil {
		return nil, err
	}
	var out []finanzen.Security
	for rows.Next() {
		s, err := scanSecurity(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, s)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	ws, err := r.weights(ctx, `SELECT w.security_id, w.dimension, w.label, w.weight FROM fin_security_weights w
JOIN fin_securities s ON s.id = w.security_id WHERE s.owner_id = ?`+weightOrder, owner)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Weights = ws[out[i].ID]
	}
	return out, nil
}

// Security returns one security with its manual weights.
func (r *Finanzen) Security(ctx context.Context, owner, id string) (finanzen.Security, error) {
	s, err := scanSecurity(r.s.db.QueryRowContext(ctx, `SELECT `+secCols+` FROM fin_securities WHERE owner_id = ? AND id = ?`, owner, id))
	if err != nil {
		return s, finNotFound(err)
	}
	ws, err := r.weights(ctx, `SELECT security_id, dimension, label, weight FROM fin_security_weights WHERE security_id = ?`+weightOrder, id)
	s.Weights = ws[id]
	return s, err
}

func (r *Finanzen) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func putWeights(ctx context.Context, tx *sql.Tx, key string, ws []finanzen.Weight) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM fin_security_weights WHERE security_id = ?`, key); err != nil {
		return err
	}
	for _, w := range ws {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fin_security_weights (security_id, dimension, label, weight) VALUES (?, ?, ?, ?)`,
			key, w.Dimension, w.Label, w.Weight); err != nil {
			return err
		}
	}
	return nil
}

// InsertSecurity stores a new security with its weights.
func (r *Finanzen) InsertSecurity(ctx context.Context, s finanzen.Security) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fin_securities (`+secCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.ID, s.OwnerID, s.Name, s.ISIN, s.Ticker, s.Kind, s.Currency, s.Symbol, b2i(s.Archived), s.ArchivedAt, s.RiskOverride,
			s.Notes, s.Version, fmtTime(s.CreatedAt), fmtTime(s.UpdatedAt)); err != nil {
			return err
		}
		return putWeights(ctx, tx, s.ID, s.Weights)
	})
}

// UpdateSecurity replaces a security and its weights if it still has version want.
func (r *Finanzen) UpdateSecurity(ctx context.Context, s finanzen.Security, want int) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE fin_securities SET name = ?, isin = ?, ticker = ?, kind = ?, currency = ?, symbol = ?,
	archived = ?, archived_at = ?, risk_override = ?, notes = ?, version = ?, updated_at = ? WHERE owner_id = ? AND id = ? AND version = ?`,
			s.Name, s.ISIN, s.Ticker, s.Kind, s.Currency, s.Symbol, b2i(s.Archived), s.ArchivedAt, s.RiskOverride, s.Notes, s.Version,
			fmtTime(s.UpdatedAt), s.OwnerID, s.ID, want)
		if err := finAffected(res, err, finanzen.ErrConflict); err != nil {
			return err
		}
		return putWeights(ctx, tx, s.ID, s.Weights)
	})
}

// DeleteSecurity removes a security without transactions or dividends.
func (r *Finanzen) DeleteSecurity(ctx context.Context, owner, id string) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM fin_transactions WHERE security_id = ?) +
	(SELECT COUNT(*) FROM fin_dividends WHERE security_id = ?)`, id, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return finanzen.ErrInUse
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM fin_security_weights WHERE security_id IN (SELECT id FROM fin_securities WHERE owner_id = ? AND id = ?)`, owner, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM fin_securities WHERE owner_id = ? AND id = ?`, owner, id)
		return finAffected(res, err, finanzen.ErrNotFound)
	})
}

// ── transactions ──────────────────────────────────────

const txCols = `id, owner_id, security_id, date, type, quantity, price_cents, fees_cents, taxes_cents, version, created_at, updated_at`

func scanTx(row scanner) (finanzen.Transaction, error) {
	var t finanzen.Transaction
	var c, u string
	if err := row.Scan(&t.ID, &t.OwnerID, &t.SecurityID, &t.Date, &t.Type, &t.Quantity, &t.PriceCents, &t.FeesCents, &t.TaxesCents,
		&t.Version, &c, &u); err != nil {
		return t, err
	}
	return t, times(&t.CreatedAt, &t.UpdatedAt, c, u)
}

// Transactions lists an owner's transactions: by date, buys before sells, then creation.
func (r *Finanzen) Transactions(ctx context.Context, owner string) ([]finanzen.Transaction, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+txCols+` FROM fin_transactions WHERE owner_id = ?
ORDER BY date, CASE type WHEN 'buy' THEN 0 ELSE 1 END, created_at, id`, owner)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []finanzen.Transaction
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Transaction returns one transaction.
func (r *Finanzen) Transaction(ctx context.Context, owner, id string) (finanzen.Transaction, error) {
	t, err := scanTx(r.s.db.QueryRowContext(ctx, `SELECT `+txCols+` FROM fin_transactions WHERE owner_id = ? AND id = ?`, owner, id))
	return t, finNotFound(err)
}

// InsertTransaction stores a new transaction.
func (r *Finanzen) InsertTransaction(ctx context.Context, t finanzen.Transaction) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO fin_transactions (`+txCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.OwnerID, t.SecurityID, t.Date, t.Type, t.Quantity, t.PriceCents, t.FeesCents, t.TaxesCents, t.Version,
		fmtTime(t.CreatedAt), fmtTime(t.UpdatedAt))
	return err
}

// UpdateTransaction replaces a transaction if it still has version want.
func (r *Finanzen) UpdateTransaction(ctx context.Context, t finanzen.Transaction, want int) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE fin_transactions SET date = ?, type = ?, quantity = ?, price_cents = ?, fees_cents = ?,
	taxes_cents = ?, version = ?, updated_at = ? WHERE owner_id = ? AND id = ? AND version = ?`,
		t.Date, t.Type, t.Quantity, t.PriceCents, t.FeesCents, t.TaxesCents, t.Version, fmtTime(t.UpdatedAt), t.OwnerID, t.ID, want)
	return finAffected(res, err, finanzen.ErrConflict)
}

// DeleteTransaction removes a transaction.
func (r *Finanzen) DeleteTransaction(ctx context.Context, owner, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM fin_transactions WHERE owner_id = ? AND id = ?`, owner, id)
	return finAffected(res, err, finanzen.ErrNotFound)
}

// ── dividends ─────────────────────────────────────────

const divCols = `id, owner_id, security_id, date, currency, amount_cents, version, created_at, updated_at`

func scanDiv(row scanner) (finanzen.Dividend, error) {
	var d finanzen.Dividend
	var c, u string
	if err := row.Scan(&d.ID, &d.OwnerID, &d.SecurityID, &d.Date, &d.Currency, &d.AmountCents, &d.Version, &c, &u); err != nil {
		return d, err
	}
	return d, times(&d.CreatedAt, &d.UpdatedAt, c, u)
}

// Dividends lists an owner's dividends by date.
func (r *Finanzen) Dividends(ctx context.Context, owner string) ([]finanzen.Dividend, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+divCols+` FROM fin_dividends WHERE owner_id = ? ORDER BY date, created_at, id`, owner)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []finanzen.Dividend
	for rows.Next() {
		d, err := scanDiv(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Dividend returns one dividend.
func (r *Finanzen) Dividend(ctx context.Context, owner, id string) (finanzen.Dividend, error) {
	d, err := scanDiv(r.s.db.QueryRowContext(ctx, `SELECT `+divCols+` FROM fin_dividends WHERE owner_id = ? AND id = ?`, owner, id))
	return d, finNotFound(err)
}

// InsertDividend stores a new dividend.
func (r *Finanzen) InsertDividend(ctx context.Context, d finanzen.Dividend) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO fin_dividends (`+divCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.OwnerID, d.SecurityID, d.Date, d.Currency, d.AmountCents, d.Version, fmtTime(d.CreatedAt), fmtTime(d.UpdatedAt))
	return err
}

// UpdateDividend replaces a dividend if it still has version want.
func (r *Finanzen) UpdateDividend(ctx context.Context, d finanzen.Dividend, want int) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE fin_dividends SET date = ?, currency = ?, amount_cents = ?, version = ?, updated_at = ?
WHERE owner_id = ? AND id = ? AND version = ?`, d.Date, d.Currency, d.AmountCents, d.Version, fmtTime(d.UpdatedAt), d.OwnerID, d.ID, want)
	return finAffected(res, err, finanzen.ErrConflict)
}

// DeleteDividend removes a dividend.
func (r *Finanzen) DeleteDividend(ctx context.Context, owner, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM fin_dividends WHERE owner_id = ? AND id = ?`, owner, id)
	return finAffected(res, err, finanzen.ErrNotFound)
}

// ── years ─────────────────────────────────────────────

const finYearCols = `owner_id, year, currency, income_cents, tax_cents, pension_cents, cash_cents, bauspar_cents, stocks_cents, funds_cents,
	real_estate_cents, comment, version, updated_at`

func scanYear(row scanner) (finanzen.Year, error) {
	var y finanzen.Year
	var u string
	if err := row.Scan(&y.OwnerID, &y.Year, &y.Currency, &y.IncomeCents, &y.TaxCents, &y.PensionCents, &y.CashCents, &y.BausparCents, &y.StocksCents,
		&y.FundsCents, &y.RealEstateCents, &y.Comment, &y.Version, &u); err != nil {
		return y, err
	}
	var err error
	y.UpdatedAt, err = parseTime(u)
	return y, err
}

// Years lists an owner's yearly snapshots, oldest first.
func (r *Finanzen) Years(ctx context.Context, owner string) ([]finanzen.Year, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+finYearCols+` FROM fin_years WHERE owner_id = ? ORDER BY year`, owner)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []finanzen.Year
	for rows.Next() {
		y, err := scanYear(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, y)
	}
	return out, rows.Err()
}

// Year returns one snapshot.
func (r *Finanzen) Year(ctx context.Context, owner string, year int) (finanzen.Year, error) {
	y, err := scanYear(r.s.db.QueryRowContext(ctx, `SELECT `+finYearCols+` FROM fin_years WHERE owner_id = ? AND year = ?`, owner, year))
	return y, finNotFound(err)
}

// InsertYear stores a new snapshot.
func (r *Finanzen) InsertYear(ctx context.Context, y finanzen.Year) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO fin_years (`+finYearCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		y.OwnerID, y.Year, y.Currency, y.IncomeCents, y.TaxCents, y.PensionCents, y.CashCents, y.BausparCents, y.StocksCents, y.FundsCents,
		y.RealEstateCents, y.Comment, y.Version, fmtTime(y.UpdatedAt))
	return err
}

// UpdateYear replaces a snapshot if it still has version want.
func (r *Finanzen) UpdateYear(ctx context.Context, y finanzen.Year, want int) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE fin_years SET currency = ?, income_cents = ?, tax_cents = ?, pension_cents = ?, cash_cents = ?,
	bauspar_cents = ?, stocks_cents = ?, funds_cents = ?, real_estate_cents = ?, comment = ?, version = ?, updated_at = ?
WHERE owner_id = ? AND year = ? AND version = ?`, y.Currency, y.IncomeCents, y.TaxCents, y.PensionCents, y.CashCents, y.BausparCents, y.StocksCents,
		y.FundsCents, y.RealEstateCents, y.Comment, y.Version, fmtTime(y.UpdatedAt), y.OwnerID, y.Year, want)
	return finAffected(res, err, finanzen.ErrConflict)
}

// DeleteYear removes a snapshot.
func (r *Finanzen) DeleteYear(ctx context.Context, owner string, year int) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM fin_years WHERE owner_id = ? AND year = ?`, owner, year)
	return finAffected(res, err, finanzen.ErrNotFound)
}

// ── market ────────────────────────────────────────────

// Prices returns all stored closes of symbol, oldest first.
func (r *Finanzen) Prices(ctx context.Context, symbol string) ([]finanzen.Price, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT date, close FROM fin_prices WHERE symbol = ? ORDER BY date`, symbol)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []finanzen.Price
	for rows.Next() {
		var p finanzen.Price
		if err := rows.Scan(&p.Date, &p.Close); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddPrices stores closes that are not stored yet and reports how many.
// A stored close is never changed.
func (r *Finanzen) AddPrices(ctx context.Context, symbol string, ps []finanzen.Price, at time.Time) (int, error) {
	n := 0
	err := r.inTx(ctx, func(tx *sql.Tx) error {
		for _, p := range ps {
			res, err := tx.ExecContext(ctx, `INSERT INTO fin_prices (symbol, date, close, fetched_at) VALUES (?, ?, ?, ?)
ON CONFLICT (symbol, date) DO NOTHING`, symbol, p.Date, p.Close, fmtTime(at))
			if err != nil {
				return fmt.Errorf("price %s %s: %w", symbol, p.Date, err)
			}
			k, _ := res.RowsAffected()
			n += int(k)
		}
		return nil
	})
	return n, err
}

// Rate returns the newest stored rate of currency in base.
func (r *Finanzen) Rate(ctx context.Context, currency, base string) (finanzen.Rate, error) {
	x := finanzen.Rate{Currency: currency, Base: base}
	err := r.s.db.QueryRowContext(ctx, `SELECT date, rate FROM fin_rates WHERE currency = ? AND base = ? ORDER BY date DESC LIMIT 1`,
		currency, base).Scan(&x.Date, &x.Rate)
	return x, finNotFound(err)
}

// AddRate stores a rate unless one exists for that day.
func (r *Finanzen) AddRate(ctx context.Context, x finanzen.Rate, at time.Time) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO fin_rates (currency, base, date, rate, fetched_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (currency, base, date) DO NOTHING`, x.Currency, x.Base, x.Date, x.Rate, fmtTime(at))
	return err
}

// Profile returns the stored provider composition of symbol.
func (r *Finanzen) Profile(ctx context.Context, symbol string) (finanzen.Profile, error) {
	p := finanzen.Profile{Symbol: symbol}
	var at string
	if err := r.s.db.QueryRowContext(ctx, `SELECT fetched_at FROM fin_profiles WHERE symbol = ?`, symbol).Scan(&at); err != nil {
		return p, finNotFound(err)
	}
	var err error
	if p.FetchedAt, err = parseTime(at); err != nil {
		return p, err
	}
	ws, err := r.weights(ctx, `SELECT symbol, dimension, label, weight FROM fin_profile_weights WHERE symbol = ?`+weightOrder, symbol)
	p.Weights = ws[symbol]
	return p, err
}

// SetProfile replaces the provider composition of a symbol.
func (r *Finanzen) SetProfile(ctx context.Context, p finanzen.Profile) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM fin_profile_weights WHERE symbol = ?`, p.Symbol); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fin_profiles (symbol, fetched_at) VALUES (?, ?)
ON CONFLICT (symbol) DO UPDATE SET fetched_at = excluded.fetched_at`, p.Symbol, fmtTime(p.FetchedAt)); err != nil {
			return err
		}
		for _, w := range p.Weights {
			if _, err := tx.ExecContext(ctx, `INSERT INTO fin_profile_weights (symbol, dimension, label, weight) VALUES (?, ?, ?, ?)`,
				p.Symbol, w.Dimension, w.Label, w.Weight); err != nil {
				return err
			}
		}
		return nil
	})
}

// Symbols lists the symbols of all active securities (any owner).
func (r *Finanzen) Symbols(ctx context.Context) ([]finanzen.SymbolUse, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT symbol, MIN(kind) FROM fin_securities WHERE symbol != '' AND archived = 0
GROUP BY symbol ORDER BY symbol`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []finanzen.SymbolUse
	for rows.Next() {
		var u finanzen.SymbolUse
		if err := rows.Scan(&u.Symbol, &u.Kind); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Currencies lists all currencies in use except base.
func (r *Finanzen) Currencies(ctx context.Context, base string) ([]string, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT currency FROM fin_assets UNION SELECT currency FROM fin_securities
UNION SELECT currency FROM fin_dividends UNION SELECT currency FROM fin_years ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		if c != base {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// AddRun records a refresh.
func (r *Finanzen) AddRun(ctx context.Context, x finanzen.Run) error {
	msg := x.Message
	if len(msg) > 4000 {
		msg = msg[:4000]
	}
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO fin_runs (at, ok, message) VALUES (?, ?, ?)`, fmtTime(x.At), b2i(x.OK), strings.ToValidUTF8(msg, ""))
	return err
}

// LastRun returns the newest refresh.
func (r *Finanzen) LastRun(ctx context.Context) (finanzen.Run, error) {
	var x finanzen.Run
	var at string
	var ok int
	if err := r.s.db.QueryRowContext(ctx, `SELECT at, ok, message FROM fin_runs ORDER BY id DESC LIMIT 1`).Scan(&at, &ok, &x.Message); err != nil {
		return x, finNotFound(err)
	}
	x.OK = ok == 1
	var err error
	x.At, err = parseTime(at)
	return x, err
}
