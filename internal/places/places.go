// Package places holds an account's places, trips (lists of places) and GPS
// visits imported from Google Location History ("Orte"). The owner writes;
// everyone with module access may read. A fundamental rework is planned (#6).
package places

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/tiliavir/allmend/internal/id"
)

// Place is a named point.
type Place struct {
	ID, OwnerID         string
	Name, Description   string
	Latitude, Longitude float64
}

// List is a trip with its places.
type List struct {
	ID, OwnerID       string
	Name, Description string
	DateFrom, DateTo  string // YYYY-MM-DD or ""
	SourceFile        string // GPS visits of this file belong to the trip
	PlaceCount        int
}

// Visit is one GPS point. A point of a track drawn without times (old KMZ
// lines) has no Timestamp (zero) but its position Seq in the track.
type Visit struct {
	ID, OwnerID         string
	PlaceID             string
	Latitude, Longitude float64
	Timestamp           time.Time
	Seq                 int // only without Timestamp
	SourceFile          string
}

// ErrNotFound means no such list.
var ErrNotFound = errors.New("places: not found")

// Repository stores places data, always for one owner.
type Repository interface {
	Lists(ctx context.Context, owner string) ([]List, error)
	List(ctx context.Context, owner, id string) (List, []Place, error)
	VisitsBySource(ctx context.Context, owner, source string) ([]Visit, error)
	VisitDays(ctx context.Context, owner string) ([]string, error) // YYYY-MM-DD (UTC), ascending
	VisitsOn(ctx context.Context, owner string, from, to time.Time) ([]Visit, error)
	// InsertVisits adds timed visits, skipping ones that exist (same source and time); returns the number added.
	// A visit without a time is an error (untimed points come only from the legacy import).
	InsertVisits(ctx context.Context, vs []Visit) (int, error)
}

// ParseGoogleLocation reads a Google Takeout location history file
// ({"locations":[{"latitudeE7":…,"longitudeE7":…,"timestamp":"…"}]}).
// Any location without a valid timestamp or coordinates is an error.
func ParseGoogleLocation(r io.Reader, owner, source string) ([]Visit, error) {
	var doc struct {
		Locations []struct {
			LatitudeE7  *int64  `json:"latitudeE7"`
			LongitudeE7 *int64  `json:"longitudeE7"`
			Timestamp   *string `json:"timestamp"`
		} `json:"locations"`
	}
	dec := json.NewDecoder(r)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("Datei ist kein gültiges Google-Location-JSON: %w", err)
	}
	if doc.Locations == nil {
		return nil, errors.New(`Datei enthält kein Feld "locations"`)
	}
	out := make([]Visit, 0, len(doc.Locations))
	for i, l := range doc.Locations {
		if l.LatitudeE7 == nil || l.LongitudeE7 == nil || l.Timestamp == nil {
			return nil, fmt.Errorf("Eintrag %d: Koordinaten oder Zeitstempel fehlen", i+1)
		}
		ts, err := time.Parse(time.RFC3339Nano, *l.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("Eintrag %d: Zeitstempel %q ungültig", i+1, *l.Timestamp)
		}
		lat, lon := float64(*l.LatitudeE7)/1e7, float64(*l.LongitudeE7)/1e7
		if math.Abs(lat) > 90 || math.Abs(lon) > 180 {
			return nil, fmt.Errorf("Eintrag %d: Koordinaten außerhalb der Erde", i+1)
		}
		out = append(out, Visit{ID: id.New(), OwnerID: owner, Latitude: lat, Longitude: lon, Timestamp: ts.UTC(), SourceFile: source})
	}
	return out, nil
}
