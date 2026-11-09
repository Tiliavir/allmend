package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/commute"
	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/links"
	"github.com/tiliavir/allmend/internal/migrations"
	"github.com/tiliavir/allmend/internal/pages"
	"github.com/tiliavir/allmend/internal/places"
	"github.com/tiliavir/allmend/internal/session"
	"github.com/tiliavir/allmend/internal/store"
)

func open(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "allmend.db")
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := db.Migrate(ctx, d, path, migrations.All(), db.Options{BackupDir: filepath.Join(dir, "backup"), Env: db.MigrationEnv{AdminEmail: "admin@example.org"}}); err != nil {
		t.Fatal(err)
	}
	return store.New(d), dir
}

func TestMigrateIdempotentWithBackup(t *testing.T) {
	s, dir := open(t)
	ctx := context.Background()
	res, err := db.Migrate(ctx, s.DB(), filepath.Join(dir, "allmend.db"), migrations.All(), db.Options{BackupDir: filepath.Join(dir, "backup")})
	if err != nil || len(res.Applied) != 0 {
		t.Fatalf("second run: %v %v", res, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "backup"))
	if len(entries) != 1 {
		t.Fatalf("want 1 backup, got %d", len(entries))
	}
}

func TestMigrateRollsBackOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	ctx := context.Background()
	d, _ := db.Open(ctx, path)
	defer func() { _ = d.Close() }()
	ms := []db.Migration{
		{Version: 1, Name: "ok", Up: db.SQL(`CREATE TABLE a (x INTEGER) STRICT`)},
		{Version: 2, Name: "bad", Up: db.SQL(`CREATE TABLE b (`)},
	}
	if _, err := db.Migrate(ctx, d, path, ms, db.Options{BackupDir: dir}); err == nil {
		t.Fatal("want error")
	}
	if v, _ := db.CurrentVersion(ctx, d); v != 0 {
		t.Fatalf("version %d after failed run", v)
	}
	var n int
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'a'`).Scan(&n)
	if n != 0 {
		t.Fatal("table a survived rollback")
	}
}

func TestRefusesNewerDatabase(t *testing.T) {
	s, dir := open(t)
	ctx := context.Background()
	_, _ = s.DB().ExecContext(ctx, `INSERT INTO schema_migrations VALUES (999, 'future', '2030-01-01T00:00:00Z')`)
	if _, err := db.Migrate(ctx, s.DB(), filepath.Join(dir, "allmend.db"), migrations.All(), db.Options{BackupDir: dir}); err == nil {
		t.Fatal("older binary must refuse a newer database")
	}
}

func TestLegacyAttachedReadOnly(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	legacyPath := filepath.Join(dir, "almanach.db")
	l, _ := db.Open(ctx, legacyPath)
	_, _ = l.ExecContext(ctx, `CREATE TABLE tours (id TEXT, data TEXT)`)
	_ = l.Close()
	before, _ := os.ReadFile(legacyPath)

	path := filepath.Join(dir, "allmend.db")
	d, _ := db.Open(ctx, path)
	defer func() { _ = d.Close() }()
	var sawLegacy bool
	ms := []db.Migration{{Version: 1, Name: "probe", Up: func(ctx context.Context, tx db.Tx, env db.MigrationEnv) error {
		sawLegacy = env.LegacyAttached
		if _, err := tx.ExecContext(ctx, `INSERT INTO legacy.tours VALUES ('x', '{}')`); err == nil {
			return errors.New("legacy database is writable")
		}
		return nil
	}}}
	if _, err := db.Migrate(ctx, d, path, ms, db.Options{BackupDir: dir, LegacyDB: legacyPath}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(legacyPath)
	if !sawLegacy || string(before) != string(after) {
		t.Fatal("legacy not attached or modified")
	}
}

func TestAccounts(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	r := s.Accounts()
	now := time.Now().UTC()
	admin := account.Account{ID: "a1", Email: "admin@example.org", IsAdmin: true, Status: account.Active, Modules: []account.Module{account.Velo}, CreatedAt: now, UpdatedAt: now}
	if err := r.Insert(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := r.Insert(ctx, account.Account{ID: "a2", Email: "ADMIN@example.org", Status: account.Invited, CreatedAt: now, UpdatedAt: now}); !errors.Is(err, account.ErrEmailTaken) {
		t.Fatalf("duplicate e-mail: %v", err)
	}
	if err := r.Insert(ctx, account.Account{ID: "a3", Email: "x@example.org", Status: account.Active, Modules: []account.Module{"nope"}, CreatedAt: now, UpdatedAt: now}); !errors.Is(err, account.ErrUnknownMod) {
		t.Fatalf("unknown module: %v", err)
	}
	admin.IsAdmin = false
	if err := r.Update(ctx, admin); !errors.Is(err, account.ErrLastAdmin) {
		t.Fatalf("last admin: %v", err)
	}
	got, err := r.ByEmail(ctx, "Admin@Example.org")
	if err != nil || !got.IsAdmin || len(got.Modules) != 1 {
		t.Fatalf("by email: %+v %v", got, err)
	}
}

func TestSessionsAndFlows(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_ = s.Accounts().Insert(ctx, account.Account{ID: "a1", Email: "a@example.org", IsAdmin: true, Status: account.Active, CreatedAt: now, UpdatedAt: now})
	r := s.Sessions()
	if err := r.Create(ctx, "h1", "a1", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if acc, err := r.Lookup(ctx, "h1", now, now.Add(time.Hour)); err != nil || acc != "a1" {
		t.Fatalf("lookup: %q %v", acc, err)
	}
	if _, err := r.Lookup(ctx, "h1", now.Add(2*time.Hour), now.Add(3*time.Hour)); !errors.Is(err, session.ErrNoSession) {
		t.Fatalf("expired: %v", err)
	}
	f := session.Flow{State: "s", Nonce: "n", Verifier: "v", Created: now}
	_ = r.SaveFlow(ctx, f)
	if got, err := r.TakeFlow(ctx, "s"); err != nil || got.Nonce != "n" {
		t.Fatalf("take: %v", err)
	}
	if _, err := r.TakeFlow(ctx, "s"); !errors.Is(err, session.ErrNoSession) {
		t.Fatal("flow must be single use")
	}
}

func TestLinksOrder(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	svc := links.Service{Repo: s.Links()}
	a, err := svc.Create(ctx, links.Link{Name: "A", URL: "https://a.example", Icon: "link"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := svc.Create(ctx, links.Link{Name: "B", URL: "https://b.example", Icon: "link", Public: true})
	if err := svc.Move(ctx, b.ID, -1); err != nil {
		t.Fatal(err)
	}
	all, _ := svc.Visible(ctx, true)
	if all[0].ID != b.ID || all[1].ID != a.ID {
		t.Fatal("move failed")
	}
	pub, _ := svc.Visible(ctx, false)
	if len(pub) != 1 || pub[0].ID != b.ID {
		t.Fatal("public filter failed")
	}
	if _, err := svc.Create(ctx, links.Link{Name: "X", URL: "javascript:alert(1)", Icon: "link"}); !errors.Is(err, links.ErrInvalid) {
		t.Fatal("javascript URL accepted")
	}
}

func TestPages(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := account.Account{ID: "o", Email: "o@example.org", IsAdmin: true, Status: account.Active, CreatedAt: now, UpdatedAt: now}
	other := account.Account{ID: "x", Email: "x@example.org", Status: account.Active, CreatedAt: now, UpdatedAt: now}
	_ = s.Accounts().Insert(ctx, owner)
	_ = s.Accounts().Insert(ctx, other)
	svc := pages.Service{Repo: s.Pages()}

	p, err := svc.Create(ctx, owner, "Einkaufsliste", "- [ ] Milch\n- [ ] Brot\n")
	if err != nil || p.Slug != "einkaufsliste" {
		t.Fatalf("create: %+v %v", p, err)
	}
	p2, _ := svc.Create(ctx, owner, "Einkaufsliste", "")
	if p2.Slug != "einkaufsliste-2" {
		t.Fatalf("slug collision: %q", p2.Slug)
	}
	// Toggle by line, then by text after a concurrent change.
	if p, err = svc.Toggle(ctx, other, p.Slug, 1, 0, "Milch"); err != nil || p.Body != "- [x] Milch\n- [ ] Brot\n" {
		t.Fatalf("toggle: %q %v", p.Body, err)
	}
	if p, err = svc.Toggle(ctx, other, p.Slug, 1, 0, "Brot"); err != nil || p.Body != "- [x] Milch\n- [x] Brot\n" {
		t.Fatalf("toggle by text: %q %v", p.Body, err)
	}
	// Stale save is rejected.
	if _, err := svc.Save(ctx, owner, p.Slug, 1, "Einkaufsliste", "überschrieben"); !errors.Is(err, pages.ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	// Private page: other loses access, page looks not found.
	if _, err := svc.SetRights(ctx, owner, p.Slug, pages.Rights{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, other, p.Slug); !errors.Is(err, pages.ErrNotFound) {
		t.Fatalf("private page visible: %v", err)
	}
	if _, err := svc.SetRights(ctx, owner, p.Slug, pages.Rights{Readers: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Toggle(ctx, other, p.Slug, 3, 0, "Milch"); !errors.Is(err, pages.ErrForbidden) {
		t.Fatalf("reader toggled: %v", err)
	}
	if err := svc.Delete(ctx, other, p.Slug); !errors.Is(err, pages.ErrForbidden) {
		t.Fatalf("reader deleted: %v", err)
	}
	_, revs, err := svc.Revisions(ctx, owner, p.Slug)
	if err != nil || len(revs) != 3 || revs[0].Version != 3 {
		t.Fatalf("revisions: %d %v", len(revs), err)
	}
	if err := svc.Delete(ctx, owner, p.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, owner, p.Slug); !errors.Is(err, pages.ErrNotFound) {
		t.Fatal("deleted page still visible")
	}
	var kept int
	_ = s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM page_revisions WHERE page_id = ?`, p.ID).Scan(&kept)
	if kept != 3 {
		t.Fatalf("revisions lost on delete: %d", kept)
	}
}

