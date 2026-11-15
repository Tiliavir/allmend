package places

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
)

// Timeline is what ParseTimeline takes from a Timeline.json.
type Timeline struct {
	Points []Point
	Stays  []Stay
	First  time.Time // earliest time in the file (zero if it has none)
}

// ParseTimeline reads the Timeline.json that Google Maps exports on the
// phone ({"semanticSegments":[…], "rawSignals":[…], …}). It takes the
// timelinePath points and the top-level visits (hierarchyLevel 0); activities
// and timeline memories are skipped, as are rawSignals and
// userLocationProfile. The file is read as a stream. A segment of unknown
// kind, a missing field or a bad value aborts with the segment's number.
func ParseTimeline(r io.Reader) (Timeline, error) {
	var t Timeline
	dec := json.NewDecoder(r)
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return t, errors.New("Datei ist kein JSON-Objekt (Timeline.json erwartet)")
	}
	seen := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return t, fmt.Errorf("Datei ist kein gültiges JSON: %w", err)
		}
		key, _ := tok.(string)
		if key != "semanticSegments" {
			if err := skipValue(dec); err != nil {
				return t, fmt.Errorf("Datei ist kein gültiges JSON: %w", err)
			}
			continue
		}
		seen = true
		if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
			return t, errors.New(`"semanticSegments" ist keine Liste`)
		}
		for i := 1; dec.More(); i++ {
			var s rawSegment
			if err := dec.Decode(&s); err != nil {
				return t, fmt.Errorf("Abschnitt %d: kein gültiges JSON: %w", i, err)
			}
			if err := s.add(&t); err != nil {
				return t, fmt.Errorf("Abschnitt %d: %w", i, err)
			}
		}
		if _, err := dec.Token(); err != nil {
			return t, fmt.Errorf("Datei ist kein gültiges JSON: %w", err)
		}
	}
	if !seen {
		return t, errors.New(`Datei enthält kein Feld "semanticSegments" – ist es eine Timeline.json vom Handy?`)
	}
	return t, nil
}

// skipValue consumes one JSON value without keeping it.
func skipValue(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}

type rawSegment struct {
	StartTime    string `json:"startTime"`
	EndTime      string `json:"endTime"`
	TimelinePath *[]struct {
		Point string `json:"point"`
		Time  string `json:"time"`
	} `json:"timelinePath"`
	Visit *struct {
		HierarchyLevel *int `json:"hierarchyLevel"`
		TopCandidate   *struct {
			SemanticType  string `json:"semanticType"`
			PlaceLocation *struct {
				LatLng string `json:"latLng"`
			} `json:"placeLocation"`
		} `json:"topCandidate"`
	} `json:"visit"`
	Activity       json.RawMessage `json:"activity"`
	TimelineMemory json.RawMessage `json:"timelineMemory"`
}

func (s rawSegment) add(t *Timeline) error {
	kinds := 0
	for _, has := range []bool{s.TimelinePath != nil, s.Visit != nil, s.Activity != nil, s.TimelineMemory != nil} {
		if has {
			kinds++
		}
	}
	if kinds != 1 {
		return errors.New("unbekannte Art (erwartet: timelinePath, visit, activity oder timelineMemory)")
	}
	start, err := parseTime(s.StartTime, "startTime")
	if err != nil {
		return err
	}
	end, err := parseTime(s.EndTime, "endTime")
	if err != nil {
		return err
	}
	if end.Before(start) {
		return errors.New("endTime liegt vor startTime")
	}
	t.first(start)
	switch {
	case s.TimelinePath != nil:
		for j, p := range *s.TimelinePath {
			at, err := parseTime(p.Time, "time")
			if err != nil {
				return fmt.Errorf("Punkt %d: %w", j+1, err)
			}
			lat, lon, err := parseLatLng(p.Point)
			if err != nil {
				return fmt.Errorf("Punkt %d: %w", j+1, err)
			}
			off := offsetMin(at)
			t.first(at)
			t.Points = append(t.Points, Point{At: at.UTC(), Offset: &off, Day: localDay(at), Latitude: lat, Longitude: lon})
		}
	case s.Visit != nil:
		v := s.Visit
		if v.HierarchyLevel == nil || *v.HierarchyLevel < 0 {
			return errors.New("visit ohne hierarchyLevel")
		}
		if *v.HierarchyLevel > 0 {
			return nil // a stay inside another one (e.g. a shop in a mall)
		}
		if v.TopCandidate == nil || v.TopCandidate.PlaceLocation == nil {
			return errors.New("visit ohne topCandidate.placeLocation")
		}
		if !semanticRe.MatchString(v.TopCandidate.SemanticType) {
			return fmt.Errorf("semanticType %q ungültig", v.TopCandidate.SemanticType)
		}
		lat, lon, err := parseLatLng(v.TopCandidate.PlaceLocation.LatLng)
		if err != nil {
			return err
		}
		t.Stays = append(t.Stays, Stay{Start: start.UTC(), End: end.UTC(), Offset: offsetMin(start), Day: localDay(start), EndDay: localDay(end.In(start.Location())),
			Latitude: lat, Longitude: lon, SemanticType: v.TopCandidate.SemanticType})
	}
	return nil
}

func (t *Timeline) first(at time.Time) {
	if t.First.IsZero() || at.Before(t.First) {
		t.First = at.UTC()
	}
}

var (
	latLngRe   = regexp.MustCompile(`^(-?\d{1,3}(?:\.\d+)?)°, ?(-?\d{1,3}(?:\.\d+)?)°$`)
	semanticRe = regexp.MustCompile(`^[A-Z][A-Z_]*$`)
)

// parseLatLng reads Google's "47.5596°, 7.5886°".
func parseLatLng(s string) (float64, float64, error) {
	m := latLngRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, fmt.Errorf("Koordinaten %q ungültig", s)
	}
	lat, err1 := strconv.ParseFloat(m[1], 64)
	lon, err2 := strconv.ParseFloat(m[2], 64)
	if err1 != nil || err2 != nil || !validLatLon(lat, lon) {
		return 0, 0, fmt.Errorf("Koordinaten %q ungültig", s)
	}
	return lat, lon, nil
}

// parseTime reads an RFC 3339 time with offset, keeping the offset.
func parseTime(s, field string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("%s fehlt", field)
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q ungültig", field, s)
	}
	if off := offsetMin(t); off < -840 || off > 840 {
		return time.Time{}, fmt.Errorf("%s %q: Zeitzone ungültig", field, s)
	}
	return t, nil
}

func offsetMin(t time.Time) int {
	_, off := t.Zone()
	return off / 60
}

// localDay is the date in t's own zone.
func localDay(t time.Time) string { return t.Format(time.DateOnly) }
