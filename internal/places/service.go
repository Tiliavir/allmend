package places

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"path"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/id"
)

// Service implements the Orte use cases.
type Service struct {
	Repo Repository
	Now  func() time.Time
	Loc  *time.Location // Europe/Zurich: dates without a zone
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) loc() *time.Location {
	if s.Loc != nil {
		return s.Loc
	}
	return time.UTC
}

// ── trips ─────────────────────────────────────────────

// TripForm holds what the import form may set; empty fields come from the file.
type TripForm struct {
	Name, Description, DateFrom, DateTo string
}

// ImportTrip stores a trip from a KML or KMZ file of a. Form fields win over
// the file; a trip without a range is refused, as is the same file twice.
func (s *Service) ImportTrip(ctx context.Context, a account.Account, fileName string, data []byte, f TripForm) (List, error) {
	fileName = path.Base(strings.ReplaceAll(fileName, `\`, "/"))
	var t KMLTrip
	var err error
	switch strings.ToLower(path.Ext(fileName)) {
	case ".kmz":
		t, err = ParseKMZ(data, s.loc())
	case ".kml":
		t, err = ParseKML(strings.NewReader(string(data)), s.loc())
	default:
		return List{}, &InvalidError{Problems{"Nur .kml- oder .kmz-Dateien"}}
	}
	if err != nil {
		return List{}, &InvalidError{Problems{"Datei: " + err.Error()}}
	}
	l := List{ID: id.New(), OwnerID: a.ID, Name: t.Name, Description: t.Description, DateFrom: t.DateFrom, DateTo: t.DateTo, SourceFile: fileName}
	if v := strings.TrimSpace(f.Name); v != "" {
		l.Name = v
	}
	if v := strings.TrimSpace(f.Description); v != "" {
		l.Description = v
	}
	if f.DateFrom != "" || f.DateTo != "" {
		l.DateFrom, l.DateTo = f.DateFrom, f.DateTo
	}
	if err := CheckTrip(l); err != nil {
		return l, err
	}
	sum := sha256.Sum256(data)
	l.SHA256 = hex.EncodeToString(sum[:])
	for i := range t.Places {
		t.Places[i].ID, t.Places[i].OwnerID = id.New(), a.ID
	}
	l.PlaceCount, l.TrackCount = len(t.Places), len(t.Track)
	return l, s.Repo.InsertTrip(ctx, l, t.Places, t.Track)
}

// SaveTrip changes name, description and range of one of a's trips. Once
// set, the range is required.
func (s *Service) SaveTrip(ctx context.Context, a account.Account, l List) (List, error) {
	if err := CheckTrip(l); err != nil {
		return l, err
	}
	if _, _, err := s.Repo.List(ctx, a.ID, l.ID); err != nil {
		return l, err
	}
	l.OwnerID = a.ID
	return l, s.Repo.UpdateTrip(ctx, l)
}

// ── location history ──────────────────────────────────

// ImportResult says what an import did.
type ImportResult struct {
	Import Import
	// Gap: the file starts after the newest stored time, so everything
	// between Mark and First is missing.
	Gap         bool
	Mark, First time.Time
}

// errNothingNew rolls back an import that added nothing.
var errNothingNew = errors.New("places: nothing new")

// ImportTimeline appends the new part of a Timeline.json to a's location
// history: only entries after the newest stored time; older ones are
// skipped and counted. The file is read as a stream and written while it is
// read, all in one transaction: a bad entry anywhere stores nothing.
// Nothing new → nothing is stored (Import.ID "").
func (s *Service) ImportTimeline(ctx context.Context, a account.Account, fileName string, r io.Reader) (ImportResult, error) {
	mark, has, err := s.Repo.Mark(ctx, a.ID)
	if err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{Mark: mark}
	imp := Import{ID: id.New(), OwnerID: a.ID, Kind: "timeline", FileName: path.Base(strings.ReplaceAll(fileName, `\`, "/")), At: s.now()}
	if imp.FileName == "" || imp.FileName == "." || imp.FileName == "/" {
		imp.FileName = "Timeline.json"
	}
	var parseErr error
	err = s.Repo.InsertImport(ctx, imp, func(sink ImportSink) (Import, error) {
		// Points repeated within the file (overlapping segments) count once.
		type key struct {
			ms       int64
			lat, lon float64
		}
		seen := make(map[key]struct{})
		first, err := ReadTimeline(r, func(p Point) error {
			k := key{p.At.UnixMilli(), p.Latitude, p.Longitude}
			if _, dup := seen[k]; dup || (has && !p.At.After(mark)) {
				imp.Skipped++
				return nil
			}
			seen[k] = struct{}{}
			imp.Points++
			return sink.Point(p)
		}, func(st Stay) error {
			if has && !st.Start.After(mark) {
				imp.Skipped++
				return nil
			}
			imp.Stays++
			return sink.Stay(st)
		})
		res.First = first
		if IsInputError(err) {
			parseErr = err
		}
		if err != nil {
			return imp, err
		}
		if imp.Points == 0 && imp.Stays == 0 {
			return imp, errNothingNew
		}
		return imp, nil
	})
	if parseErr != nil {
		return ImportResult{}, &InvalidError{Problems{"Import abgebrochen, nichts gespeichert: " + parseErr.Error()}}
	}
	res.Gap = has && !res.First.IsZero() && res.First.After(mark)
	if errors.Is(err, errNothingNew) {
		res.Import = Import{Skipped: imp.Skipped}
		return res, nil
	}
	if err != nil {
		return ImportResult{}, err
	}
	res.Import = imp
	return res, nil
}

// ── heatmap ───────────────────────────────────────────

// CellDeg is the heatmap cell size in degrees for a map zoom level: about
// 8 screen pixels.
func CellDeg(zoom int) float64 {
	return 360 / (256 * math.Pow(2, float64(zoom))) * 8
}

// Heat returns the heatmap cells of a's data for zoom and view.
func (s *Service) Heat(ctx context.Context, a account.Account, zoom int, b Box) ([]Cell, error) {
	if zoom < 0 || zoom > 19 || !validLatLon(b.South, b.West) || !validLatLon(b.North, b.East) || b.North < b.South || b.East < b.West {
		return nil, &InvalidError{Problems{"Kartenausschnitt ungültig"}}
	}
	return s.Repo.Heat(ctx, a.ID, CellDeg(zoom), b)
}
