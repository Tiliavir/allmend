// Package velo tracks an account's bikes, tours, repairs, yearly comments and
// monthly goals (formerly "Rad"/Randdistanz). All data is private to its owner.
package velo

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Bike is one bicycle.
type Bike struct {
	ID, OwnerID          string
	Name, Brand, Model   string
	Acquired             string // YYYY-MM-DD or ""
	PurchaseCents        int64  // base currency, minor units
	AccessoriesCents     int64
	Notes                string
	Color                string // "#rrggbb" or ""
	Version              int
	CreatedAt, UpdatedAt time.Time
}

// Tour is one ride.
type Tour struct {
	ID, OwnerID          string
	BikeID               string // "" = no bike
	Date                 string // YYYY-MM-DD
	DistanceKm           float64
	ElevationM           int
	Title, Description   string
	Version              int
	CreatedAt, UpdatedAt time.Time
}

// Repair is a cost on a bike.
type Repair struct {
	ID, OwnerID, BikeID  string
	Date                 string
	CostCents            int64 // base currency, minor units
	Title, Comment       string
	OdometerKm           *float64
	Version              int
	CreatedAt, UpdatedAt time.Time
}

// Errors.
var (
	ErrNotFound = errors.New("velo: not found")
	ErrConflict = errors.New("velo: changed meanwhile")
	ErrInUse    = errors.New("velo: bike has tours or repairs")
)

// Repository stores Velo data. Every method is scoped to one owner.
type Repository interface {
	Bikes(ctx context.Context, owner string) ([]Bike, error) // by name
	Bike(ctx context.Context, owner, id string) (Bike, error)
	InsertBike(ctx context.Context, b Bike) error
	UpdateBike(ctx context.Context, b Bike, want int) error
	DeleteBike(ctx context.Context, owner, id string) error // ErrInUse if referenced

	Tours(ctx context.Context, owner string) ([]Tour, error) // by date, newest first
	Tour(ctx context.Context, owner, id string) (Tour, error)
	InsertTour(ctx context.Context, t Tour) error
	UpdateTour(ctx context.Context, t Tour, want int) error
	DeleteTour(ctx context.Context, owner, id string) error

	Repairs(ctx context.Context, owner, bikeID string) ([]Repair, error) // newest first
	Repair(ctx context.Context, owner, id string) (Repair, error)
	InsertRepair(ctx context.Context, r Repair) error
	UpdateRepair(ctx context.Context, r Repair, want int) error
	DeleteRepair(ctx context.Context, owner, id string) error

	Comment(ctx context.Context, owner string, year int) (string, error) // "" if none
	SetComment(ctx context.Context, owner string, year int, text string, at time.Time) error
	Goal(ctx context.Context, owner string, year, month int) (float64, error) // 0 if none
	SetGoal(ctx context.Context, owner string, year, month int, km float64, at time.Time) error
}

var (
	dateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	colorRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)
)

// ValidDate reports whether s is a real calendar date "YYYY-MM-DD".
func ValidDate(s string) bool {
	if !dateRe.MatchString(s) {
		return false
	}
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

// Problems lists what is wrong with user input (German).
type Problems []string

func (p *Problems) add(cond bool, msg string) {
	if cond {
		*p = append(*p, msg)
	}
}

// Err returns nil or an error carrying the problems.
func (p Problems) Err() error {
	if len(p) == 0 {
		return nil
	}
	return &InvalidError{p}
}

// InvalidError lists input problems.
type InvalidError struct{ Problems Problems }

func (e *InvalidError) Error() string { return "velo: invalid: " + strings.Join(e.Problems, "; ") }

// CheckBike validates a bike.
func CheckBike(b Bike) error {
	var p Problems
	p.add(strings.TrimSpace(b.Name) == "" || len([]rune(b.Name)) > 60, "Name fehlt oder ist zu lang")
	p.add(b.Acquired != "" && !ValidDate(b.Acquired), "Kaufdatum ist kein gültiges Datum")
	p.add(b.PurchaseCents < 0 || b.AccessoriesCents < 0, "Preise dürfen nicht negativ sein")
	p.add(b.Color != "" && !colorRe.MatchString(b.Color), "Farbe ungültig")
	p.add(len(b.Brand) > 60 || len(b.Model) > 60 || len(b.Notes) > 5000, "Ein Feld ist zu lang")
	return p.Err()
}

// CheckTour validates a tour.
func CheckTour(t Tour) error {
	var p Problems
	p.add(!ValidDate(t.Date), "Datum fehlt oder ist ungültig")
	p.add(t.DistanceKm <= 0 || t.DistanceKm > 2000, "Distanz muss zwischen 0 und 2000 km liegen")
	p.add(t.ElevationM < 0 || t.ElevationM > 50000, "Höhenmeter ungültig")
	p.add(len([]rune(t.Title)) > 120 || len(t.Description) > 20000, "Titel oder Beschreibung zu lang")
	return p.Err()
}

// CheckRepair validates a repair.
func CheckRepair(r Repair) error {
	var p Problems
	p.add(!ValidDate(r.Date), "Datum fehlt oder ist ungültig")
	p.add(strings.TrimSpace(r.Title) == "" || len([]rune(r.Title)) > 120, "Titel fehlt oder ist zu lang")
	p.add(r.CostCents < 0, "Kosten dürfen nicht negativ sein")
	p.add(r.OdometerKm != nil && (*r.OdometerKm < 0 || *r.OdometerKm > 10_000_000), "Kilometerstand ungültig")
	p.add(len(r.Comment) > 5000, "Kommentar zu lang")
	return p.Err()
}
