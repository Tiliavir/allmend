package migrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/finanzen"
)

const v7Schema = `
CREATE TABLE fin_assets (
	id                  TEXT    PRIMARY KEY,
	owner_id            TEXT    NOT NULL REFERENCES accounts(id),
	name                TEXT    NOT NULL CHECK (trim(name) != ''),
	kind                TEXT    NOT NULL CHECK (kind IN ('konto', 'bauspar', 'vorsorge', 'sachwert')),
	category            TEXT    NOT NULL DEFAULT '',
	currency            TEXT    NOT NULL CHECK (length(currency) = 3 AND currency GLOB '[A-Z][A-Z][A-Z]'),
	value_cents         INTEGER NOT NULL CHECK (value_cents >= 0),
	as_of               TEXT    NOT NULL DEFAULT '' CHECK (as_of = '' OR date(as_of) = as_of),
	maturity            TEXT    NOT NULL DEFAULT '' CHECK (maturity = '' OR (date(maturity) = maturity AND kind = 'bauspar')),
	tax_allowance_cents INTEGER NOT NULL DEFAULT 0 CHECK (tax_allowance_cents >= 0),
	cost_cents          INTEGER NOT NULL DEFAULT 0 CHECK (cost_cents >= 0),
	paid_in_cents       INTEGER NOT NULL DEFAULT 0 CHECK (paid_in_cents >= 0),
	distributions_cents INTEGER NOT NULL DEFAULT 0 CHECK (distributions_cents >= 0),
	comment             TEXT    NOT NULL DEFAULT '',
	version             INTEGER NOT NULL,
	created_at          TEXT    NOT NULL,
	updated_at          TEXT    NOT NULL
) STRICT;
CREATE INDEX fin_assets_owner ON fin_assets(owner_id);

CREATE TABLE fin_securities (
	id            TEXT    PRIMARY KEY,
	owner_id      TEXT    NOT NULL REFERENCES accounts(id),
	name          TEXT    NOT NULL CHECK (trim(name) != ''),
	isin          TEXT    NOT NULL DEFAULT '' CHECK (isin = '' OR (length(isin) = 12 AND isin GLOB '[A-Z][A-Z][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][0-9]')),
	ticker        TEXT    NOT NULL DEFAULT '',
	kind          TEXT    NOT NULL CHECK (kind IN ('stock', 'etf', 'fund')),
	currency      TEXT    NOT NULL CHECK (length(currency) = 3 AND currency GLOB '[A-Z][A-Z][A-Z]'),
	symbol        TEXT    NOT NULL DEFAULT '',
	archived      INTEGER NOT NULL CHECK (archived IN (0, 1)),
	archived_at   TEXT    NOT NULL DEFAULT '' CHECK (archived_at = '' OR (archived = 1 AND date(archived_at) = archived_at)),
	risk_override INTEGER NOT NULL DEFAULT 0 CHECK (risk_override BETWEEN 0 AND 7),
	version       INTEGER NOT NULL,
	created_at    TEXT    NOT NULL,
	updated_at    TEXT    NOT NULL
) STRICT;
CREATE INDEX fin_securities_owner ON fin_securities(owner_id);

CREATE TABLE fin_security_weights (
	security_id TEXT NOT NULL REFERENCES fin_securities(id),
	dimension   TEXT NOT NULL CHECK (dimension IN ('sector', 'country', 'currency', 'holding')),
	label       TEXT NOT NULL COLLATE NOCASE CHECK (trim(label) != ''),
	weight      REAL NOT NULL CHECK (weight > 0 AND weight <= 1),
	PRIMARY KEY (security_id, dimension, label)
) STRICT;

CREATE TABLE fin_transactions (
	id          TEXT    PRIMARY KEY,
	owner_id    TEXT    NOT NULL REFERENCES accounts(id),
	security_id TEXT    NOT NULL REFERENCES fin_securities(id),
	date        TEXT    NOT NULL CHECK (date(date) = date),
	type        TEXT    NOT NULL CHECK (type IN ('buy', 'sell')),
	quantity    REAL    NOT NULL CHECK (quantity > 0),
	price_cents INTEGER NOT NULL CHECK (price_cents >= 0),
	fees_cents  INTEGER NOT NULL DEFAULT 0 CHECK (fees_cents >= 0),
	taxes_cents INTEGER NOT NULL DEFAULT 0 CHECK (taxes_cents >= 0),
	version     INTEGER NOT NULL,
	created_at  TEXT    NOT NULL,
	updated_at  TEXT    NOT NULL
) STRICT;
CREATE INDEX fin_transactions_owner ON fin_transactions(owner_id, security_id, date);

CREATE TABLE fin_dividends (
	id           TEXT    PRIMARY KEY,
	owner_id     TEXT    NOT NULL REFERENCES accounts(id),
	security_id  TEXT    NOT NULL REFERENCES fin_securities(id),
	date         TEXT    NOT NULL CHECK (date(date) = date),
	amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
	version      INTEGER NOT NULL,
	created_at   TEXT    NOT NULL,
	updated_at   TEXT    NOT NULL
) STRICT;
CREATE INDEX fin_dividends_owner ON fin_dividends(owner_id, security_id, date);

CREATE TABLE fin_years (
	owner_id          TEXT    NOT NULL REFERENCES accounts(id),
	year              INTEGER NOT NULL CHECK (year BETWEEN 1900 AND 2200),
	income_cents      INTEGER NOT NULL CHECK (income_cents >= 0),
	tax_cents         INTEGER NOT NULL CHECK (tax_cents >= 0),
	pension_cents     INTEGER NOT NULL CHECK (pension_cents >= 0),
	cash_cents        INTEGER NOT NULL CHECK (cash_cents >= 0),
	bauspar_cents     INTEGER NOT NULL CHECK (bauspar_cents >= 0),
	stocks_cents      INTEGER NOT NULL CHECK (stocks_cents >= 0),
	funds_cents       INTEGER NOT NULL CHECK (funds_cents >= 0),
	real_estate_cents INTEGER NOT NULL CHECK (real_estate_cents >= 0),
	comment           TEXT    NOT NULL DEFAULT '',
	version           INTEGER NOT NULL,
	updated_at        TEXT    NOT NULL,
	PRIMARY KEY (owner_id, year)
) STRICT;

CREATE TABLE fin_prices (
	symbol     TEXT NOT NULL,
	date       TEXT NOT NULL CHECK (date(date) = date),
	close      REAL NOT NULL CHECK (close > 0),
	fetched_at TEXT NOT NULL,
	PRIMARY KEY (symbol, date)
) STRICT, WITHOUT ROWID;

CREATE TABLE fin_rates (
	currency   TEXT NOT NULL CHECK (currency GLOB '[A-Z][A-Z][A-Z]'),
	base       TEXT NOT NULL CHECK (base GLOB '[A-Z][A-Z][A-Z]'),
	date       TEXT NOT NULL CHECK (date(date) = date),
	rate       REAL NOT NULL CHECK (rate > 0),
	fetched_at TEXT NOT NULL,
	PRIMARY KEY (currency, base, date)
) STRICT, WITHOUT ROWID;

CREATE TABLE fin_profiles (
	symbol     TEXT PRIMARY KEY,
	fetched_at TEXT NOT NULL
) STRICT;

CREATE TABLE fin_profile_weights (
	symbol    TEXT NOT NULL REFERENCES fin_profiles(symbol),
	dimension TEXT NOT NULL CHECK (dimension IN ('sector', 'country', 'currency', 'holding')),
	label     TEXT NOT NULL COLLATE NOCASE CHECK (trim(label) != ''),
	weight    REAL NOT NULL CHECK (weight > 0 AND weight <= 1),
	PRIMARY KEY (symbol, dimension, label)
) STRICT;

CREATE TABLE fin_runs (
	id      INTEGER PRIMARY KEY,
	at      TEXT    NOT NULL,
	ok      INTEGER NOT NULL CHECK (ok IN (0, 1)),
	message TEXT    NOT NULL
) STRICT;
`

