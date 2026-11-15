package places

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
)

// KMLTrip is what ParseKML takes from a KML or KMZ file.
type KMLTrip struct {
	Name, Description string
	DateFrom, DateTo  string // from the document's TimeSpan or the track times; may be ""
	Places            []Place
	Track             []TrackPoint
}

// ParseKMZ opens a KMZ archive and parses its main document: doc.kml, or the
// only .kml file in it.
func ParseKMZ(data []byte, loc *time.Location) (KMLTrip, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return KMLTrip{}, errors.New("Datei ist kein gültiges KMZ (ZIP)")
	}
	var main *zip.File
	var kmls []*zip.File
	for _, f := range zr.File {
		if strings.EqualFold(path.Ext(f.Name), ".kml") {
			kmls = append(kmls, f)
			if strings.EqualFold(f.Name, "doc.kml") {
				main = f
			}
		}
	}
	if main == nil {
		if len(kmls) != 1 {
			return KMLTrip{}, fmt.Errorf("KMZ enthält %d KML-Dateien und keine doc.kml", len(kmls))
		}
		main = kmls[0]
	}
	rc, err := main.Open()
	if err != nil {
		return KMLTrip{}, fmt.Errorf("KMZ: %s lässt sich nicht öffnen", main.Name)
	}
	defer func() { _ = rc.Close() }()
	return ParseKML(io.LimitReader(rc, 256<<20), loc)
}

type kmlCoords struct {
	Coordinates string `xml:"coordinates"`
}

type kmlTrack struct {
	Whens  []string `xml:"when"`
	Coords []string `xml:"http://www.google.com/kml/ext/2.2 coord"`
}

type kmlGeometry struct {
	Points      []kmlCoords   `xml:"Point"`
	Lines       []kmlCoords   `xml:"LineString"`
	Tracks      []kmlTrack    `xml:"http://www.google.com/kml/ext/2.2 Track"`
	MultiTracks []kmlGeometry `xml:"http://www.google.com/kml/ext/2.2 MultiTrack"`
	Multi       []kmlGeometry `xml:"MultiGeometry"`
	Polygons    []struct{}    `xml:"Polygon"`
	Rings       []struct{}    `xml:"LinearRing"`
	Models      []struct{}    `xml:"Model"`
}

type kmlPlacemark struct {
	Name        string `xml:"name"`
	Description string `xml:"description"`
	kmlGeometry
}

