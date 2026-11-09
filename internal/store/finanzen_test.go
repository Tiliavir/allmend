package store_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/migrations"
	"github.com/tiliavir/allmend/internal/store"
)

var finTables = []string{"fin_accounts", "fin_securities", "fin_transactions", "fin_dividends", "fin_bauspar", "fin_pensions", "fin_assets", "fin_annual", "fin_annual_comments"}

func finLegacy(t *testing.T, dir, stmts string) string {
	t.Helper()
	ctx := context.Background()
	p := filepath.Join(dir, "almanach.db")
	l, err := db.Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	for _, tbl := range finTables {
		if _, err := l.ExecContext(ctx, `CREATE TABLE `+tbl+` (id TEXT PRIMARY KEY, data TEXT NOT NULL, schema_version INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.ExecContext(ctx, `CREATE TABLE cache_stock_prices_live (symbol TEXT NOT NULL, date TEXT NOT NULL, price REAL NOT NULL, fetched_at TEXT NOT NULL, PRIMARY KEY (symbol, date));
CREATE TABLE cache_exchange_rates (base TEXT NOT NULL, target TEXT NOT NULL, date TEXT NOT NULL, rate REAL NOT NULL, fetched_at TEXT NOT NULL, PRIMARY KEY (base, target, date));`); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ExecContext(ctx, stmts); err != nil {
		t.Fatal(err)
	}
	return p
}

func migrateWith(t *testing.T, dir, legacy string) (*store.Store, error) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(dir, "allmend.db")
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	_, err = db.Migrate(ctx, d, path, migrations.All(), db.Options{BackupDir: dir, LegacyDB: legacy, Env: db.MigrationEnv{AdminEmail: "admin@example.org", BaseCurrency: "EUR"}})
	return store.New(d), err
}

const finGood = `
INSERT INTO fin_accounts VALUES ('a1', '{"id":"a1","name":"Giro","currency":"EUR","balance_minor":123456,"as_of":"2025-12-31","cost_currency":"EUR","created_at":"x","updated_at":"y"}', 2, ` + ts0 + `);
INSERT INTO fin_bauspar VALUES ('b1', '{"id":"b1","name":"LBS","currency":"EUR","value_minor":500000,"maturity_date":"2030-01-01","paid_in_minor":450000,"paid_in_currency":"EUR"}', 2, ` + ts0 + `);
INSERT INTO fin_pensions VALUES ('p1', '{"id":"p1","name":"3a","currency":"CHF","value_minor":2000000}', 2, ` + ts0 + `);
INSERT INTO fin_assets VALUES ('m1', '{"id":"m1","name":"Reka","category":"Reka","currency":"CHF","value_minor":30000}', 2, ` + ts0 + `);
INSERT INTO fin_securities VALUES ('s1', '{"id":"s1","name":"Welt","isin":"IE00BK5BQT80","kind":"etf","currency":"EUR","price_connector_id":"stock-prices-live","price_symbol":"VWCE.DEX","archived":false,"risk_override":0,"composition_json":"{\"Sectors\":[{\"Label\":\"Tech\",\"Weight\":0.3}],\"Countries\":[],\"Currencies\":[],\"TopHoldings\":[]}"}', 2, ` + ts0 + `);
INSERT INTO fin_securities VALUES ('s2', '{"id":"s2","name":"Alt","kind":"stock","currency":"USD","archived":true,"archived_at":"2024-06-01"}', 2, ` + ts0 + `);
INSERT INTO fin_transactions VALUES ('t1', '{"id":"t1","security_id":"s1","date":"2024-01-02","type":"buy","quantity":10.5,"currency":"EUR","price_per_unit_minor":10000,"fees_minor":500,"taxes_minor":0}', 1, ` + ts0 + `);
INSERT INTO fin_transactions VALUES ('t2', '{"id":"t2","security_id":"s1","date":"2024-03-02","type":"sell","quantity":10.5,"currency":"EUR","price_per_unit_minor":11000,"fees_minor":0,"taxes_minor":0}', 1, ` + ts0 + `);
INSERT INTO fin_dividends VALUES ('d1', '{"id":"d1","security_id":"s1","date":"2024-02-01","amount_minor":1230,"currency":"EUR"}', 1, ` + ts0 + `);
INSERT INTO fin_annual VALUES ('y1', '{"id":"y1","year":2024,"income_minor":9000000,"income_currency":"EUR","cash_minor":100,"cash_currency":"EUR"}', 2, ` + ts0 + `);
INSERT INTO fin_annual_comments VALUES ('c1', '{"id":"c1","year":"2024","comment":"Gutes Jahr"}', 1, ` + ts0 + `);
INSERT INTO cache_stock_prices_live VALUES ('VWCE.DEX', '2024-01-02', 100.5, 'x'), ('VWCE.DEX', 'latest', 101, 'x');
INSERT INTO cache_exchange_rates VALUES ('CHF', 'EUR', '2024-01-02', 1.05, 'x'), ('CHF', 'EUR', 'latest', 1.06, 'x');
`

func TestFinanzenLegacyImport(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	st, err := migrateWith(t, dir, finLegacy(t, dir, finGood))
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Accounts().ByEmail(ctx, "admin@example.org")
	if err != nil {
		t.Fatal(err)
	}
	f := st.Finanzen()
	as, _ := f.Assets(ctx, a.ID)
	if len(as) != 4 || as[0].Kind != "konto" || as[0].ValueCents != 123456 || as[1].MaturityDate != "2030-01-01" || as[3].Category != "Reka" {
		t.Fatalf("assets %+v", as)
	}
	secs, _ := f.Securities(ctx, a.ID)
	if len(secs) != 2 || !secs[0].Archived || secs[1].Symbol != "VWCE.DEX" || len(secs[1].Weights) != 1 || secs[1].Weights[0].Weight != 0.3 {
		t.Fatalf("securities %+v", secs)
	}
	txs, _ := f.Transactions(ctx, a.ID)
	if len(txs) != 2 || txs[0].Quantity != 10.5 {
		t.Fatalf("transactions %+v", txs)
	}
	ys, _ := f.Years(ctx, a.ID)
	if len(ys) != 1 || ys[0].Comment != "Gutes Jahr" || ys[0].IncomeCents != 9000000 {
		t.Fatalf("years %+v", ys)
	}
	ps, _ := f.Prices(ctx, "VWCE.DEX")
	if len(ps) != 1 || ps[0].Close != 100.5 {
		t.Fatalf("prices %+v", ps)
	}
	if r, err := f.Rate(ctx, "CHF", "EUR"); err != nil || r.Rate != 1.05 {
		t.Fatalf("rate %+v %v", r, err)
	}
}

func TestFinanzenLegacyRejectsUnexpected(t *testing.T) {
	cases := map[string]string{
		"unknown field":     `INSERT INTO fin_accounts VALUES ('a9', '{"id":"a9","name":"G","currency":"EUR","balance_minor":1,"iban":"x"}', 1, ` + ts0 + `);`,
		"mixed currency":    `INSERT INTO fin_accounts VALUES ('a9', '{"id":"a9","name":"G","currency":"EUR","balance_minor":1,"cost_minor":1,"cost_currency":"CHF"}', 1, ` + ts0 + `);`,
		"float amount":      `INSERT INTO fin_bauspar VALUES ('b9', '{"id":"b9","name":"L","currency":"EUR","value_minor":1.5}', 1, ` + ts0 + `);`,
		"negative value":    `INSERT INTO fin_assets VALUES ('m9', '{"id":"m9","name":"L","currency":"EUR","value_minor":-1}', 1, ` + ts0 + `);`,
		"bad kind":          `INSERT INTO fin_securities VALUES ('s9', '{"id":"s9","name":"X","kind":"bond","currency":"EUR"}', 1, ` + ts0 + `);`,
		"bad isin":          `INSERT INTO fin_securities VALUES ('s9', '{"id":"s9","name":"X","kind":"etf","currency":"EUR","isin":"DE12"}', 1, ` + ts0 + `);`,
		"bad composition":   `INSERT INTO fin_securities VALUES ('s9', '{"id":"s9","name":"X","kind":"etf","currency":"EUR","composition_json":"{\"Sectors\":[{\"Label\":\"A\",\"Weight\":30}]}"}', 1, ` + ts0 + `);`,
		"orphan tx":         `INSERT INTO fin_transactions VALUES ('t9', '{"id":"t9","security_id":"gone","date":"2024-01-02","type":"buy","quantity":1,"price_per_unit_minor":1}', 1, ` + ts0 + `);`,
		"short sell":        `INSERT INTO fin_securities VALUES ('s1', '{"id":"s1","name":"X","kind":"etf","currency":"EUR"}', 1, ` + ts0 + `); INSERT INTO fin_transactions VALUES ('t9', '{"id":"t9","security_id":"s1","date":"2024-01-02","type":"sell","quantity":1,"price_per_unit_minor":1}', 1, ` + ts0 + `);`,
		"tx currency":       `INSERT INTO fin_securities VALUES ('s1', '{"id":"s1","name":"X","kind":"etf","currency":"EUR"}', 1, ` + ts0 + `); INSERT INTO fin_transactions VALUES ('t9', '{"id":"t9","security_id":"s1","date":"2024-01-02","type":"buy","quantity":1,"currency":"USD","price_per_unit_minor":1}', 1, ` + ts0 + `);`,
		"zero dividend":     `INSERT INTO fin_securities VALUES ('s1', '{"id":"s1","name":"X","kind":"etf","currency":"EUR"}', 1, ` + ts0 + `); INSERT INTO fin_dividends VALUES ('d9', '{"id":"d9","security_id":"s1","date":"2024-01-02","amount_minor":0}', 1, ` + ts0 + `);`,
		"annual chf":        `INSERT INTO fin_annual VALUES ('y9', '{"id":"y9","year":2024,"cash_minor":1,"cash_currency":"CHF"}', 1, ` + ts0 + `);`,
		"orphan comment":    `INSERT INTO fin_annual_comments VALUES ('c9', '{"id":"c9","year":"2019","comment":"x"}', 1, ` + ts0 + `);`,
		"bad cached price9": `INSERT INTO fin_accounts VALUES ('a1', '{"id":"a1","name":"G","currency":"EUR"}', 1, ` + ts0 + `); INSERT INTO cache_stock_prices_live VALUES ('X9', '2024-01-02', -1, 'x');`,
	}
	for name, stmt := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			ctx := context.Background()
			_, err := migrateWith(t, dir, finLegacy(t, dir, stmt))
			if err == nil || !strings.Contains(err.Error(), "9") {
				t.Fatalf("want rejection naming the row, got %v", err)
			}
			d, _ := db.Open(ctx, filepath.Join(dir, "allmend.db"))
			defer func() { _ = d.Close() }()
			if v, _ := db.CurrentVersion(ctx, d); v != 0 {
				t.Fatalf("partial migration: version %d", v)
			}
		})
	}
}
