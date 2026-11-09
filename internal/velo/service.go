package velo

import (
	"context"
	"strconv"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/id"
)

// Service implements Velo use cases for the acting account (always the owner).
type Service struct {
	Repo Repository
	Now  func() time.Time
	Loc  *time.Location // for "today"
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Today is the local date.
func (s *Service) Today() time.Time {
	loc := s.Loc
	if loc == nil {
		loc = time.UTC
	}
	return s.now().In(loc)
}

// Overview builds the year page for a, optionally for one bike.
func (s *Service) Overview(ctx context.Context, a account.Account, year int, bikeID string) (Overview, error) {
	all, err := s.Repo.Tours(ctx, a.ID)
	if err != nil {
		return Overview{}, err
	}
	ts := Filter(all, bikeID)
	today := s.Today()
	until := ""
	if year == today.Year() {
		until = today.Format(time.DateOnly)
	}
	o := Overview{Year: year, Stats: Summarize(ts, year, "")}
	prevUntil := ""
	if until != "" {
		prevUntil = strconv.Itoa(year-1) + until[4:]
		if until[5:] == "02-29" {
			prevUntil = strconv.Itoa(year-1) + "-02-28"
		}
	}
	o.Previous = Summarize(ts, year-1, prevUntil)
	o.Years = YearRows(ts)
	for i := range o.Years {
		if o.Years[i].Comment, err = s.Repo.Comment(ctx, a.ID, o.Years[i].Year); err != nil {
			return o, err
		}
	}
	o.Records = Records(ts)
	o.Heat = Heatmap(ts, year)
	o.CumThis = Cumulative(ts, year, until)
	o.CumPrev = Cumulative(ts, year-1, "")
	if year == today.Year() {
		m := int(today.Month())
		o.ShowGoal, o.GoalMonth = true, m
		o.MonthKm = Summarize(all, year, "").Months[m-1] // goals count all bikes
		if o.Goal, err = s.Repo.Goal(ctx, a.ID, year, m); err != nil {
			return o, err
		}
	}
	return o, nil
}

// Years lists the years with tours, newest first (always includes this year).
func (s *Service) Years(ctx context.Context, a account.Account) ([]int, error) {
	ts, err := s.Repo.Tours(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	cur := s.Today().Year()
	out := []int{cur}
	for _, r := range YearRows(ts) {
		if r.Year != cur {
			out = append(out, r.Year)
		}
	}
	return out, nil
}

// ── bikes ─────────────────────────────────────────────

// SaveBike creates (ID empty) or updates a bike of a.
func (s *Service) SaveBike(ctx context.Context, a account.Account, b Bike) (Bike, error) {
	if err := CheckBike(b); err != nil {
		return b, err
	}
	b.OwnerID, b.UpdatedAt = a.ID, s.now()
	if b.ID == "" {
		b.ID, b.Version, b.CreatedAt = id.New(), 1, b.UpdatedAt
		return b, s.Repo.InsertBike(ctx, b)
	}
	old, err := s.Repo.Bike(ctx, a.ID, b.ID)
	if err != nil {
		return b, err
	}
	want := b.Version
	b.CreatedAt, b.Version = old.CreatedAt, want+1
	return b, s.Repo.UpdateBike(ctx, b, want)
}

// ── tours ─────────────────────────────────────────────

// SaveTour creates (ID empty) or updates a tour of a. The bike must be a's.
func (s *Service) SaveTour(ctx context.Context, a account.Account, t Tour) (Tour, error) {
	if err := CheckTour(t); err != nil {
		return t, err
	}
	if t.BikeID != "" {
		if _, err := s.Repo.Bike(ctx, a.ID, t.BikeID); err != nil {
			return t, err
		}
	}
	t.OwnerID, t.UpdatedAt = a.ID, s.now()
	if t.ID == "" {
		t.ID, t.Version, t.CreatedAt = id.New(), 1, t.UpdatedAt
		return t, s.Repo.InsertTour(ctx, t)
	}
	old, err := s.Repo.Tour(ctx, a.ID, t.ID)
	if err != nil {
		return t, err
	}
	want := t.Version
	t.CreatedAt, t.Version = old.CreatedAt, want+1
	return t, s.Repo.UpdateTour(ctx, t, want)
}

// ── repairs ───────────────────────────────────────────

// SaveRepair creates (ID empty) or updates a repair on one of a's bikes.
func (s *Service) SaveRepair(ctx context.Context, a account.Account, r Repair) (Repair, error) {
	if err := CheckRepair(r); err != nil {
		return r, err
	}
	if _, err := s.Repo.Bike(ctx, a.ID, r.BikeID); err != nil {
		return r, err
	}
	r.OwnerID, r.UpdatedAt = a.ID, s.now()
	if r.ID == "" {
		r.ID, r.Version, r.CreatedAt = id.New(), 1, r.UpdatedAt
		return r, s.Repo.InsertRepair(ctx, r)
	}
	old, err := s.Repo.Repair(ctx, a.ID, r.ID)
	if err != nil {
		return r, err
	}
	want := r.Version
	r.CreatedAt, r.Version = old.CreatedAt, want+1
	return r, s.Repo.UpdateRepair(ctx, r, want)
}

// ── comments, goals ───────────────────────────────────

// SetComment stores a's comment for year (Markdown; "" removes it).
func (s *Service) SetComment(ctx context.Context, a account.Account, year int, text string) error {
	if len(text) > 20000 || year < 1900 || year > 2200 {
		return (&Problems{"Kommentar zu lang oder Jahr ungültig"}).Err()
	}
	return s.Repo.SetComment(ctx, a.ID, year, text, s.now())
}

// SetGoal stores a's monthly target in km (0 removes it).
func (s *Service) SetGoal(ctx context.Context, a account.Account, year, month int, km float64) error {
	if month < 1 || month > 12 || year < 1900 || year > 2200 || km < 0 || km > 100000 {
		return (&Problems{"Ziel ungültig"}).Err()
	}
	return s.Repo.SetGoal(ctx, a.ID, year, month, km, s.now())
}