// ParseKML reads a KML document: its name, description and TimeSpan, every
// Placemark with a Point as a place (name required) and every LineString and
// gx:Track, in document order, as one track. Polygons and models are
// refused, as is a file with neither places nor track. Dates without a zone
// are read in loc.
func ParseKML(r io.Reader, loc *time.Location) (KMLTrip, error) {
	var t KMLTrip
	dec := xml.NewDecoder(r)
	var stack []string
	var begin, end string
	n := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return t, fmt.Errorf("Datei ist kein gültiges KML: %w", err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			switch {
			case e.Name.Local == "Placemark":
				n++
				var pm kmlPlacemark
				if err := dec.DecodeElement(&pm, &e); err != nil {
					return t, fmt.Errorf("Placemark %d: %w", n, err)
				}
				if err := t.addPlacemark(n, pm); err != nil {
					return t, fmt.Errorf("Placemark %d: %w", n, err)
				}
				continue
			case parent == "Document" && (e.Name.Local == "name" || e.Name.Local == "description"):
				var s string
				if err := dec.DecodeElement(&s, &e); err != nil {
					return t, fmt.Errorf("Datei ist kein gültiges KML: %w", err)
				}
				if e.Name.Local == "name" {
					t.Name = strings.TrimSpace(s)
				} else {
					t.Description = strings.TrimSpace(s)
				}
				continue
			case parent == "TimeSpan" && len(stack) >= 2 && stack[len(stack)-2] == "Document" && (e.Name.Local == "begin" || e.Name.Local == "end"):
				var s string
				if err := dec.DecodeElement(&s, &e); err != nil {
					return t, fmt.Errorf("Datei ist kein gültiges KML: %w", err)
				}
				if e.Name.Local == "begin" {
					begin = strings.TrimSpace(s)
				} else {
					end = strings.TrimSpace(s)
				}
				continue
			}
			stack = append(stack, e.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(t.Places) == 0 && len(t.Track) == 0 {
		return t, errors.New("Datei enthält weder Orte (Point) noch einen Track")
	}
	if begin != "" || end != "" {
		from, err := kmlDate(begin, loc)
		if err != nil {
			return t, fmt.Errorf("TimeSpan begin: %w", err)
		}
		to, err := kmlDate(end, loc)
		if err != nil {
			return t, fmt.Errorf("TimeSpan end: %w", err)
		}
		t.DateFrom, t.DateTo = from, to
		return t, nil
	}
	var first, last time.Time
	for _, p := range t.Track {
		if p.At.IsZero() {
			continue
		}
		if first.IsZero() || p.At.Before(first) {
			first = p.At
		}
		if p.At.After(last) {
			last = p.At
		}
	}
	if !first.IsZero() {
		t.DateFrom, t.DateTo = first.In(loc).Format(time.DateOnly), last.In(loc).Format(time.DateOnly)
	}
	return t, nil
}

func (t *KMLTrip) addPlacemark(n int, pm kmlPlacemark) error {
	return t.addGeometry(n, pm, pm.kmlGeometry)
}

func (t *KMLTrip) addGeometry(n int, pm kmlPlacemark, g kmlGeometry) error {
	if len(g.Polygons) > 0 || len(g.Rings) > 0 || len(g.Models) > 0 {
		return errors.New("Flächen und 3D-Modelle werden nicht unterstützt")
	}
	for _, p := range g.Points {
		name := strings.TrimSpace(pm.Name)
		if name == "" {
			return errors.New("Ort ohne Namen")
		}
		pts, err := kmlCoordList(p.Coordinates)
		if err != nil {
			return err
		}
		if len(pts) != 1 {
			return errors.New("Point braucht genau eine Koordinate")
		}
		t.Places = append(t.Places, Place{Name: name, Description: strings.TrimSpace(pm.Description), Latitude: pts[0].Latitude, Longitude: pts[0].Longitude})
	}
	for _, l := range g.Lines {
		pts, err := kmlCoordList(l.Coordinates)
		if err != nil {
			return err
		}
		if len(pts) < 2 {
			return errors.New("LineString braucht mindestens zwei Koordinaten")
		}
		t.Track = append(t.Track, pts...)
	}
	for _, tr := range g.Tracks {
		if len(tr.Whens) != len(tr.Coords) || len(tr.Coords) == 0 {
			return fmt.Errorf("gx:Track hat %d Zeiten und %d Koordinaten", len(tr.Whens), len(tr.Coords))
		}
		for i, c := range tr.Coords {
			at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(tr.Whens[i]))
			if err != nil {
				return fmt.Errorf("gx:Track: Zeit %q ungültig", tr.Whens[i])
			}
			f := strings.Fields(c)
			if len(f) < 2 || len(f) > 3 {
				return fmt.Errorf("gx:coord %q ungültig", c)
			}
			lat, lon, err := lonLat(f[0], f[1])
			if err != nil {
				return err
			}
			t.Track = append(t.Track, TrackPoint{At: at.UTC(), Latitude: lat, Longitude: lon})
		}
	}
	for _, sub := range append(g.MultiTracks, g.Multi...) {
		if err := t.addGeometry(n, pm, sub); err != nil {
			return err
		}
	}
	return nil
}

// kmlCoordList reads "lon,lat[,alt] lon,lat[,alt] …".
func kmlCoordList(s string) ([]TrackPoint, error) {
	var out []TrackPoint
	for _, tuple := range strings.Fields(s) {
		f := strings.Split(tuple, ",")
		if len(f) < 2 || len(f) > 3 {
			return nil, fmt.Errorf("Koordinate %q ungültig", tuple)
		}
		lat, lon, err := lonLat(f[0], f[1])
		if err != nil {
			return nil, err
		}
		out = append(out, TrackPoint{Latitude: lat, Longitude: lon})
	}
	if len(out) == 0 {
		return nil, errors.New("Koordinaten fehlen")
	}
	return out, nil
}

func lonLat(lonS, latS string) (float64, float64, error) {
	lon, err1 := strconv.ParseFloat(lonS, 64)
	lat, err2 := strconv.ParseFloat(latS, 64)
	if err1 != nil || err2 != nil || !validLatLon(lat, lon) {
		return 0, 0, fmt.Errorf("Koordinate %s,%s ungültig", lonS, latS)
	}
	return lat, lon, nil
}

// kmlDate reads a TimeSpan value (date or dateTime) as a local date.
func kmlDate(s string, loc *time.Location) (string, error) {
	if ValidDate(s) {
		return s, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return "", fmt.Errorf("%q ist kein Datum", s)
	}
	if strings.HasSuffix(s, "Z") {
		t = t.In(loc)
	}
	return t.Format(time.DateOnly), nil
}