// v7 creates the finance tables and imports the old finance data and the
// old price and exchange-rate caches. Each legacy record is decoded strictly
// (unknown fields, wrong types, currencies that do not match, invalid values
// and sells beyond the holding stop the migration, naming the records).
// The old composition cache is not imported: it is refetched.
func v7(ctx context.Context, tx db.Tx, env db.MigrationEnv) error {
	if _, err := tx.ExecContext(ctx, v7Schema); err != nil {
		return err
	}
	if !env.LegacyAttached {
		return nil
	}
	if env.BaseCurrency == "" {
		env.BaseCurrency = "EUR"
	}
	im := &finImport{tx: tx, env: env}
	steps := []struct {
		table string
		fn    func(context.Context, legacyRow) error
	}{
		{"fin_accounts", im.asset(finanzen.Konto)},
		{"fin_bauspar", im.asset(finanzen.Bauspar)},
		{"fin_pensions", im.asset(finanzen.Vorsorge)},
		{"fin_assets", im.asset(finanzen.Sachwert)},
		{"fin_securities", im.security},
		{"fin_transactions", im.transaction},
		{"fin_dividends", im.dividend},
		{"fin_annual", im.year},
		{"fin_annual_comments", im.yearComment},
	}
	for _, s := range steps {
		ok, err := legacyTable(ctx, tx, env, s.table)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if im.owner == "" {
			if im.owner, err = adminID(ctx, tx, env); err != nil {
				return err
			}
		}
		if err := im.each(ctx, s.table, s.fn); err != nil {
			return err
		}
	}
	if err := im.checkHoldings(ctx); err != nil {
		return err
	}
	return im.caches(ctx)
}

