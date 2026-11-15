// Package places is the Orte module (ADR-20261115-orte): trips imported from
// KML/KMZ, the location history imported from Google's Timeline.json, and a
// heatmap of both. Trips are readable by everyone with access; the location
// history belongs to its owner alone.
package places

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Place is a named point of a trip.
type Place struct {
	ID, OwnerID         string
	Name, Description   string
	Latitude, Longitude float64
}

// List is a trip.
type List struct {
	ID, OwnerID       string
	Name, Description string
	DateFrom, DateTo  string // YYYY-MM-DD; old trips may lack one or both
	SourceFile        string // name of the imported file, may be ""
	SHA256            string // of the imported file; "" for old trips
	PlaceCount        int
	TrackCount        int
}

// MissingRange reports an old trip whose range is empty or incomplete.
func (l List) MissingRange() bool { return l.DateFrom == "" || l.DateTo == "" }

// TrackPoint is one point of a trip's track; At is zero for lines drawn
// without times.
type TrackPoint struct {
	At                  time.Time
	Latitude, Longitude float64
}

// Point is one point of the location history.
type Point struct {
	At                  time.Time
	Offset              *int   // UTC offset in minutes at the source; nil for old data
	Day                 string // local day, YYYY-MM-DD
	Latitude, Longitude float64
}

// Stay is a stay from Timeline.json. The file names no place, only a kind.
type Stay struct {
	Start, End          time.Time
	Offset              int // UTC offset in minutes at the start
	Day, EndDay         string
	Latitude, Longitude float64
	SemanticType        string // Google's semanticType, e.g. HOME, UNKNOWN
}

// Label is the German name of a stay's kind, "" for an unnamed one.
func (s Stay) Label() string {
	switch s.SemanticType {
	case "HOME", "INFERRED_HOME":
		return "Zuhause"
	case "WORK", "INFERRED_WORK":
		return "Arbeit"
	}
	return ""
}

// Import is one location history import.
type Import struct {
	ID, OwnerID string
	Kind        string // "timeline" or "records" (old Records.json)
	FileName    string
	At          time.Time
	Points      int
	Stays       int
	Skipped     int
}

// Cell is one heatmap cell: its centre and the number of points in it.
type Cell struct {
	Latitude, Longitude float64
	N                   int
}

// Box is a map view in degrees.
type Box struct{ South, West, North, East float64 }

// Errors.
var (
	ErrNotFound  = errors.New("places: not found")
	ErrDuplicate = errors.New("places: file already imported")
)

// Repository stores Orte data, always for one owner.
type Repository interface {
	Lists(ctx context.Context, owner string) ([]List, error) // newest range first, old trips last
	List(ctx context.Context, owner, id string) (List, []Place, error)
	Track(ctx context.Context, owner, listID string) ([]TrackPoint, error)
	// InsertTrip stores a trip with its places and track in one
	// transaction; ErrDuplicate if the owner imported the same file before.
	InsertTrip(ctx context.Context, l List, ps []Place, track []TrackPoint) error
	UpdateTrip(ctx context.Context, l List) error // name, description, range
	// DeleteTrip removes the trip, its track and the places of no other trip.
	DeleteTrip(ctx context.Context, owner, id string) error

	// Mark is the newest time in the owner's location history (points and
	// stay starts); ok is false if there is none.
	Mark(ctx context.Context, owner string) (mark time.Time, ok bool, err error)
	// InsertImport stores an import with its points and stays in one transaction.
	InsertImport(ctx context.Context, imp Import, pts []Point, stays []Stay) error
	Imports(ctx context.Context, owner string) ([]Import, error) // newest first
	DeleteImport(ctx context.Context, owner, id string) error    // with its points and stays

	Days(ctx context.Context, owner string) ([]string, error) // days with points or stays, ascending
	PointsOn(ctx context.Context, owner, day string) ([]Point, error)
	PointsBetween(ctx context.Context, owner, fromDay, toDay string) ([]Point, error)
	StaysOn(ctx context.Context, owner, day string) ([]Stay, error)
	// Heat counts the owner's points, track points and places per cell of
	// size deg inside b.
	Heat(ctx context.Context, owner string, deg float64, b Box) ([]Cell, error)
	// Bounds is the box around everything Heat counts; ok false if empty.
	Bounds(ctx context.Context, owner string) (b Box, ok bool, err error)
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

func (e *InvalidError) Error() string { return "places: invalid: " + strings.Join(e.Problems, "; ") }

// ValidDate reports whether s is a calendar date "YYYY-MM-DD".
func ValidDate(s string) bool {
	if len(s) != 10 {
		return false
	}
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

// CheckTrip validates a trip's own fields; a full range is required.
func CheckTrip(l List) error {
	var p Problems
	p.add(strings.TrimSpace(l.Name) == "" || len([]rune(l.Name)) > 120, "Name fehlt oder ist zu lang")
	p.add(len(l.Description) > 20000, "Beschreibung ist zu lang")
	switch {
	case l.DateFrom == "" || l.DateTo == "":
		p.add(true, "Zeitraum fehlt (Von und Bis)")
	case !ValidDate(l.DateFrom) || !ValidDate(l.DateTo):
		p.add(true, "Von und Bis müssen gültige Daten sein")
	default:
		p.add(l.DateTo < l.DateFrom, "Bis liegt vor Von")
	}
	return p.Err()
}

func validLatLon(lat, lon float64) bool {
	return lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}
