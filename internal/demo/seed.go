// Package demo builds the public demo: a fictional family's data, entered
// through the domain services (so it passes the same validation as user
// input), and a static export of the rendered pages for GitHub Pages.
package demo

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/ausgaben"
	"github.com/tiliavir/allmend/internal/commute"
	"github.com/tiliavir/allmend/internal/finanzen"
	"github.com/tiliavir/allmend/internal/id"
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
	Places   places.Repository
}

type seeder struct {
	Deps
	ctx   context.Context
	rnd   *rand.Rand
	today time.Time // local date, midnight
}

// Seed fills an empty database with the demo family. The admin must exist
// (Accounts.EnsureAdmin with AdminEmail). Dates are relative to now, so the
// current year always has data; the same seed gives the same data.
func Seed(ctx context.Context, d Deps, now time.Time, loc *time.Location) (account.Account, error) {
	y, m, day := now.In(loc).Date()
	s := &seeder{Deps: d, ctx: ctx, rnd: rand.New(rand.NewPCG(1866, 2026)), today: time.Date(y, m, day, 0, 0, 0, 0, time.UTC)} //nolint:gosec // demo data, not security
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
	shop, err := s.Pages.Create(s.ctx, f.anna, "", "Einkaufsliste", "# Wochenmarkt\n\n- [ ] Äpfel\n- [ ] Bergkäse\n- [x] Eier\n- [ ] Brot\n\n# Drogerie\n\n- [ ] Zahnpasta\n- [x] Sonnencreme\n")
	if err != nil {
		return err
	}
	if _, err := s.Pages.SetRights(s.ctx, f.anna, shop.Slug, pages.Rights{ReadAll: true, WriteAll: true}); err != nil {
		return err
	}
	if _, err := s.Pages.Save(s.ctx, f.ben, shop.Slug, shop.Version, shop.Title, shop.Body+"\n# Velo\n\n- [ ] Kettenöl\n"); err != nil {
		return err
	}
	home, err := s.Pages.Create(s.ctx, f.anna, "", "Haus und Technik", "Alles rund ums Haus. Die Unterseiten stehen unten.\n")
	if err != nil {
		return err
	}
	net, err := s.Pages.Create(s.ctx, f.anna, home.ID, "Heimnetz", "Wie unser Netz aufgebaut ist.\n\n```mermaid\ngraph TD\n  I((Internet)) --> R[Router]\n  R --> W[WLAN]\n  R --> G[Gäste-WLAN]\n  R --> N[NAS]\n  N --> A[Allmend]\n  N --> K[Kalender]\n  W --> P[Laptops und Handys]\n  G --> T[Fernseher]\n```\n\n| Gerät | Netz |\n|---|---|\n| NAS | WLAN, fest |\n| Drucker | WLAN |\n| Fernseher | Gäste |\n")
	if err != nil {
		return err
	}
	if _, err := s.Pages.SetRights(s.ctx, f.anna, net.Slug, pages.Rights{ReadAll: true}); err != nil {
		return err
	}
	if _, err := s.Pages.Create(s.ctx, f.ben, home.ID, "Heizung", "Wartung jedes Jahr im September.\n\n- [ ] Termin vereinbaren\n- [ ] Filter kaufen\n"); err != nil {
		return err
	}
	trip, err := s.Pages.Create(s.ctx, f.anna, "", "Sommerferien", "Ideen für den Sommer:\n\n1. Velotour um den Bodensee\n2. Zelten am See\n3. **Ein** Regentag im Museum\n\n- [ ] Zelt prüfen\n- [ ] Fähre nachschauen\n")
	if err != nil {
		return err
	}
	_, err = s.Pages.SetRights(s.ctx, f.anna, trip.Slug, pages.Rights{Readers: []string{f.ben.ID, f.mia.ID}, Writers: []string{f.ben.ID}})
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

func (s *seeder) places(f *family) error {
	first := s.today.AddDate(0, 0, -60)
	var vs []places.Visit
	for i := 0; i+1 < len(lakeRoute); i++ {
		a, b := lakeRoute[i], lakeRoute[i+1]
		if a[2] != b[2] {
			continue
		}
		day := first.AddDate(0, 0, int(a[2]))
		for k := range 20 {
			t := float64(k) / 20
			at := day.Add(time.Duration(9*60+(i%4)*60+k*3) * time.Minute)
			vs = append(vs, places.Visit{
				ID: id.New(), OwnerID: f.anna.ID, SourceFile: "demo.json", Timestamp: at,
				Latitude:  a[0] + (b[0]-a[0])*t + (s.rnd.Float64()-0.5)*0.002,
				Longitude: a[1] + (b[1]-a[1])*t + (s.rnd.Float64()-0.5)*0.002,
			})
		}
	}
	_, err := s.Places.InsertVisits(s.ctx, vs)
	return err
}

// ── schiffe ───────────────────────────────────────────

func (s *seeder) schiffe(f *family) error {
	// A finished classic game and a salvo game in the middle.
	if err := s.game(f.mia, f.anna, schiffe.Classic, -1); err != nil {
		return err
	}
	return s.game(f.anna, f.ben, schiffe.Salvo, 6)
}

// game plays turns (−1: until the end) with random but legal shots.
func (s *seeder) game(a, b account.Account, v schiffe.Variant, turns int) error {
	g, err := s.Schiffe.Challenge(s.ctx, a, b.ID, v, false, true)
	if err != nil {
		return err
	}
	n := v.Size()
	for _, p := range []account.Account{a, b} {
		if g, err = s.Schiffe.Place(s.ctx, p, g.ID, schiffe.RandomFleet(s.rnd, n, false)); err != nil {
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
		want := min(g.ShotsPerTurn(p.ID), n*n-len(blocked))
		var cells [][2]int
		for len(cells) < want {
			c := [2]int{s.rnd.IntN(n), s.rnd.IntN(n)}
			if !blocked[c] {
				blocked[c] = true
				cells = append(cells, c)
			}
		}
		if g, _, err = s.Schiffe.Fire(s.ctx, p, g.ID, cells); err != nil {
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
		{"Miete", "Wohnen", 1_480_00, f.anna, 1},
		{"Strom", "Energie", 86_00, f.ben, 28},
	} {
		next := ausgaben.Clamp(first.Format("2006-01"), r.day)
		if _, err := s.Ausgaben.SaveRecurring(s.ctx, r.payer, ausgaben.Recurring{GroupID: g.ID, Description: r.desc, Category: r.cat,
			Amount: r.amount, Currency: "EUR", PaidBy: r.payer.ID, Shares: ausgaben.Equal(g, r.amount, r.payer.ID, both), Day: r.day, Next: next}); err != nil {
			return err
		}
	}
	shops := []struct{ desc, cat string }{{"Wochenmarkt", "Lebensmittel"}, {"Grossverteiler", "Lebensmittel"}, {"Drogerie", "Haushalt"}, {"Baumarkt", "Wohnen"}, {"Tierarzt", "Haustier"}}
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
		Category: "Freizeit", Amount: 196_00, Currency: "CHF", PaidBy: f.ben.ID, Shares: []ausgaben.Share{{AccountID: f.anna.ID, Amount: 112_00}, {AccountID: f.ben.ID, Amount: 84_00}}}); err != nil {
		return err
	}
	_, err = s.Ausgaben.SaveEntry(s.ctx, f.ben, ausgaben.Entry{GroupID: g.ID, Kind: ausgaben.Payment, Date: s.date(30), Description: "Ausgleich Oktober",
		Amount: 150_00, Currency: "EUR", PaidBy: f.ben.ID, Shares: []ausgaben.Share{{AccountID: f.anna.ID, Amount: 150_00}}})
	return err
}
