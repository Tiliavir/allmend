package places

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// ImportTimeline appends the new part of a Timeline.json to a's location
// history: only entries after the newest stored time; older ones are
// skipped and counted. Nothing new → nothing is stored (Import.ID "").
func (s *Service) ImportTimeline(ctx context.Context, a account.Account, fileName string, r io.Reader) (ImportResult, error) {
	t, err := ParseTimeline(r)
	if err != nil {
		return ImportResult{}, &InvalidError{Problems{"Import abgebrochen, nichts gespeichert: " + err.Error()}}
	}
	mark, has, err := s.Repo.Mark(ctx, a.ID)
	if err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{Mark: mark, First: t.First}
	imp := Import{ID: id.New(), OwnerID: a.ID, Kind: "timeline", FileName: path.Base(strings.ReplaceAll(fileName, `\`, "/")), At: s.now()}
	if imp.FileName == "" || imp.FileName == "." || imp.FileName == "/" {
		imp.FileName = "Timeline.json"
	}
	var pts []Point
	seen := make(map[Point]bool)
	for _, p := range t.Points {
		key := Point{At: p.At, Latitude: p.Latitude, Longitude: p.Longitude}
		switch {
		case has && !p.At.After(mark), seen[key]:
			imp.Skipped++
		default:
			seen[key] = true
			pts = append(pts, p)
		}
	}
	var stays []Stay
	for _, st := range t.Stays {
		if has && !st.Start.After(mark) {
			imp.Skipped++
			continue
		}
		stays = append(stays, st)
	}
	imp.Points, imp.Stays = len(pts), len(stays)
	res.Gap = has && !t.First.IsZero() && t.First.After(mark)
	if len(pts) == 0 && len(stays) == 0 {
		res.Import = Import{Skipped: imp.Skipped}
		return res, nil
	}
	res.Import = imp
	return res, s.Repo.InsertImport(ctx, imp, pts, stays)
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