func TestCommuteLegacyImport(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	legacyPath := filepath.Join(dir, "almanach.db")
	l, _ := db.Open(ctx, legacyPath)
	_, err := l.ExecContext(ctx, `CREATE TABLE commute_years (id TEXT PRIMARY KEY, data TEXT NOT NULL, schema_version INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
INSERT INTO commute_years VALUES ('old-1', '{"id":"old-1","year":2025,"car_dist_km":0,"bike_dist_km":20,"workdays":[20,19,21,0,0,0,0,0,0,0,0,0],"car_train":[13,10,0,0,0,0,0,0,0,0,0,0],"ho":[7,9,21,0,0,0,0,0,0,0,0,0]}', 1, '2025-02-01T10:00:00Z', '2025-03-01T10:00:00Z'),
	('old-2', '{"id":"old-2","year":2024,"pendler_low_cents":31,"pendler_high_cents":0,"pendler_threshold_km":15}', 1, '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');`)
	_ = l.Close()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "allmend.db")
	d, _ := db.Open(ctx, path)
	defer func() { _ = d.Close() }()
	if _, err := db.Migrate(ctx, d, path, migrations.All(), db.Options{BackupDir: dir, LegacyDB: legacyPath, Env: db.MigrationEnv{AdminEmail: "admin@example.org"}}); err != nil {
		t.Fatal(err)
	}
	s := store.New(d)
	admin, err := s.Accounts().ByEmail(ctx, "admin@example.org")
	if err != nil || !admin.IsAdmin {
		t.Fatalf("admin created by import: %+v %v", admin, err)
	}
	y, err := s.Commute().Get(ctx, admin.ID, 2025)
	if err != nil {
		t.Fatal(err)
	}
	if y.CarDistKm != 7.6 || y.TrainDistKm != 15 || y.BikeDistKm != 20 || y.RateLowCents != nil || y.ThresholdKm != nil {
		t.Errorf("defaults: %+v", y)
	}
	if y.Months[0].CarTrain != 13 || y.Months[2].HomeOffice != 21 || y.Months[11].Workdays != 0 {
		t.Errorf("months: %+v", y.Months)
	}
	if v := commute.Compute(y); v.Derived[0].PendlerCents != 13*(20*30+2*38) {
		t.Errorf("pendler 2025: %d", v.Derived[0].PendlerCents)
	}
	y24, _ := s.Commute().Get(ctx, admin.ID, 2024)
	if y24.RateLowCents == nil || *y24.RateLowCents != 31 || *y24.RateHighCents != 0 || *y24.ThresholdKm != 15 {
		t.Errorf("2024 overrides: %+v", y24)
	}
}

