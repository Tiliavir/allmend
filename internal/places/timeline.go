package places

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Timeline is what ParseTimeline takes from a Timeline.json.
type Timeline struct {
	Points []Point
	Stays  []Stay
	First  time.Time // earliest time in the file (zero if it has none)
}

// ParseTimeline reads a whole Timeline.json into memory (see ReadTimeline).
func ParseTimeline(r io.Reader) (Timeline, error) {
	var t Timeline
	first, err := ReadTimeline(r,
		func(p Point) error { t.Points = append(t.Points, p); return nil },
		func(s Stay) error { t.Stays = append(t.Stays, s); return nil })
	t.First = first
	return t, err
}

// timelineReader hands each entry to its callbacks as it is read.
type timelineReader struct {
	onPoint  func(Point) error
	onStay   func(Stay) error
	first    time.Time
	segments int
}

// sinkError is an error of a callback: passed on as is, not as bad input.
type sinkError struct{ err error }

func (e sinkError) Error() string { return e.err.Error() }
func (e sinkError) Unwrap() error { return e.err }

// IsInputError reports whether an error of ReadTimeline is about the file
// (and not of a callback).
func IsInputError(err error) bool {
	var se sinkError
	return err != nil && !errors.As(err, &se)
}

// ReadTimeline reads the Timeline.json that Google Maps exports on the
// phone. Two shapes exist: an object {"semanticSegments":[…], "rawSignals":
// […], …} with "47.5°, 7.5°" coordinates, and a bare array of segments with
// "geo:47.5,7.5" coordinates (newer exports, iOS). It takes the
// timelinePath points and the top-level visits (hierarchyLevel 0);
// activities and timeline memories are skipped, as are rawSignals and
// userLocationProfile. The file is read as a stream. A segment of unknown
// kind, a missing field or a bad value aborts with the segment's number.
// Every entry goes to onPoint or onStay as soon as it is read; an error of
// a callback stops reading and is returned (see IsInputError). first is the
// earliest time in the file.
func ReadTimeline(r io.Reader, onPoint func(Point) error, onStay func(Stay) error) (first time.Time, err error) {
	t := &timelineReader{onPoint: onPoint, onStay: onStay}
	defer func() { first = t.first }()
	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err != nil {
		return time.Time{}, errors.New("Datei ist kein JSON (Timeline.json erwartet)")
	}
	switch tok {
	case json.Delim('['):
		if err := t.readSegments(dec); err != nil {
			return time.Time{}, err
		}
		return time.Time{}, t.nonEmpty()
	case json.Delim('{'):
	default:
		return time.Time{}, errors.New("Datei ist weder JSON-Objekt noch -Liste (Timeline.json erwartet)")
	}
	var keys []string
	seen := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return time.Time{}, fmt.Errorf("Datei ist kein gültiges JSON: %w", err)
		}
		key, _ := tok.(string)
		if key != "semanticSegments" {
			keys = append(keys, key)
			if err := skipValue(dec); err != nil {
				return time.Time{}, fmt.Errorf("Datei ist kein gültiges JSON: %w", err)
			}
			continue
		}
		seen = true
		if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
			return time.Time{}, errors.New(`"semanticSegments" ist keine Liste`)
		}
		if err := t.readSegments(dec); err != nil {
			return time.Time{}, err
		}
	}
	if !seen {
		return time.Time{}, fmt.Errorf(`Datei enthält kein Feld "semanticSegments", sondern %q – ist es eine Timeline.json vom Handy?`, keys)
	}
	return time.Time{}, t.nonEmpty()
}

func (t *timelineReader) nonEmpty() error {
	if t.segments == 0 {
		return errors.New("Datei enthält keine Einträge")
	}
	return nil
}

// readSegments reads a list of segments up to and including its closing "]".
func (t *timelineReader) readSegments(dec *json.Decoder) error {
	for i := 1; dec.More(); i++ {
		t.segments++
		var s rawSegment
		if err := dec.Decode(&s); err != nil {
			return fmt.Errorf("Abschnitt %d: kein gültiges JSON: %w", i, err)
		}
		if err := s.add(t); err != nil {
			var se sinkError
			if errors.As(err, &se) {
				return se
			}
			return fmt.Errorf("Abschnitt %d: %w", i, err)
		}
	}
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("Datei ist kein gültiges JSON: %w", err)
	}
	return nil
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
		Point  string          `json:"point"`
		Time   string          `json:"time"`
		Offset json.RawMessage `json:"durationMinutesOffsetFromStartTime"` // number or string
	} `json:"timelinePath"`
	Visit *struct {
		HierarchyLevel json.RawMessage `json:"hierarchyLevel"` // number or string
		TopCandidate   *struct {
			SemanticType  string          `json:"semanticType"`
			PlaceLocation json.RawMessage `json:"placeLocation"` // {"latLng": "…"} or "geo:…"
		} `json:"topCandidate"`
	} `json:"visit"`
	Activity       json.RawMessage `json:"activity"`
	TimelineMemory json.RawMessage `json:"timelineMemory"`
}

