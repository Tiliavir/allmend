// Package demo builds the public demo: a fictional family's data, entered
// through the domain services (so it passes the same validation as user
// input), and a static export of the rendered pages for GitHub Pages.
package demo

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/ausgaben"
	"github.com/tiliavir/allmend/internal/commute"
	"github.com/tiliavir/allmend/internal/finanzen"
	"github.com/tiliavir/allmend/internal/links"
	"github.com/tiliavir/allmend/internal/pages"
	"github.com/tiliavir/allmend/internal/places"
	"github.com/tiliavir/allmend/internal/schiffe"
	"github.com/tiliavir/allmend/internal/velo"
)

// AdminEmail is the demo's first admin; the export shows the app as this account.
const AdminEmail = "anna@example.org"

// Deps are the services the seed writes through.
type Deps struct {
	Accounts *account.Service
	Links    *links.Service
	Pages    *pages.Service
	Commute  *commute.Service
	Velo     *velo.Service
	Finanzen *finanzen.Service
	Schiffe  *schiffe.Service
	Ausgaben *ausgaben.Service
	Places   *places.Service
}

type seeder struct {
	Deps
	ctx   context.Context
	rnd   *rand.Rand
	today time.Time // local date, midnight
	loc   *time.Location
}

// Seed fills an empty database with the demo family. The admin must exist
// (Accounts.EnsureAdmin with AdminEmail). Dates are relative to now, so the
// current year always has data; the same seed gives the same data.
func Seed(ctx context.Context, d Deps, now time.Time, loc *time.Location) (account.Account, error) {
	y, m, day := now.In(loc).Date()
	s := &seeder{Deps: d, ctx: ctx, rnd: rand.New(rand.NewPCG(1866, 2026)), today: time.Date(y, m, day, 0, 0, 0, 0, time.UTC), loc: loc} //nolint:gosec // demo data, not security
	anna, err := d.Accounts.Repo.ByEmail(ctx, AdminEmail)
	if err != nil {
		return anna, fmt.Errorf("demo: admin: %w", err)
	}
	steps := []struct {
		name string
		run  func(*family) error
	}{
		{"accounts", s.accounts},
		{"links", s.links},
		{"pages", s.pages},
		{"velo", s.velo},
		{"commute", s.commute},
		{"finanzen", s.finanzen},
		{"places", s.places},
		{"schiffe", s.schiffe},
		{"ausgaben", s.ausgaben},
	}
	f := &family{anna: anna}
	for _, st := range steps {
		if err := st.run(f); err != nil {
			return anna, fmt.Errorf("demo: seed %s: %w", st.name, err)
		}
	}
	return f.anna, nil
}

type family struct{ anna, ben, mia account.Account }

func (s *seeder) date(daysAgo int) string {
	return s.today.AddDate(0, 0, -daysAgo).Format(time.DateOnly)
}

// ── accounts, links ───────────────────────────────────

func (s *seeder) accounts(f *family) error {
	all := []account.Module{account.Velo, account.Arbeitswege, account.Orte, account.Seiten, account.Schiffe, account.Ausgaben}
	var err error
	if f.anna, err = s.Accounts.Update(s.ctx, f.anna, f.anna.ID, account.Changes{
		Email: AdminEmail, Name: "Anna", IsAdmin: true, Status: account.Active, Modules: f.anna.Modules,
	}); err != nil {
		return err
	}
	add := func(email, name string, mods []account.Module) (account.Account, error) {
		a, err := s.Accounts.Invite(s.ctx, f.anna, account.Changes{Email: email, Name: name, Modules: mods})
		if err != nil {
			return a, err
		}
		return s.Accounts.Update(s.ctx, f.anna, a.ID, account.Changes{Email: email, Name: name, Status: account.Active, Modules: mods})
	}
	if f.ben, err = add("ben@example.org", "Ben", all); err != nil {
		return err
	}
	if f.mia, err = add("mia@example.org", "Mia", []account.Module{account.Seiten, account.Schiffe}); err != nil {
		return err
	}
	_, err = s.Accounts.Invite(s.ctx, f.anna, account.Changes{Email: "oma@example.org", Name: "Oma", Modules: []account.Module{account.Seiten}})
	return err
}

