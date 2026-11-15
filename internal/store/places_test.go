package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/migrations"
	"github.com/tiliavir/allmend/internal/places"
	"github.com/tiliavir/allmend/internal/store"
)

// v14Setup migrates to v13, runs the legacy inserts and then the rest.
func v14Setup(t *testing.T, legacy string) (*store.Store, error) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(dir, "allmend.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	opt := db.Options{BackupDir: filepath.Join(dir, "backup")}
	all := migrations.All()
	if _, err := db.Migrate(ctx, d, all[:13], opt); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO accounts (id, email, is_admin, status, created_at, updated_at) VALUES ('anna', 'anna@example.org', 1, 'active', `+ts0+`);`+legacy); err != nil {
		t.Fatal(err)
	}
	_, err = db.Migrate(ctx, d, all, opt)
	return store.New(d), err
}

func TestPlacesMigration(t *testing.T) {
	ctx := context.Background()
	s, err := v14Setup(t, `
INSERT INTO places (id, owner_id, name, latitude, longitude, created_at, updated_at) VALUES ('p1', 'anna', 'Basel', 47.5596, 7.5886, `+ts0+`);
INSERT INTO place_lists (id, owner_id, name, date_from, source_file, created_at, updated_at) VALUES ('l1', 'anna', 'Rhein', '2024-05-01', 'weg.kmz', `+ts0+`);
INSERT INTO place_list_places (list_id, place_id) VALUES ('l1', 'p1');
INSERT INTO place_visits (id, owner_id, latitude, longitude, at, seq, source_file) VALUES
	('v1', 'anna', 47.5, 7.6, '2024-05-03T22:30:00.000Z', NULL, 'Records.json'),
	('v2', 'anna', 46.2, 7.8, NULL, 1, 'weg.kmz'),
	('v3', 'anna', 46.1, 7.8, NULL, 0, 'weg.kmz');`)
	if err != nil {
		t.Fatal(err)
	}
	p := s.Places()
	l, ps, err := p.List(ctx, "anna", "l1")
	if err != nil || len(ps) != 1 || l.TrackCount != 2 || !l.MissingRange() {
		t.Fatalf("trip %+v %v", l, err)
	}
	if tr, _ := p.Track(ctx, "anna", "l1"); len(tr) != 2 || tr[0].Latitude != 46.1 || !tr[0].At.IsZero() {
		t.Errorf("track %+v", tr)
	}
	// 22:30 UTC on 3 May is 4 May in Zurich.
	if days, _ := p.Days(ctx, "anna"); len(days) != 1 || days[0] != "2024-05-04" {
		t.Errorf("days %v", days)
	}
	if imps, _ := p.Imports(ctx, "anna"); len(imps) != 1 || imps[0].Kind != "records" || imps[0].Points != 1 || imps[0].FileName != "Records.json" {
		t.Errorf("imports %+v", imps)
	}
	// Changing the range needs both dates.
	l.Name, l.DateTo = "Rhein", ""
	if err := p.UpdateTrip(ctx, l); err == nil {
		t.Error("incomplete range saved")
	}
	l.DateTo = "2024-05-02"
	if err := p.UpdateTrip(ctx, l); err != nil {
		t.Error(err)
	}

	for name, legacy := range map[string]string{
		"untimed point of no trip": `INSERT INTO place_visits (id, owner_id, latitude, longitude, at, seq, source_file) VALUES ('v', 'anna', 1, 1, NULL, 0, 'x.kmz');`,
		"inverted range":           `INSERT INTO place_lists (id, owner_id, name, date_from, date_to, created_at, updated_at) VALUES ('l', 'anna', 'X', '2024-05-02', '2024-05-01', ` + ts0 + `);`,
	} {
		if _, err := v14Setup(t, legacy); err == nil {
			t.Errorf("%s: migration accepted", name)
		}
	}
}

func TestPlacesTripsAndHistory(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"anna", "ben"} {
		if err := s.Accounts().Insert(ctx, account.Account{ID: id, Email: id + "@example.org", IsAdmin: id == "anna", Status: account.Active, Theme: account.ThemeAuto, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	zurich, _ := time.LoadLocation("Europe/Zurich")
	svc := &places.Service{Repo: s.Places(), Now: func() time.Time { return now }, Loc: zurich}
	anna := account.Account{ID: "anna"}

	kml := `<kml><Document><name>See</name><TimeSpan><begin>2025-09-20</begin><end>2025-09-22</end></TimeSpan>
<Placemark><name>Konstanz</name><Point><coordinates>9.17,47.66</coordinates></Point></Placemark>
<Placemark><name>Weg</name><LineString><coordinates>9.17,47.66 9.27,47.69</coordinates></LineString></Placemark></Document></kml>`
	l, err := svc.ImportTrip(ctx, anna, `C:\Reisen\see.kml`, []byte(kml), places.TripForm{Description: "Mit Velo"})
	if err != nil || l.Name != "See" || l.Description != "Mit Velo" || l.SourceFile != "see.kml" || l.DateTo != "2025-09-22" {
		t.Fatalf("import %+v %v", l, err)
	}
	if _, err := svc.ImportTrip(ctx, anna, "again.kml", []byte(kml), places.TripForm{}); !errors.Is(err, places.ErrDuplicate) {
		t.Errorf("same file twice: %v", err)
	}
	noRange := strings.Replace(kml, "<TimeSpan><begin>2025-09-20</begin><end>2025-09-22</end></TimeSpan>", "", 1)
	var inv *places.InvalidError
	if _, err := svc.ImportTrip(ctx, anna, "b.kml", []byte(noRange), places.TripForm{}); !errors.As(err, &inv) {
		t.Errorf("trip without range: %v", err)
	}
	if _, err := svc.ImportTrip(ctx, anna, "b.kml", []byte(noRange), places.TripForm{DateFrom: "2025-01-01", DateTo: "2025-01-03"}); err != nil {
		t.Errorf("range from form: %v", err)
	}
	if _, _, err := s.Places().List(ctx, "ben", l.ID); !errors.Is(err, places.ErrNotFound) {
		t.Error("other owner sees trip")
	}

	seg := func(at, lat string) string {
		return `{"startTime":"` + at + `","endTime":"` + at + `","timelinePath":[{"point":"` + lat + `°, 7.5°","time":"` + at + `"}]}`
	}
	file := func(segs ...string) *strings.Reader {
		return strings.NewReader(`{"semanticSegments":[` + strings.Join(segs, ",") + `]}`)
	}
	r1, err := svc.ImportTimeline(ctx, anna, "Timeline.json", file(seg("2026-11-01T10:00:00.000+01:00", "47.1"), seg("2026-11-02T10:00:00.000+01:00", "47.2"),
		`{"startTime":"2026-11-02T11:00:00.000+01:00","endTime":"2026-11-03T08:00:00.000+01:00","visit":{"hierarchyLevel":0,"topCandidate":{"semanticType":"WORK","placeLocation":{"latLng":"47.3°, 7.5°"}}}}`))
	if err != nil || r1.Import.Points != 2 || r1.Import.Stays != 1 || r1.Gap {
		t.Fatalf("first import %+v %v", r1, err)
	}
	// Append only: older entries are skipped; the newest stay start is the mark.
	r2, err := svc.ImportTimeline(ctx, anna, "Timeline.json", file(seg("2026-11-02T10:00:00.000+01:00", "47.2"), seg("2026-11-02T10:30:00.000+01:00", "47.9"), seg("2026-11-04T10:00:00.000+01:00", "47.4")))
	if err != nil || r2.Import.Points != 1 || r2.Import.Skipped != 2 || r2.Gap {
		t.Fatalf("second import %+v %v", r2, err)
	}
	r3, err := svc.ImportTimeline(ctx, anna, "Timeline.json", file(seg("2026-11-10T10:00:00.000+01:00", "47.5")))
	if err != nil || !r3.Gap || r3.Import.Points != 1 {
		t.Fatalf("gap %+v %v", r3, err)
	}
	r4, err := svc.ImportTimeline(ctx, anna, "Timeline.json", file(seg("2026-11-10T10:00:00.000+01:00", "47.5")))
	if err != nil || r4.Import.ID != "" || r4.Import.Skipped != 1 {
		t.Fatalf("nothing new %+v %v", r4, err)
	}
	// A bad entry at the end: the points written before it are rolled back.
	if _, err := svc.ImportTimeline(ctx, anna, "Timeline.json", file(seg("2026-11-11T10:00:00.000+01:00", "47.6"), seg("2026-11-12T10:00:00.000+01:00", "95.0"))); err == nil {
		t.Fatal("bad entry accepted")
	}
	p := s.Places()
	if days, _ := p.Days(ctx, "anna"); strings.Join(days, " ") != "2026-11-01 2026-11-02 2026-11-04 2026-11-10" {
		t.Errorf("days %v", days)
	}
	if st, _ := p.StaysOn(ctx, "anna", "2026-11-03"); len(st) != 1 || st[0].Label() != "Arbeit" {
		t.Errorf("stay over midnight %+v", st)
	}
	if pts, _ := p.PointsBetween(ctx, "anna", "2026-11-01", "2026-11-02"); len(pts) != 2 || *pts[0].Offset != 60 {
		t.Errorf("between %+v", pts)
	}
	if days, _ := p.Days(ctx, "ben"); len(days) != 0 {
		t.Error("other owner sees history")
	}
	// Undo the third import: the mark falls back, so the same file adds it again.
	if err := p.DeleteImport(ctx, "anna", r3.Import.ID); err != nil {
		t.Fatal(err)
	}
	if r5, _ := svc.ImportTimeline(ctx, anna, "Timeline.json", file(seg("2026-11-10T10:00:00.000+01:00", "47.5"))); r5.Import.Points != 1 {
		t.Errorf("after undo %+v", r5)
	}
	if err := p.DeleteImport(ctx, "ben", r1.Import.ID); !errors.Is(err, places.ErrNotFound) {
		t.Errorf("other owner deleted import: %v", err)
	}

	cells, err := svc.Heat(ctx, anna, 3, places.Box{South: -90, West: -180, North: 90, East: 180})
	total := 0
	for _, c := range cells {
		total += c.N
	}
	// 4 points + 2 trips × (1 place + 2 track points)
	if err != nil || total != 10 {
		t.Errorf("heat %d %v", total, err)
	}
	if b, ok, _ := p.Bounds(ctx, "anna"); !ok || b.South != 47.1 || b.East != 9.27 {
		t.Errorf("bounds %+v", b)
	}

	if err := p.DeleteTrip(ctx, "ben", l.ID); !errors.Is(err, places.ErrNotFound) {
		t.Error("other owner deleted trip")
	}
	if err := p.DeleteTrip(ctx, "anna", l.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM places WHERE name = 'Konstanz'`).Scan(&left)
	if left != 1 {
		t.Errorf("places left %d (want only the other trip's)", left)
	}
}