func (s rawSegment) add(t *timelineReader) error {
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
	t.seen(start)
	switch {
	case s.TimelinePath != nil:
		for j, p := range *s.TimelinePath {
			at, err := pathTime(start, p.Time, p.Offset)
			if err != nil {
				return fmt.Errorf("Punkt %d: %w", j+1, err)
			}
			lat, lon, err := parseLatLng(p.Point)
			if err != nil {
				return fmt.Errorf("Punkt %d: %w", j+1, err)
			}
			off := offsetMin(at)
			t.seen(at)
			if err := t.onPoint(Point{At: at.UTC(), Offset: &off, Day: localDay(at), Latitude: lat, Longitude: lon}); err != nil {
				return sinkError{err}
			}
		}
	case s.Visit != nil:
		v := s.Visit
		level, err := flexInt(v.HierarchyLevel)
		if err != nil || level < 0 {
			return errors.New("visit ohne gültiges hierarchyLevel")
		}
		if level > 0 {
			return nil // a stay inside another one (e.g. a shop in a mall)
		}
		if v.TopCandidate == nil || v.TopCandidate.PlaceLocation == nil {
			return errors.New("visit ohne topCandidate.placeLocation")
		}
		kind := semanticType(v.TopCandidate.SemanticType)
		if !semanticRe.MatchString(kind) {
			return fmt.Errorf("semanticType %q ungültig", v.TopCandidate.SemanticType)
		}
		loc, err := placeLocation(v.TopCandidate.PlaceLocation)
		if err != nil {
			return err
		}
		lat, lon, err := parseLatLng(loc)
		if err != nil {
			return err
		}
		if err := t.onStay(Stay{Start: start.UTC(), End: end.UTC(), Offset: offsetMin(start), Day: localDay(start), EndDay: localDay(end.In(start.Location())),
			Latitude: lat, Longitude: lon, SemanticType: kind}); err != nil {
			return sinkError{err}
		}
	}
	return nil
}

// pathTime is a path point's own time, or the segment start plus
// durationMinutesOffsetFromStartTime (the array shape has only that).
func pathTime(start time.Time, at string, offset json.RawMessage) (time.Time, error) {
	if at != "" {
		return parseTime(at, "time")
	}
	if offset == nil {
		return time.Time{}, errors.New("time fehlt")
	}
	m, err := flexInt(offset)
	if err != nil || m < 0 {
		return time.Time{}, fmt.Errorf("durationMinutesOffsetFromStartTime %s ungültig", offset)
	}
	return start.Add(time.Duration(m) * time.Minute), nil
}

// flexInt reads a whole number given as JSON number or string ("0").
func flexInt(raw json.RawMessage) (int, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strconv.Atoi(s)
	}
	var n int
	err := json.Unmarshal(raw, &n)
	return n, err
}

// placeLocation reads {"latLng": "…"} or a plain string.
func placeLocation(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var o struct {
		LatLng string `json:"latLng"`
	}
	if err := json.Unmarshal(raw, &o); err != nil || o.LatLng == "" {
		return "", errors.New("placeLocation ungültig")
	}
	return o.LatLng, nil
}

// semanticType writes Google's kinds the same way in both shapes: "Home"
// and "HOME", "Searched Address" and "SEARCHED_ADDRESS".
func semanticType(s string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", "_"))
}

func (t *timelineReader) seen(at time.Time) {
	if t.first.IsZero() || at.Before(t.first) {
		t.first = at.UTC()
	}
}

var (
	latLngRe   = regexp.MustCompile(`^(-?\d{1,3}(?:\.\d+)?)°, ?(-?\d{1,3}(?:\.\d+)?)°$`)
	geoRe      = regexp.MustCompile(`^geo:(-?\d{1,3}(?:\.\d+)?),(-?\d{1,3}(?:\.\d+)?)$`)
	semanticRe = regexp.MustCompile(`^[A-Z][A-Z_]*$`)
)

// parseLatLng reads Google's "47.5596°, 7.5886°" or "geo:47.5596,7.5886".
func parseLatLng(s string) (float64, float64, error) {
	m := latLngRe.FindStringSubmatch(s)
	if m == nil {
		m = geoRe.FindStringSubmatch(s)
	}
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