func TestCommuteConflict(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	a := account.Account{ID: "a", Email: "a@example.org", IsAdmin: true, Status: account.Active, CreatedAt: now, UpdatedAt: now}
	_ = s.Accounts().Insert(ctx, a)
	svc := commute.Service{Repo: s.Commute()}
	y, err := svc.Create(ctx, a, commute.Year{Year: 2026, CarDistKm: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, a, commute.Year{Year: 2026}); !errors.Is(err, commute.ErrExists) {
		t.Fatalf("duplicate year: %v", err)
	}
	y.Months[0].Workdays = 20
	if _, err := svc.Save(ctx, a, y, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Save(ctx, a, y, 1); !errors.Is(err, commute.ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	d, _ := svc.Draft(ctx, a, 2027)
	if d.CarDistKm != 5 {
		t.Errorf("draft prefill: %+v", d)
	}
}

func legacyDB(t *testing.T, dir, stmts string) string {
	t.Helper()
	ctx := context.Background()
	p := filepath.Join(dir, "almanach.db")
	l, err := db.Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	for _, tbl := range []string{"bikes", "tours", "repairs", "bike_year_comments", "bike_challenges"} {
		if _, err := l.ExecContext(ctx, `CREATE TABLE `+tbl+` (id TEXT PRIMARY KEY, data TEXT NOT NULL, schema_version INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.ExecContext(ctx, stmts); err != nil {
		t.Fatal(err)
	}
	return p
}

const ts0 = `'2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z'`

func TestVeloLegacyImport(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	legacy := legacyDB(t, dir, `
INSERT INTO bikes VALUES ('b1', '{"id":"b1","name":"Gravel","brand":"Feldweg","acquisition_date":"2023-04-01","purchase_price_minor":250000,"purchase_price_currency":"EUR","color":"#5C7A3E"}', 1, `+ts0+`);
INSERT INTO tours VALUES ('t1', '{"id":"t1","bike_id":"b1","date":"2024-05-03","distance_km":42.5,"elevation_m":610,"title":"Blauen"}', 1, `+ts0+`),
	('t2', '{"id":"t2","date":"2024-05-04","distance_km":10}', 1, `+ts0+`);
INSERT INTO repairs VALUES ('r1', '{"id":"r1","bike_id":"b1","date":"2024-06-01","cost_minor":4590,"cost_currency":"EUR","title":"Kette","odometer_km":3100.5}', 1, `+ts0+`);
INSERT INTO bike_year_comments VALUES ('c1', '{"id":"c1","year":"2024","comment":"**Gutes** Jahr"}', 1, `+ts0+`);
INSERT INTO bike_challenges VALUES ('g1', '{"id":"g1","year":"2024","month":"05","target_km":300}', 1, `+ts0+`),
	('g2', '{"id":"g2","year":"2024","month":"06","target_km":0}', 1, `+ts0+`);`)
	path := filepath.Join(dir, "allmend.db")
	d, _ := db.Open(ctx, path)
	defer func() { _ = d.Close() }()
	if _, err := db.Migrate(ctx, d, path, migrations.All(), db.Options{BackupDir: dir, LegacyDB: legacy, Env: db.MigrationEnv{AdminEmail: "admin@example.org", BaseCurrency: "EUR"}}); err != nil {
		t.Fatal(err)
	}
	s := store.New(d)
	admin, _ := s.Accounts().ByEmail(ctx, "admin@example.org")
	r := s.Velo()
	b, err := r.Bike(ctx, admin.ID, "b1")
	if err != nil || b.PurchaseCents != 250000 || b.Color != "#5c7a3e" || b.Acquired != "2023-04-01" {
		t.Fatalf("bike: %+v %v", b, err)
	}
	ts, _ := r.Tours(ctx, admin.ID)
	if len(ts) != 2 || ts[1].ElevationM != 610 || ts[1].BikeID != "b1" || ts[0].BikeID != "" {
		t.Fatalf("tours: %+v", ts)
	}
	reps, _ := r.Repairs(ctx, admin.ID, "b1")
	if len(reps) != 1 || reps[0].CostCents != 4590 || *reps[0].OdometerKm != 3100.5 {
		t.Fatalf("repairs: %+v", reps)
	}
	if c, _ := r.Comment(ctx, admin.ID, 2024); c != "**Gutes** Jahr" {
		t.Errorf("comment %q", c)
	}
	if g, _ := r.Goal(ctx, admin.ID, 2024, 5); g != 300 {
		t.Errorf("goal %v", g)
	}
	// Another owner sees nothing.
	if other, _ := r.Tours(ctx, "someone-else"); len(other) != 0 {
		t.Error("tours leak across owners")
	}
}

func TestVeloLegacyBadDateStops(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	legacy := legacyDB(t, dir, `INSERT INTO tours VALUES ('t1', '{"id":"t1","date":"2024-13-03","distance_km":4}', 1, `+ts0+`);`)
	path := filepath.Join(dir, "allmend.db")
	d, _ := db.Open(ctx, path)
	defer func() { _ = d.Close() }()
	_, err := db.Migrate(ctx, d, path, migrations.All(), db.Options{BackupDir: dir, LegacyDB: legacy, Env: db.MigrationEnv{AdminEmail: "admin@example.org"}})
	if err == nil || !strings.Contains(err.Error(), "t1=2024-13-03") {
		t.Fatalf("want date error, got %v", err)
	}
	if v, _ := db.CurrentVersion(ctx, d); v != 0 {
		t.Fatalf("partial migration: version %d", v)
	}
}

func TestVeloLegacyRejectsUnexpected(t *testing.T) {
	cases := map[string]string{
		"orphan tour":   `INSERT INTO tours VALUES ('t9', '{"id":"t9","bike_id":"gone","date":"2024-05-04","distance_km":10}', 1, ` + ts0 + `);`,
		"chf bike":      `INSERT INTO bikes VALUES ('b9', '{"id":"b9","name":"X","purchase_price_minor":100,"purchase_price_currency":"CHF"}', 1, ` + ts0 + `);`,
		"text distance": `INSERT INTO tours VALUES ('t9', '{"id":"t9","date":"2024-05-04","distance_km":"10"}', 1, ` + ts0 + `);`,
		"no distance":   `INSERT INTO tours VALUES ('t9', '{"id":"t9","date":"2024-05-04"}', 1, ` + ts0 + `);`,
		"orphan repair": `INSERT INTO repairs VALUES ('r9', '{"id":"r9","bike_id":"gone","date":"2024-05-04","cost_minor":1,"title":"x"}', 1, ` + ts0 + `);`,
		"float cost":    `INSERT INTO bikes VALUES ('b1', '{"id":"b1","name":"X"}', 1, ` + ts0 + `); INSERT INTO repairs VALUES ('r9', '{"id":"r9","bike_id":"b1","date":"2024-05-04","cost_minor":1.5,"title":"x"}', 1, ` + ts0 + `);`,
		"month 13 goal": `INSERT INTO bike_challenges VALUES ('g9', '{"id":"g9","year":"2024","month":"13","target_km":100}', 1, ` + ts0 + `);`,
		"nameless bike": `INSERT INTO bikes VALUES ('b9', '{"id":"b9","name":" "}', 1, ` + ts0 + `);`,
	}
	for name, stmt := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			ctx := context.Background()
			legacy := legacyDB(t, dir, stmt)
			path := filepath.Join(dir, "allmend.db")
			d, _ := db.Open(ctx, path)
			defer func() { _ = d.Close() }()
			_, err := db.Migrate(ctx, d, path, migrations.All(), db.Options{BackupDir: dir, LegacyDB: legacy, Env: db.MigrationEnv{AdminEmail: "admin@example.org", BaseCurrency: "EUR"}})
			if err == nil || !strings.Contains(err.Error(), "9") {
				t.Fatalf("want rejection naming the row, got %v", err)
			}
			if v, _ := db.CurrentVersion(ctx, d); v != 0 {
				t.Fatalf("partial migration: version %d", v)
			}
		})
	}
}

func TestPlacesLegacyImport(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	p := filepath.Join(dir, "almanach.db")
	l, _ := db.Open(ctx, p)
	for _, tbl := range []string{"places", "place_lists", "place_list_places", "place_visits"} {
		_, _ = l.ExecContext(ctx, `CREATE TABLE `+tbl+` (id TEXT PRIMARY KEY, data TEXT NOT NULL, schema_version INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`)
	}
	_, err := l.ExecContext(ctx, `
INSERT INTO places VALUES ('p1', '{"name":"Basel","latitude":47.5596,"longitude":7.5886}', 1, `+ts0+`);
INSERT INTO place_lists VALUES ('l1', '{"name":"Rhein","date_from":"2024-05-01","date_to":"","source_file":"trip.json"}', 1, `+ts0+`);
INSERT INTO place_list_places VALUES ('j1', '{"place_list_id":"l1","place_id":"p1"}', 1, `+ts0+`);
INSERT INTO place_visits VALUES ('v1', '{"latitude":47.5,"longitude":7.6,"timestamp":"2024-05-03T10:00:00Z","source_file":"trip.json"}', 1, `+ts0+`);`)
	_ = l.Close()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "allmend.db")
	d, _ := db.Open(ctx, path)
	defer func() { _ = d.Close() }()
	if _, err := db.Migrate(ctx, d, path, migrations.All(), db.Options{BackupDir: dir, LegacyDB: p, Env: db.MigrationEnv{AdminEmail: "admin@example.org"}}); err != nil {
		t.Fatal(err)
	}
	s := store.New(d)
	admin, _ := s.Accounts().ByEmail(ctx, "admin@example.org")
	list, ps, err := s.Places().List(ctx, admin.ID, "l1")
	if err != nil || len(ps) != 1 || ps[0].Name != "Basel" || list.SourceFile != "trip.json" {
		t.Fatalf("list %+v %+v %v", list, ps, err)
	}
	vs, _ := s.Places().VisitsBySource(ctx, admin.ID, "trip.json")
	if len(vs) != 1 || vs[0].Timestamp.Hour() != 10 {
		t.Fatalf("visits %+v", vs)
	}
	day, _ := time.Parse(time.DateOnly, "2024-05-03")
	if on, _ := s.Places().VisitsOn(ctx, admin.ID, day, day.AddDate(0, 0, 1)); len(on) != 1 {
		t.Errorf("visits on day: %d", len(on))
	}
	// Re-import of the same point is skipped.
	n, err := s.Places().InsertVisits(ctx, []places.Visit{{ID: "x", OwnerID: admin.ID, Latitude: 47.5, Longitude: 7.6, Timestamp: vs[0].Timestamp, SourceFile: "trip.json"}})
	if err != nil || n != 0 {
		t.Errorf("duplicate visit: %d %v", n, err)
	}
}