func (s *seeder) links(*family) error {
	for _, l := range []links.Link{
		{Name: "Aufgaben", URL: "https://aufgaben.example.org", Icon: "check", Description: "Gemeinsame To-dos", Public: true},
		{Name: "Kalender", URL: "https://kalender.example.org", Icon: "cal", Description: "Familienkalender (CalDAV)", Public: true},
		{Name: "PDF-Werkzeuge", URL: "https://pdf.example.org", Icon: "pdf", Description: "Zusammenfügen, drehen, verkleinern"},
		{Name: "Rezepte", URL: "https://rezepte.example.org", Icon: "book", Description: "Unsere Lieblingsrezepte", Public: true},
		{Name: "NAS", URL: "https://nas.example.org", Icon: "server", Description: "Dateien und Backups"},
	} {
		if _, err := s.Links.Create(s.ctx, l); err != nil {
			return err
		}
	}
	return nil
}

// ── pages ─────────────────────────────────────────────

func (s *seeder) pages(f *family) error {
	shop, err := s.Pages.Create(s.ctx, f.anna, "", "Einkaufsliste", "# Wochenmarkt\n\n- [ ] Äpfel\n- [ ] Bergkäse\n- [x] Eier\n- [ ] Brot\n\n# Drogerie\n\n- [ ] Zahnpasta\n- [x] Sonnencreme\n", pages.Public)
	if err != nil {
		return err
	}
	if _, err := s.Pages.Save(s.ctx, f.ben, shop.Slug, shop.Version, shop.Title, shop.Body+"\n# Velo\n\n- [ ] Kettenöl\n"); err != nil {
		return err
	}
	home, err := s.Pages.Create(s.ctx, f.anna, "", "Haus und Technik", "Alles rund ums Haus. Die Unterseiten stehen unten.\n", pages.Public)
	if err != nil {
		return err
	}
	if _, err := s.Pages.Create(s.ctx, f.anna, home.ID, "Heimnetz", "Wie unser Netz aufgebaut ist.\n\n```mermaid\ngraph TD\n  I((Internet)) --> R[Router]\n  R --> W[WLAN]\n  R --> G[Gäste-WLAN]\n  R --> N[NAS]\n  N --> A[Allmend]\n  N --> K[Kalender]\n  W --> P[Laptops und Handys]\n  G --> T[Fernseher]\n```\n\n| Gerät | Netz |\n|---|---|\n| NAS | WLAN, fest |\n| Drucker | WLAN |\n| Fernseher | Gäste |\n", pages.Rights{ReadAll: true}); err != nil {
		return err
	}
	if _, err := s.Pages.Create(s.ctx, f.ben, home.ID, "Heizung", "Wartung jedes Jahr im September.\n\n- [ ] Termin vereinbaren\n- [ ] Filter kaufen\n", pages.Public); err != nil {
		return err
	}
	_, err = s.Pages.Create(s.ctx, f.anna, "", "Sommerferien", "Ideen für den Sommer:\n\n1. Velotour um den Bodensee\n2. Zelten am See\n3. **Ein** Regentag im Museum\n\n- [ ] Zelt prüfen\n- [ ] Fähre nachschauen\n",
		pages.Rights{Readers: []string{f.ben.ID, f.mia.ID}, Writers: []string{f.ben.ID}})
	return err
}

// ── velo ──────────────────────────────────────────────

var tourNames = []string{"Feierabendrunde", "Hügelrunde", "Seerunde", "Sonntagsausfahrt", "Gravel-Schlaufe", "Zum Bäcker", "Waldrunde", "Über den Pass"}

