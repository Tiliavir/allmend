package migrations

// v7 creates the finance tables. Until 0.3.0 it also imported the old
// finance data and price and exchange-rate caches (#8).
const v7 = `
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
	notes         TEXT    NOT NULL DEFAULT '',
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
	currency     TEXT    NOT NULL CHECK (length(currency) = 3 AND currency GLOB '[A-Z][A-Z][A-Z]'),
	amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
	version      INTEGER NOT NULL,
	created_at   TEXT    NOT NULL,
	updated_at   TEXT    NOT NULL
) STRICT;
CREATE INDEX fin_dividends_owner ON fin_dividends(owner_id, security_id, date);

CREATE TABLE fin_years (
	owner_id          TEXT    NOT NULL REFERENCES accounts(id),
	year              INTEGER NOT NULL CHECK (year BETWEEN 1900 AND 2200),
	currency          TEXT    NOT NULL CHECK (length(currency) = 3 AND currency GLOB '[A-Z][A-Z][A-Z]'),
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
