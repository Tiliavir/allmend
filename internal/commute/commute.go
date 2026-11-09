// Package commute tracks work commutes per account and year ("Arbeitswege"):
// workdays per month by kind, derived kilometres and the German
// Entfernungspauschale (Pendlerpauschale).
package commute

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/id"
)

// Month is one month of a year. Days may be halves.
type Month struct {
	Workdays, Holidays, Kurs, Kompensation, Unfall, Krank float64
	Bike, CarTrain, HomeOffice                            float64
}

// Year is one account's commute record for a calendar year.
type Year struct {
	ID      string
	OwnerID string
	Year    int
	Version int

	CarDistKm, TrainDistKm, BikeDistKm float64 // one-way distances
	// Pendlerpauschale override; nil = statutory value for the year.
	RateLowCents, RateHighCents, ThresholdKm *float64

	Months    [12]Month
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Errors.
var (
	ErrNotFound  = errors.New("commute year not found")
	ErrExists    = errors.New("commute year exists")
	ErrConflict  = errors.New("commute year changed meanwhile")
	ErrForbidden = errors.New("not your commute year")
)

// StatutoryRates returns the Entfernungspauschale in euro cents per km of
// one-way distance and commute day: low applies up to the threshold, high
// beyond it.
//
//	≤2020 30/30 · 2021 30/35 · 2022–2025 30/38 · 2026+ 38/38 (tier abolished)
func StatutoryRates(year int) (low, high float64) {
	switch {
	case year >= 2026:
		return 38, 38
	case year >= 2022:
		return 30, 38
	case year == 2021:
		return 30, 35
	default:
		return 30, 30
	}
}

// StatutoryThresholdKm is where the higher rate starts.
const StatutoryThresholdKm = 20.0

// Rates returns the effective rates and threshold for y.
func (y Year) Rates() (low, high, threshold float64) {
	low, high = StatutoryRates(y.Year)
	threshold = StatutoryThresholdKm
	if y.RateLowCents != nil {
		low = *y.RateLowCents
	}
	if y.RateHighCents != nil {
		high = *y.RateHighCents
	}
	if y.ThresholdKm != nil {
		threshold = *y.ThresholdKm
	}
	return low, high, threshold
}

// Derived are the computed values of one month.
type Derived struct {
	Accounted              float64
	KmCar, KmTrain, KmBike float64 // there and back
	HomeOfficePct          float64
	Check                  bool // accounted days equal workdays
	PendlerCents           int64
}

// View is a year with everything derived.
type View struct {
	Year
	RateLow, RateHigh, Threshold    float64
	BaseCarTrainKm, BaseBikeKm      float64 // full one-way km
	Derived                         [12]Derived
	Totals                          Month
	TotalKmCar, TotalKmTrain        float64
	TotalKmBike, TotalHomeOfficePct float64
	TotalPendlerCents               int64
	AllChecksOK                     bool
}

// Compute derives all values. A car+train day counts the combined one-way
// distance, rounded down to full kilometres ("volle Kilometer").
func Compute(y Year) View {
	v := View{Year: y, AllChecksOK: true}
	v.RateLow, v.RateHigh, v.Threshold = y.Rates()
	v.BaseCarTrainKm = math.Floor(y.CarDistKm + y.TrainDistKm)
	v.BaseBikeKm = math.Floor(y.BikeDistKm)
	for i, m := range y.Months {
		acc := m.Holidays + m.Kurs + m.Kompensation + m.Unfall + m.Krank + m.Bike + m.CarTrain + m.HomeOffice
		d := Derived{
			Accounted: acc,
			KmCar:     m.CarTrain * y.CarDistKm * 2,
			KmTrain:   m.CarTrain * y.TrainDistKm * 2,
			KmBike:    m.Bike * y.BikeDistKm * 2,
			Check:     math.Abs(acc-m.Workdays) < 0.01,
			PendlerCents: pendlerCents(m.CarTrain, v.BaseCarTrainKm, v.RateLow, v.RateHigh, v.Threshold) +
				pendlerCents(m.Bike, v.BaseBikeKm, v.RateLow, v.RateHigh, v.Threshold),
		}
		if m.Workdays > 0 {
			d.HomeOfficePct = m.HomeOffice / m.Workdays
		}
		v.Derived[i] = d
		v.AllChecksOK = v.AllChecksOK && d.Check
		t := &v.Totals
		t.Workdays += m.Workdays
		t.Holidays += m.Holidays
		t.Kurs += m.Kurs
		t.Kompensation += m.Kompensation
		t.Unfall += m.Unfall
		t.Krank += m.Krank
		t.Bike += m.Bike
		t.CarTrain += m.CarTrain
		t.HomeOffice += m.HomeOffice
		v.TotalKmCar += d.KmCar
		v.TotalKmTrain += d.KmTrain
		v.TotalKmBike += d.KmBike
		v.TotalPendlerCents += d.PendlerCents
	}
	if v.Totals.Workdays > 0 {
		v.TotalHomeOfficePct = v.Totals.HomeOffice / v.Totals.Workdays
	}
	return v
}

func pendlerCents(days, oneWayKm, low, high, threshold float64) int64 {
	if days <= 0 || oneWayKm <= 0 {
		return 0
	}
	lowKm := math.Min(oneWayKm, threshold)
	highKm := math.Max(oneWayKm-threshold, 0)
	return int64(math.Round((lowKm*low + highKm*high) * days))
}

// Repository stores commute years.
type Repository interface {
	Years(ctx context.Context, ownerID string) ([]int, error) // newest first
	Get(ctx context.Context, ownerID string, year int) (Year, error)
	Latest(ctx context.Context, ownerID string) (Year, error)
	Insert(ctx context.Context, y Year) error // ErrExists if (owner, year) exists
	Update(ctx context.Context, y Year, want int) error
	Delete(ctx context.Context, ownerID string, year int) error
}

// Service implements commute use cases. Module access is checked by the caller.
// Everyone with access may read every owner's years; only the owner writes.
type Service struct {
	Repo Repository
	Now  func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Draft returns a new year for a, prefilled with the distances and rate
// overrides of a's latest year.
func (s *Service) Draft(ctx context.Context, a account.Account, year int) (Year, error) {
	y := Year{OwnerID: a.ID, Year: year}
	last, err := s.Repo.Latest(ctx, a.ID)
	if errors.Is(err, ErrNotFound) {
		return y, nil
	}
	if err != nil {
		return y, err
	}
	y.CarDistKm, y.TrainDistKm, y.BikeDistKm = last.CarDistKm, last.TrainDistKm, last.BikeDistKm
	y.ThresholdKm = last.ThresholdKm
	return y, nil
}

// Create stores a new year for a.
func (s *Service) Create(ctx context.Context, a account.Account, y Year) (Year, error) {
	now := s.now()
	y.ID, y.OwnerID, y.Version, y.CreatedAt, y.UpdatedAt = id.New(), a.ID, 1, now, now
	return y, s.Repo.Insert(ctx, y)
}

// Save updates a's year if it is still at version base.
func (s *Service) Save(ctx context.Context, a account.Account, y Year, base int) (Year, error) {
	old, err := s.Repo.Get(ctx, a.ID, y.Year)
	if err != nil {
		return y, err
	}
	if old.Version != base {
		return old, ErrConflict
	}
	y.ID, y.OwnerID, y.CreatedAt, y.Version, y.UpdatedAt = old.ID, a.ID, old.CreatedAt, base+1, s.now()
	return y, s.Repo.Update(ctx, y, base)
}

// Delete removes one of a's years.
func (s *Service) Delete(ctx context.Context, a account.Account, year int) error {
	return s.Repo.Delete(ctx, a.ID, year)
}