func (s *seeder) velo(f *family) error {
	gravel, err := s.Velo.SaveBike(s.ctx, f.anna, velo.Bike{Name: "Gravel", Brand: "Feldweg", Model: "GR 2", Acquired: s.date(900), PurchaseCents: 249000, AccessoriesCents: 18950, Color: "#5c7a3e"})
	if err != nil {
		return err
	}
	city, err := s.Velo.SaveBike(s.ctx, f.anna, velo.Bike{Name: "Stadtvelo", Brand: "Altstadt", Acquired: s.date(1600), PurchaseCents: 89000, Color: "#3e5c7a", Notes: "Mit Körbchen."})
	if err != nil {
		return err
	}
	// Tours on about 3 years of days: more in summer, mostly on the gravel bike.
	start := time.Date(s.today.Year()-2, 1, 1, 0, 0, 0, 0, time.UTC)
	for d := start; !d.After(s.today); d = d.AddDate(0, 0, 1) {
		season := 0.5 - 0.5*math.Cos(2*math.Pi*float64(d.YearDay())/365)
		if s.rnd.Float64() > 0.06+0.3*season {
			continue
		}
		t := velo.Tour{Date: d.Format(time.DateOnly), BikeID: gravel.ID}
		if s.rnd.IntN(4) == 0 {
			t.BikeID, t.DistanceKm, t.ElevationM = city.ID, round1(4+s.rnd.Float64()*12), s.rnd.IntN(80)
			t.Title = "Zum Bäcker"
		} else {
			t.DistanceKm = round1(20 + s.rnd.Float64()*60 + 30*season*s.rnd.Float64())
			t.ElevationM = int(t.DistanceKm * (6 + s.rnd.Float64()*12))
			t.Title = tourNames[s.rnd.IntN(len(tourNames))]
		}
		if _, err := s.Velo.SaveTour(s.ctx, f.anna, t); err != nil {
			return err
		}
	}
	odo := 4120.0
	for _, r := range []velo.Repair{
		{BikeID: gravel.ID, Date: s.date(640), CostCents: 4590, Title: "Kette und Kassette", OdometerKm: &odo},
		{BikeID: gravel.ID, Date: s.date(300), CostCents: 7900, Title: "Neue Reifen", Comment: "Etwas breiter, 45 mm."},
		{BikeID: gravel.ID, Date: s.date(40), CostCents: 12000, Title: "Service"},
		{BikeID: city.ID, Date: s.date(500), CostCents: 1890, Title: "Licht"},
	} {
		if _, err := s.Velo.SaveRepair(s.ctx, f.anna, r); err != nil {
			return err
		}
	}
	y := s.today.Year()
	for year, c := range map[int]string{y - 2: "Das erste Jahr mit dem **Gravel**.", y - 1: "Viel Regen im Frühling, dafür ein langer Herbst."} {
		if err := s.Velo.SetComment(s.ctx, f.anna, year, c); err != nil {
			return err
		}
	}
	for i := range 3 {
		m := s.today.AddDate(0, -i, 0)
		if err := s.Velo.SetGoal(s.ctx, f.anna, m.Year(), int(m.Month()), float64(200+50*i)); err != nil {
			return err
		}
	}
	mtb, err := s.Velo.SaveBike(s.ctx, f.ben, velo.Bike{Name: "Bergvelo", Color: "#7a3e5c"})
	if err != nil {
		return err
	}
	for i := 3; i < 400; i += 9 + s.rnd.IntN(9) {
		if _, err := s.Velo.SaveTour(s.ctx, f.ben, velo.Tour{Date: s.date(i), BikeID: mtb.ID, DistanceKm: round1(15 + s.rnd.Float64()*30), ElevationM: 300 + s.rnd.IntN(700), Title: "Trail"}); err != nil {
			return err
		}
	}
	return nil
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// ── commute ───────────────────────────────────────────

var workdays = [12]float64{21, 20, 22, 20, 20, 21, 23, 21, 22, 21, 20, 19}

func (s *seeder) commute(f *family) error {
	for _, p := range []struct {
		a              account.Account
		car, train, bk float64
		ho             float64 // share of home office
	}{{f.anna, 6, 32, 9, 0.4}, {f.ben, 18, 0, 0, 0.2}} {
		for year := s.today.Year() - 1; year <= s.today.Year(); year++ {
			y := commute.Year{Year: year, CarDistKm: p.car, TrainDistKm: p.train, BikeDistKm: p.bk}
			last := 12
			if year == s.today.Year() {
				last = int(s.today.Month()) - 1 // only completed months
			}
			for i := range last {
				m := commute.Month{Workdays: workdays[i]}
				if i == 7 {
					m.Holidays = 10
				}
				m.Krank = float64(s.rnd.IntN(2))
				left := m.Workdays - m.Holidays - m.Krank
				m.HomeOffice = math.Round(left * p.ho)
				if p.bk > 0 && i >= 3 && i <= 8 {
					m.Bike = float64(2 + s.rnd.IntN(4))
				}
				m.CarTrain = left - m.HomeOffice - m.Bike
				y.Months[i] = m
			}
			if _, err := s.Commute.Create(s.ctx, p.a, y); err != nil {
				return err
			}
		}
	}
	return nil
}

// ── places ────────────────────────────────────────────

// A three-day ride around Lake Constance, one GPS point every few minutes.
var lakeRoute = [][3]float64{ // lat, lon, day
	{47.6603, 9.1758, 0}, {47.6950, 9.2700, 0}, {47.7590, 9.3750, 0}, {47.6560, 9.4790, 0},
	{47.6560, 9.4790, 1}, {47.5970, 9.5900, 1}, {47.5440, 9.6850, 1}, {47.5050, 9.7420, 1},
	{47.5050, 9.7420, 2}, {47.4900, 9.5600, 2}, {47.5600, 9.3800, 2}, {47.6580, 9.1750, 2},
}

// Workdays around Basel: home, office, back.
var basel = [][2]float64{{47.5480, 7.5880}, {47.5560, 7.5930}, {47.5600, 7.6000}, {47.5640, 7.6080}}

// places seeds a trip from a KML file and the location history from a
// Timeline.json, both through the importers.
func (s *seeder) places(f *family) error {
	first := s.today.AddDate(0, 0, -60)
	var kml strings.Builder
	kml.WriteString(`<?xml version="1.0" encoding="UTF-8"?><kml xmlns="http://www.opengis.net/kml/2.2"><Document><name>Bodensee-Runde</name>` +
		`<description>Drei Tage mit dem Velo einmal um den See.</description>`)
	for _, p := range []struct {
		name     string
		lat, lon float64
	}{{"Konstanz", 47.6603, 9.1758}, {"Meersburg", 47.6950, 9.2700}, {"Friedrichshafen", 47.6560, 9.4790}, {"Bregenz", 47.5050, 9.7420}, {"Arbon", 47.5160, 9.4330}} {
		fmt.Fprintf(&kml, `<Placemark><name>%s</name><Point><coordinates>%.4f,%.4f</coordinates></Point></Placemark>`, p.name, p.lon, p.lat)
	}
	kml.WriteString(`<Placemark><name>Route</name><LineString><coordinates>`)
	for _, p := range lakeRoute {
		fmt.Fprintf(&kml, "%.4f,%.4f ", p[1], p[0])
	}
	kml.WriteString(`</coordinates></LineString></Placemark></Document></kml>`)
	trip := places.TripForm{DateFrom: first.Format(time.DateOnly), DateTo: first.AddDate(0, 0, 2).Format(time.DateOnly)}
	if _, err := s.Places.ImportTrip(s.ctx, f.anna, "bodensee.kml", []byte(kml.String()), trip); err != nil {
		return err
	}
	ben := `<kml><Document><name>Toskana</name><Placemark><name>Siena</name><Point><coordinates>11.3308,43.3188</coordinates></Point></Placemark>` +
		`<Placemark><name>Florenz</name><Point><coordinates>11.2558,43.7696</coordinates></Point></Placemark></Document></kml>`
	if _, err := s.Places.ImportTrip(s.ctx, f.ben, "toskana.kml", []byte(ben), places.TripForm{DateFrom: s.date(120), DateTo: s.date(110)}); err != nil {
		return err
	}

	var segs []string
	at := func(t time.Time) string { return t.In(s.loc).Format("2006-01-02T15:04:05.000-07:00") }
	ll := func(lat, lon float64) string { return fmt.Sprintf("%.6f°, %.6f°", lat, lon) }
	path := func(pts [][2]float64, start time.Time, step time.Duration) {
		var b strings.Builder
		for k, p := range pts {
			if k > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"point":%q,"time":%q}`, ll(p[0], p[1]), at(start.Add(time.Duration(k)*step)))
		}
		end := start.Add(time.Duration(len(pts)-1) * step)
		segs = append(segs, fmt.Sprintf(`{"startTime":%q,"endTime":%q,"timelinePath":[%s]}`, at(start), at(end), b.String()))
	}
	stay := func(kind string, lat, lon float64, from, to time.Time) {
		segs = append(segs, fmt.Sprintf(`{"startTime":%q,"endTime":%q,"visit":{"hierarchyLevel":0,"probability":0.9,"topCandidate":{"placeId":"demo","semanticType":%q,"probability":0.8,"placeLocation":{"latLng":%q}}}}`,
			at(from), at(to), kind, ll(lat, lon)))
	}
	local := func(day time.Time, h, m int) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, s.loc)
	}
	for d := range 3 {
		day := first.AddDate(0, 0, d)
		var pts [][2]float64
		for i := 0; i+1 < len(lakeRoute); i++ {
			a, b := lakeRoute[i], lakeRoute[i+1]
			if int(a[2]) != d || a[2] != b[2] {
				continue
			}
			for k := range 20 {
				t := float64(k) / 20
				pts = append(pts, [2]float64{a[0] + (b[0]-a[0])*t + (s.rnd.Float64()-0.5)*0.002, a[1] + (b[1]-a[1])*t + (s.rnd.Float64()-0.5)*0.002})
			}
		}
		path(pts, local(day, 9, 0), 3*time.Minute)
		end := lakeRoute[4*d+3]
		stay("UNKNOWN", end[0], end[1], local(day, 18, 0), local(day.AddDate(0, 0, 1), 8, 30))
	}
	for d := 14; d >= 1; d-- {
		day := s.today.AddDate(0, 0, -d)
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		stay("HOME", basel[0][0], basel[0][1], local(day, 0, 0), local(day, 7, 30))
		path(basel, local(day, 7, 35), 5*time.Minute)
		stay("WORK", basel[3][0], basel[3][1], local(day, 7, 55), local(day, 17, 0))
		back := slices.Clone(basel)
		slices.Reverse(back)
		path(back, local(day, 17, 10), 6*time.Minute)
	}
	tl := `{"semanticSegments":[` + strings.Join(segs, ",") + `],"rawSignals":[],"userLocationProfile":{}}`
	_, err := s.Places.ImportTimeline(s.ctx, f.anna, "Timeline.json", strings.NewReader(tl))
	return err
}

// ── schiffe ───────────────────────────────────────────

func (s *seeder) schiffe(f *family) error {
	// A finished classic game, a salvo game and a sea-mines game in the middle.
	if err := s.game(f.mia, f.anna, schiffe.Classic, -1); err != nil {
		return err
	}
	if err := s.game(f.anna, f.ben, schiffe.Salvo, 6); err != nil {
		return err
	}
	return s.game(f.ben, f.anna, schiffe.Mines, 14)
}

// game plays turns (−1: until the end) with random but legal shots.
func (s *seeder) game(a, b account.Account, v schiffe.Variant, turns int) error {
	g, err := s.Schiffe.Challenge(s.ctx, a, b.ID, v, false, true)
	if err != nil {
		return err
	}
	n := v.Size()
	for _, p := range []account.Account{a, b} {
		ships, mines := schiffe.RandomSetup(s.rnd, n, v.Mines(), false)
		if g, err = s.Schiffe.Place(s.ctx, p, g.ID, ships, mines); err != nil {
			return err
		}
	}
	players := map[string]account.Account{a.ID: a, b.ID: b}
	for turn := 0; g.Status == schiffe.Playing && turn != turns; turn++ {
		p := players[g.Turn]
		target := g.Opponent(p.ID)
		blocked := g.Blocked(target)
		for _, sh := range g.ShotsAt(target) {
			blocked[[2]int{sh.X, sh.Y}] = true
		}
		for _, sh := range g.SunkShips(target) {
			for _, c := range sh.Cells() {
				blocked[c] = true // also ships lost on a mine
			}
		}
		want := min(g.ShotsPerTurn(p.ID), n*n-len(blocked))
		var cells [][2]int
		for len(cells) < want {
			c := [2]int{s.rnd.IntN(n), s.rnd.IntN(n)}
			if !blocked[c] {
				blocked[c] = true
				cells = append(cells, c)
			}
		}
		ship := schiffe.NoShip
		if v.Mines() > 0 {
			for i := range g.FleetOf(p.ID) {
				if g.Afloat(p.ID, i) {
					ship = i // the largest ship afloat fires
					break
				}
			}
		}
		if g, _, err = s.Schiffe.Fire(s.ctx, p, g.ID, cells, ship); err != nil {
			return err
		}
	}
	return nil
}

// ── ausgaben ──────────────────────────────────────────

func (s *seeder) ausgaben(f *family) error {
	g, err := s.Ausgaben.CreateGroup(s.ctx, f.anna, "Haushalt", []string{f.ben.ID})
	if err != nil {
		return err
	}
	both := g.Members
	// Monthly rent and electricity, started a few months ago: opening the
	// group books the due months.
	first := s.today.AddDate(0, -4, 0)
	for _, r := range []struct {
		desc, cat string
		amount    int64
		payer     account.Account
		day       int
	}{
		{"Miete", "rent", 1_480_00, f.anna, 1},
		{"Strom", "electricity", 86_00, f.ben, 28},
	} {
		next := ausgaben.Clamp(first.Format("2006-01"), r.day)
		if _, err := s.Ausgaben.SaveRecurring(s.ctx, r.payer, ausgaben.Recurring{GroupID: g.ID, Description: r.desc, Category: r.cat,
			Amount: r.amount, Currency: "EUR", PaidBy: r.payer.ID, Shares: ausgaben.Equal(g, r.amount, r.payer.ID, both), Day: r.day, Next: next}); err != nil {
			return err
		}
	}
	shops := []struct{ desc, cat string }{{"Wochenmarkt", "groceries"}, {"Grossverteiler", "groceries"}, {"Drogerie", "household_supplies"}, {"Baumarkt", "maintenance"}, {"Tierarzt", "pets"}, {"Pizzeria", "dining_out"}, {"Kino", "movies"}, {"Tankstelle", "gas_fuel"}}
	for i := 0; i < 24; i++ {
		sh := shops[s.rnd.IntN(len(shops))]
		payer := f.anna
		if s.rnd.IntN(2) == 0 {
			payer = f.ben
		}
		amount := int64(800 + s.rnd.IntN(12000))
		if _, err := s.Ausgaben.SaveEntry(s.ctx, payer, ausgaben.Entry{GroupID: g.ID, Kind: ausgaben.Expense, Date: s.date(4 + i*5), Description: sh.desc,
			Category: sh.cat, Amount: amount, Currency: "EUR", PaidBy: payer.ID, Shares: ausgaben.Equal(g, amount, payer.ID, both)}); err != nil {
			return err
		}
	}
	// A ski weekend in Switzerland, paid by Ben, Anna's share is bigger.
	if _, err := s.Ausgaben.SaveEntry(s.ctx, f.ben, ausgaben.Entry{GroupID: g.ID, Kind: ausgaben.Expense, Date: s.date(40), Description: "Skipass",
		Category: "sports", Amount: 196_00, Currency: "CHF", PaidBy: f.ben.ID, Shares: []ausgaben.Share{{AccountID: f.anna.ID, Amount: 112_00}, {AccountID: f.ben.ID, Amount: 84_00}}}); err != nil {
		return err
	}
	// Six weeks ago the EUR balance was settled: the group page shows the
	// entries since then, older ones are in the archive.
	v, err := s.Ausgaben.Open(s.ctx, f.anna, g.ID)
	if err != nil {
		return err
	}
	// The payment's day must have no other entry, or the order within the
	// day would decide whether it settles.
	used := map[string]bool{}
	for _, e := range v.Entries {
		used[e.Date] = true
	}
	ago := 42
	for used[s.date(ago)] {
		ago++
	}
	cut := s.date(ago)
	var before []ausgaben.Entry
	for _, e := range v.Entries {
		if e.Currency == "EUR" && e.Date < cut {
			before = append(before, e)
		}
	}
	for _, b := range ausgaben.Balances(g, before) {
		for _, t := range ausgaben.Settle(b) {
			payer := f.anna
			if t.From == f.ben.ID {
				payer = f.ben
			}
			if _, err := s.Ausgaben.SaveEntry(s.ctx, payer, ausgaben.Entry{GroupID: g.ID, Kind: ausgaben.Payment, Date: cut, Description: "Ausgleich",
				Amount: t.Amount, Currency: t.Currency, PaidBy: t.From, Shares: []ausgaben.Share{{AccountID: t.To, Amount: t.Amount}}}); err != nil {
				return err
			}
		}
	}
	return nil
}