type legacyRow struct {
	id, data, created, updated string
}

type finImport struct {
	tx     db.Tx
	env    db.MigrationEnv
	owner  string
	secCur map[string]string // security id → currency
}

// each decodes every row of table with fn and collects the failures.
func (im *finImport) each(ctx context.Context, table string, fn func(context.Context, legacyRow) error) error {
	rows, err := im.tx.QueryContext(ctx, `SELECT id, data, created_at, updated_at FROM legacy.`+table+` ORDER BY created_at, id`)
	if err != nil {
		return fmt.Errorf("legacy %s: %w", table, err)
	}
	var all []legacyRow
	for rows.Next() {
		var r legacyRow
		if err := rows.Scan(&r.id, &r.data, &r.created, &r.updated); err != nil {
			_ = rows.Close()
			return err
		}
		all = append(all, r)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	var bad []string
	for _, r := range all {
		if err := fn(ctx, r); err != nil {
			bad = append(bad, r.id+": "+err.Error())
		}
	}
	if len(bad) > 0 {
		if len(bad) > 20 {
			bad = append(bad[:20], fmt.Sprintf("… und %d weitere", len(bad)-20))
		}
		return fmt.Errorf("legacy import %s (fix in the old app, then retry):\n  %s", table, strings.Join(bad, "\n  "))
	}
	return nil
}

// decode reads JSON into v; unknown fields and wrong types are errors.
func decode(data string, v any) error {
	d := json.NewDecoder(bytes.NewReader([]byte(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.More() {
		return errors.New("trailing data")
	}
	return nil
}

// sameCur checks that every given sub-currency is empty or cur.
func sameCur(cur string, subs map[string]string) error {
	for _, k := range sortedNames(subs) {
		if v := subs[k]; v != "" && v != cur {
			return fmt.Errorf("%s ist %s, erwartet %s", k, v, cur)
		}
	}
	return nil
}

func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func invalid(err error) error {
	var inv *finanzen.InvalidError
	if errors.As(err, &inv) {
		return errors.New(strings.Join(inv.Problems, "; "))
	}
	return err
}

func (im *finImport) asset(kind finanzen.AssetKind) func(context.Context, legacyRow) error {
	return func(ctx context.Context, r legacyRow) error {
		var v struct {
			ID                    string `json:"id"`
			Name                  string `json:"name"`
			Category              string `json:"category"`
			Currency              string `json:"currency"`
			Balance               *int64 `json:"balance_minor"`
			Value                 *int64 `json:"value_minor"`
			AsOf                  string `json:"as_of"`
			Maturity              string `json:"maturity_date"`
			TaxAllowance          int64  `json:"tax_allowance_minor"`
			TaxAllowanceCurrency  string `json:"tax_allowance_currency"`
			Cost                  int64  `json:"cost_minor"`
			CostCurrency          string `json:"cost_currency"`
			PaidIn                int64  `json:"paid_in_minor"`
			PaidInCurrency        string `json:"paid_in_currency"`
			Distributions         int64  `json:"distributions_minor"`
			DistributionsCurrency string `json:"distributions_currency"`
			Comment               string `json:"comment"`
			CreatedAt             string `json:"created_at"`
			UpdatedAt             string `json:"updated_at"`
		}
		if err := decode(r.data, &v); err != nil {
			return err
		}
		if v.ID != "" && v.ID != r.id {
			return errors.New("id im Datensatz weicht ab")
		}
		value := v.Value
		if kind == finanzen.Konto {
			if v.Value != nil {
				return errors.New("Konto mit value_minor statt balance_minor")
			}
			value = v.Balance
		} else if v.Balance != nil {
			return errors.New("balance_minor gibt es nur bei Konten")
		}
		if v.Category != "" && kind != finanzen.Sachwert {
			return errors.New("category gibt es nur bei Sachwerten")
		}
		if err := sameCur(v.Currency, map[string]string{"tax_allowance_currency": v.TaxAllowanceCurrency,
			"cost_currency": v.CostCurrency, "paid_in_currency": v.PaidInCurrency, "distributions_currency": v.DistributionsCurrency}); err != nil {
			return err
		}
		a := finanzen.Asset{ID: r.id, OwnerID: im.owner, Name: strings.TrimSpace(v.Name), Kind: kind, Category: strings.TrimSpace(v.Category),
			Currency: v.Currency, AsOf: v.AsOf, MaturityDate: v.Maturity, TaxAllowanceCents: v.TaxAllowance, CostCents: v.Cost,
			PaidInCents: v.PaidIn, DistributionsCents: v.Distributions, Comment: v.Comment}
		if value != nil {
			a.ValueCents = *value
		}
		if err := finanzen.CheckAsset(a); err != nil {
			return invalid(err)
		}
		_, err := im.tx.ExecContext(ctx, `INSERT INTO fin_assets (id, owner_id, name, kind, category, currency, value_cents, as_of, maturity,
	tax_allowance_cents, cost_cents, paid_in_cents, distributions_cents, comment, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`, a.ID, a.OwnerID, a.Name, string(a.Kind), a.Category, a.Currency, a.ValueCents,
			a.AsOf, a.MaturityDate, a.TaxAllowanceCents, a.CostCents, a.PaidInCents, a.DistributionsCents, a.Comment, r.created, r.updated)
		return err
	}
}

type legacyComposition struct {
	Sectors     []legacyWeight
	Countries   []legacyWeight
	Currencies  []legacyWeight
	TopHoldings []legacyWeight
}

type legacyWeight struct {
	Label  string
	Weight float64
}

func (im *finImport) security(ctx context.Context, r legacyRow) error {
	var v struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		ISIN             string `json:"isin"`
		Ticker           string `json:"ticker"`
		Kind             string `json:"kind"`
		Currency         string `json:"currency"`
		PriceConnectorID string `json:"price_connector_id"`
		PriceSymbol      string `json:"price_symbol"`
		Archived         *bool  `json:"archived"`
		ArchivedAt       string `json:"archived_at"`
		RiskOverride     int    `json:"risk_override"`
		CompositionJSON  string `json:"composition_json"`
		CreatedAt        string `json:"created_at"`
		UpdatedAt        string `json:"updated_at"`
	}
	if err := decode(r.data, &v); err != nil {
		return err
	}
	if v.ID != "" && v.ID != r.id {
		return errors.New("id im Datensatz weicht ab")
	}
	if v.PriceConnectorID != "" && v.PriceConnectorID != "stock-prices-live" {
		return fmt.Errorf("unbekannter price_connector_id %q", v.PriceConnectorID)
	}
	s := finanzen.Security{ID: r.id, OwnerID: im.owner, Name: strings.TrimSpace(v.Name), ISIN: strings.ToUpper(strings.TrimSpace(v.ISIN)),
		Ticker: strings.TrimSpace(v.Ticker), Kind: v.Kind, Currency: v.Currency, Symbol: strings.TrimSpace(v.PriceSymbol),
		ArchivedAt: v.ArchivedAt, RiskOverride: v.RiskOverride}
	if v.Archived != nil {
		s.Archived = *v.Archived
	}
	if strings.TrimSpace(v.CompositionJSON) != "" {
		var c legacyComposition
		if err := decode(v.CompositionJSON, &c); err != nil {
			return fmt.Errorf("composition_json: %w", err)
		}
		for _, g := range []struct {
			dim string
			ws  []legacyWeight
		}{{finanzen.Sector, c.Sectors}, {finanzen.Country, c.Countries}, {finanzen.Currency, c.Currencies}, {finanzen.Holdings, c.TopHoldings}} {
			for _, w := range g.ws {
				s.Weights = append(s.Weights, finanzen.Weight{Dimension: g.dim, Label: strings.TrimSpace(w.Label), Weight: w.Weight})
			}
		}
	}
	if err := finanzen.CheckSecurity(s); err != nil {
		return invalid(err)
	}
	if _, err := im.tx.ExecContext(ctx, `INSERT INTO fin_securities (id, owner_id, name, isin, ticker, kind, currency, symbol, archived, archived_at,
	risk_override, version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		s.ID, s.OwnerID, s.Name, s.ISIN, s.Ticker, s.Kind, s.Currency, s.Symbol, s.Archived, s.ArchivedAt, s.RiskOverride, r.created, r.updated); err != nil {
		return err
	}
	for _, w := range s.Weights {
		if _, err := im.tx.ExecContext(ctx, `INSERT INTO fin_security_weights (security_id, dimension, label, weight) VALUES (?, ?, ?, ?)`,
			s.ID, w.Dimension, w.Label, w.Weight); err != nil {
			return err
		}
	}
	if im.secCur == nil {
		im.secCur = map[string]string{}
	}
	im.secCur[s.ID] = s.Currency
	return nil
}

func (im *finImport) transaction(ctx context.Context, r legacyRow) error {
	var v struct {
		ID         string  `json:"id"`
		SecurityID string  `json:"security_id"`
		Date       string  `json:"date"`
		Type       string  `json:"type"`
		Quantity   float64 `json:"quantity"`
		Currency   string  `json:"currency"`
		Price      int64   `json:"price_per_unit_minor"`
		Fees       int64   `json:"fees_minor"`
		Taxes      int64   `json:"taxes_minor"`
		CreatedAt  string  `json:"created_at"`
		UpdatedAt  string  `json:"updated_at"`
	}
	if err := decode(r.data, &v); err != nil {
		return err
	}
	cur, ok := im.secCur[v.SecurityID]
	switch {
	case v.ID != "" && v.ID != r.id:
		return errors.New("id im Datensatz weicht ab")
	case !ok:
		return fmt.Errorf("unbekanntes Wertpapier %q", v.SecurityID)
	case v.Currency != "" && v.Currency != cur:
		return fmt.Errorf("Währung %s, das Wertpapier handelt in %s", v.Currency, cur)
	}
	t := finanzen.Transaction{ID: r.id, OwnerID: im.owner, SecurityID: v.SecurityID, Date: v.Date, Type: v.Type, Quantity: v.Quantity,
		PriceCents: v.Price, FeesCents: v.Fees, TaxesCents: v.Taxes}
	if err := finanzen.CheckTransaction(t); err != nil {
		return invalid(err)
	}
	_, err := im.tx.ExecContext(ctx, `INSERT INTO fin_transactions (id, owner_id, security_id, date, type, quantity, price_cents, fees_cents,
	taxes_cents, version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		t.ID, t.OwnerID, t.SecurityID, t.Date, t.Type, t.Quantity, t.PriceCents, t.FeesCents, t.TaxesCents, r.created, r.updated)
	return err
}

func (im *finImport) dividend(ctx context.Context, r legacyRow) error {
	var v struct {
		ID         string `json:"id"`
		SecurityID string `json:"security_id"`
		Date       string `json:"date"`
		Amount     int64  `json:"amount_minor"`
		Currency   string `json:"currency"`
		CreatedAt  string `json:"created_at"`
		UpdatedAt  string `json:"updated_at"`
	}
	if err := decode(r.data, &v); err != nil {
		return err
	}
	cur, ok := im.secCur[v.SecurityID]
	switch {
	case v.ID != "" && v.ID != r.id:
		return errors.New("id im Datensatz weicht ab")
	case !ok:
		return fmt.Errorf("unbekanntes Wertpapier %q", v.SecurityID)
	case v.Currency != "" && v.Currency != cur:
		return fmt.Errorf("Währung %s, das Wertpapier handelt in %s", v.Currency, cur)
	}
	d := finanzen.Dividend{ID: r.id, OwnerID: im.owner, SecurityID: v.SecurityID, Date: v.Date, AmountCents: v.Amount}
	if err := finanzen.CheckDividend(d); err != nil {
		return invalid(err)
	}
	_, err := im.tx.ExecContext(ctx, `INSERT INTO fin_dividends (id, owner_id, security_id, date, amount_cents, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 1, ?, ?)`, d.ID, d.OwnerID, d.SecurityID, d.Date, d.AmountCents, r.created, r.updated)
	return err
}

// yearNumber accepts 2024 or "2024", nothing else.
type yearNumber int

func (y *yearNumber) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	n, err := strconv.Atoi(s)
	if err != nil || len(s) != 4 {
		return fmt.Errorf("year %s is not a four-digit year", b)
	}
	*y = yearNumber(n)
	return nil
}

func (im *finImport) year(ctx context.Context, r legacyRow) error {
	var v struct {
		ID            string     `json:"id"`
		Year          yearNumber `json:"year"`
		Income        int64      `json:"income_minor"`
		IncomeCur     string     `json:"income_currency"`
		Tax           int64      `json:"tax_burden_minor"`
		TaxCur        string     `json:"tax_burden_currency"`
		Pension       int64      `json:"pension_fund_value_minor"`
		PensionCur    string     `json:"pension_fund_value_currency"`
		Cash          int64      `json:"cash_minor"`
		CashCur       string     `json:"cash_currency"`
		Bauspar       int64      `json:"bauspar_minor"`
		BausparCur    string     `json:"bauspar_currency"`
		Stocks        int64      `json:"stocks_minor"`
		StocksCur     string     `json:"stocks_currency"`
		Funds         int64      `json:"funds_minor"`
		FundsCur      string     `json:"funds_currency"`
		RealEstate    int64      `json:"real_estate_minor"`
		RealEstateCur string     `json:"real_estate_currency"`
		CreatedAt     string     `json:"created_at"`
		UpdatedAt     string     `json:"updated_at"`
	}
	if err := decode(r.data, &v); err != nil {
		return err
	}
	if v.ID != "" && v.ID != r.id {
		return errors.New("id im Datensatz weicht ab")
	}
	if err := sameCur(im.env.BaseCurrency, map[string]string{"income_currency": v.IncomeCur, "tax_burden_currency": v.TaxCur,
		"pension_fund_value_currency": v.PensionCur, "cash_currency": v.CashCur, "bauspar_currency": v.BausparCur,
		"stocks_currency": v.StocksCur, "funds_currency": v.FundsCur, "real_estate_currency": v.RealEstateCur}); err != nil {
		return err
	}
	y := finanzen.Year{OwnerID: im.owner, Year: int(v.Year), IncomeCents: v.Income, TaxCents: v.Tax, PensionCents: v.Pension, CashCents: v.Cash,
		BausparCents: v.Bauspar, StocksCents: v.Stocks, FundsCents: v.Funds, RealEstateCents: v.RealEstate}
	if err := finanzen.CheckYear(y); err != nil {
		return invalid(err)
	}
	_, err := im.tx.ExecContext(ctx, `INSERT INTO fin_years (owner_id, year, income_cents, tax_cents, pension_cents, cash_cents, bauspar_cents,
	stocks_cents, funds_cents, real_estate_cents, comment, version, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', 1, ?)`,
		y.OwnerID, y.Year, y.IncomeCents, y.TaxCents, y.PensionCents, y.CashCents, y.BausparCents, y.StocksCents, y.FundsCents, y.RealEstateCents, r.updated)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("Jahr %d kommt doppelt vor", y.Year)
	}
	return err
}

// yearComment attaches a comment to its year. The old app only showed
// comments of recorded years, so one without a year is unexpected.
func (im *finImport) yearComment(ctx context.Context, r legacyRow) error {
	var v struct {
		ID        string     `json:"id"`
		Year      yearNumber `json:"year"`
		Comment   string     `json:"comment"`
		CreatedAt string     `json:"created_at"`
		UpdatedAt string     `json:"updated_at"`
	}
	if err := decode(r.data, &v); err != nil {
		return err
	}
	if len(v.Comment) > 20000 {
		return errors.New("Kommentar ist zu lang")
	}
	res, err := im.tx.ExecContext(ctx, `UPDATE fin_years SET comment = ? WHERE owner_id = ? AND year = ? AND comment = ''`, v.Comment, im.owner, int(v.Year))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("Kommentar zu %d ohne Jahreseintrag (oder doppelt)", v.Year)
	}
	return nil
}

// checkHoldings rejects securities where a sell exceeds the holding.
func (im *finImport) checkHoldings(ctx context.Context) error {
	rows, err := im.tx.QueryContext(ctx, `SELECT id, security_id, date, type, quantity FROM fin_transactions
ORDER BY security_id, date, CASE type WHEN 'buy' THEN 0 ELSE 1 END, created_at, id`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	held := map[string]float64{}
	var bad []string
	for rows.Next() {
		var id, sec, date, typ string
		var q float64
		if err := rows.Scan(&id, &sec, &date, &typ, &q); err != nil {
			return err
		}
		if typ == finanzen.Buy {
			held[sec] += q
			continue
		}
		held[sec] -= q
		if held[sec] < -1e-9 {
			bad = append(bad, id+" ("+date+")")
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("legacy import fin_transactions: sells exceed the holding (fix in the old app, then retry): %s", strings.Join(bad, ", "))
	}
	return nil
}

// caches imports stored prices and exchange rates. Rows keyed "latest" are
// cache entries for "today" in the old app, not dated data: they are skipped.
func (im *finImport) caches(ctx context.Context) error {
	if ok, err := legacyTable(ctx, im.tx, im.env, "cache_stock_prices_live"); err != nil {
		return err
	} else if ok {
		const src = `legacy.cache_stock_prices_live WHERE date != 'latest'`
		if err := rejectCache(ctx, im.tx, "cache_stock_prices_live", "symbol || '@' || date",
			`symbol = '' OR date(date) IS NOT date OR typeof(price) NOT IN ('real', 'integer') OR price <= 0`); err != nil {
			return err
		}
		if _, err := im.tx.ExecContext(ctx, `INSERT INTO fin_prices (symbol, date, close, fetched_at)
SELECT symbol, date, CAST(price AS REAL), fetched_at FROM `+src); err != nil {
			return fmt.Errorf("prices: %w", err)
		}
		if err := sameCount(ctx, im.tx, "prices", src, "fin_prices"); err != nil {
			return err
		}
	}
	if ok, err := legacyTable(ctx, im.tx, im.env, "cache_exchange_rates"); err != nil {
		return err
	} else if ok {
		const src = `legacy.cache_exchange_rates WHERE date != 'latest'`
		if err := rejectCache(ctx, im.tx, "cache_exchange_rates", "base || '/' || target || '@' || date",
			`base NOT GLOB '[A-Z][A-Z][A-Z]' OR target NOT GLOB '[A-Z][A-Z][A-Z]' OR date(date) IS NOT date OR typeof(rate) NOT IN ('real', 'integer') OR rate <= 0`); err != nil {
			return err
		}
		if _, err := im.tx.ExecContext(ctx, `INSERT INTO fin_rates (currency, base, date, rate, fetched_at)
SELECT base, target, date, CAST(rate AS REAL), fetched_at FROM `+src); err != nil {
			return fmt.Errorf("rates: %w", err)
		}
		if err := sameCount(ctx, im.tx, "rates", src, "fin_rates"); err != nil {
			return err
		}
	}
	return nil
}

// rejectCache fails if a dated cache row matches cond, naming up to 20 keys.
func rejectCache(ctx context.Context, tx db.Tx, table, key, cond string) error {
	rows, err := tx.QueryContext(ctx, `SELECT `+key+` FROM legacy.`+table+` WHERE date != 'latest' AND (`+cond+`) LIMIT 20`)
	if err != nil {
		return fmt.Errorf("legacy check %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	var bad []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return err
		}
		bad = append(bad, k)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("legacy import %s: invalid rows: %s", table, strings.Join(bad, ", "))
	}
	return nil
}
