package places

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"
)

const timelineOK = `{
 "semanticSegments": [
  {"startTime": "2026-11-13T07:00:00.000+01:00", "endTime": "2026-11-13T07:42:00.000+01:00",
   "startTimeTimezoneUtcOffsetMinutes": 60, "endTimeTimezoneUtcOffsetMinutes": 60,
   "visit": {"hierarchyLevel": 0, "probability": 0.9, "topCandidate": {"placeId": "x", "semanticType": "HOME", "probability": 0.8, "placeLocation": {"latLng": "47.5596°, 7.5886°"}}}},
  {"startTime": "2026-11-13T07:10:00.000+01:00", "endTime": "2026-11-13T07:20:00.000+01:00",
   "visit": {"hierarchyLevel": 1, "topCandidate": {"semanticType": "UNKNOWN", "placeLocation": {"latLng": "47.5°, 7.5°"}}}},
  {"startTime": "2026-11-13T07:42:00.000+01:00", "endTime": "2026-11-13T08:15:00.000+01:00",
   "activity": {"topCandidate": {"type": "CYCLING"}}},
  {"startTime": "2026-11-12T23:00:00.000+01:00", "endTime": "2026-11-13T01:00:00.000+01:00",
   "timelinePath": [{"point": "47.5596°, 7.5886°", "time": "2026-11-12T23:30:00.000+01:00"},
                    {"point": "-33.1°, -70.2°", "time": "2026-11-13T00:10:00.000+01:00"}]}
 ],
 "rawSignals": [{"position": {"LatLng": "1°, 2°"}}],
 "userLocationProfile": {"frequentPlaces": []}
}`

func TestParseTimeline(t *testing.T) {
	tl, err := ParseTimeline(strings.NewReader(timelineOK))
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Stays) != 1 || tl.Stays[0].Label() != "Zuhause" || tl.Stays[0].Offset != 60 || tl.Stays[0].Day != "2026-11-13" {
		t.Errorf("stays %+v", tl.Stays)
	}
	if len(tl.Points) != 2 || tl.Points[0].Day != "2026-11-12" || tl.Points[1].Day != "2026-11-13" || *tl.Points[0].Offset != 60 || tl.Points[1].Latitude != -33.1 {
		t.Errorf("points %+v", tl.Points)
	}
	if !tl.First.Equal(time.Date(2026, 11, 12, 22, 0, 0, 0, time.UTC)) {
		t.Errorf("first %v", tl.First)
	}
	for _, bad := range []string{
		`[]`, `{}`, `{"semanticSegments": {}}`,
		`{"semanticSegments": [{"startTime": "2026-11-13T07:00:00Z", "endTime": "2026-11-13T08:00:00Z"}]}`,
		`{"semanticSegments": [{"startTime": "2026-11-13T07:00:00Z", "endTime": "2026-11-13T06:00:00Z", "activity": {}}]}`,
		`{"semanticSegments": [{"startTime": "gestern", "endTime": "2026-11-13T08:00:00Z", "activity": {}}]}`,
		`{"semanticSegments": [{"startTime": "2026-11-13T07:00:00Z", "endTime": "2026-11-13T08:00:00Z", "timelinePath": [{"point": "geo:47.1,7.5", "time": "2026-11-13T07:00:00Z"}]}]}`,
		`{"semanticSegments": [{"startTime": "2026-11-13T07:00:00Z", "endTime": "2026-11-13T08:00:00Z", "timelinePath": [{"point": "95.0°, 7.5°", "time": "2026-11-13T07:00:00Z"}]}]}`,
		`{"semanticSegments": [{"startTime": "2026-11-13T07:00:00Z", "endTime": "2026-11-13T08:00:00Z", "visit": {"hierarchyLevel": 0, "topCandidate": {"semanticType": "home", "placeLocation": {"latLng": "47°, 7°"}}}}]}`,
		`{"semanticSegments": [{"startTime": "2026-11-13T07:00:00Z", "endTime": "2026-11-13T08:00:00Z", "visit": {"topCandidate": {"semanticType": "HOME", "placeLocation": {"latLng": "47°, 7°"}}}}]}`,
		`{"semanticSegments": [{"startTime": "2026-11-13T07:00:00Z", "endTime": "2026-11-13T08:00:00Z", "visit": {}, "activity": {}}]}`,
	} {
		if _, err := ParseTimeline(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

const kmlOK = `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2" xmlns:gx="http://www.google.com/kml/ext/2.2">
<Document>
 <name>Bodensee-Runde</name>
 <description>Drei Tage um den See</description>
 <Folder><name>Orte</name>
  <Placemark><name>Konstanz</name><description>Start</description><Point><coordinates>9.1758,47.6603,0</coordinates></Point></Placemark>
  <Placemark><name>Bregenz</name><Point><coordinates>9.7420,47.5050</coordinates></Point></Placemark>
 </Folder>
 <Placemark><name>Weg</name><LineString><coordinates>9.17,47.66 9.27,47.69 9.37,47.75</coordinates></LineString></Placemark>
 <Placemark><gx:Track><when>2025-09-20T07:30:00Z</when><when>2025-09-22T22:30:00Z</when>
   <gx:coord>9.47 47.65 400</gx:coord><gx:coord>9.59 47.59 400</gx:coord></gx:Track></Placemark>
</Document>
</kml>`

func TestParseKML(t *testing.T) {
	zurich, _ := time.LoadLocation("Europe/Zurich")
	k, err := ParseKML(strings.NewReader(kmlOK), zurich)
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "Bodensee-Runde" || k.Description != "Drei Tage um den See" || len(k.Places) != 2 || k.Places[0].Name != "Konstanz" || k.Places[0].Latitude != 47.6603 {
		t.Errorf("%+v", k)
	}
	// 22:30Z on the 22nd is the 23rd in Zurich.
	if len(k.Track) != 5 || k.DateFrom != "2025-09-20" || k.DateTo != "2025-09-23" || !k.Track[0].At.IsZero() || k.Track[3].At.IsZero() {
		t.Errorf("track %d %s–%s", len(k.Track), k.DateFrom, k.DateTo)
	}
	span := strings.Replace(kmlOK, "<name>Bodensee-Runde</name>", "<name>X</name><TimeSpan><begin>2025-09-19</begin><end>2025-09-21T10:00:00+02:00</end></TimeSpan>", 1)
	if k, err := ParseKML(strings.NewReader(span), zurich); err != nil || k.DateFrom != "2025-09-19" || k.DateTo != "2025-09-21" || k.Name != "X" {
		t.Errorf("timespan %+v %v", k, err)
	}
	for _, bad := range []string{
		`nicht xml`,
		`<kml><Document><name>Leer</name></Document></kml>`,
		`<kml><Document><Placemark><Point><coordinates>9,47</coordinates></Point></Placemark></Document></kml>`,
		`<kml><Document><Placemark><name>A</name><Point><coordinates>9,97</coordinates></Point></Placemark></Document></kml>`,
		`<kml><Document><Placemark><name>A</name><Polygon/></Placemark></Document></kml>`,
		`<kml xmlns:gx="http://www.google.com/kml/ext/2.2"><Document><Placemark><gx:Track><when>2025-01-01T00:00:00Z</when></gx:Track></Placemark></Document></kml>`,
		`<kml><Document><Placemark><name>A</name><LineString><coordinates>9,47</coordinates></LineString></Placemark></Document></kml>`,
	} {
		if _, err := ParseKML(strings.NewReader(bad), zurich); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestParseKMZ(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("doc.kml")
	_, _ = w.Write([]byte(kmlOK))
	w, _ = zw.Create("files/other.kml")
	_, _ = w.Write([]byte("<kml/>"))
	_ = zw.Close()
	if k, err := ParseKMZ(buf.Bytes(), time.UTC); err != nil || k.Name != "Bodensee-Runde" {
		t.Fatalf("%+v %v", k, err)
	}
	if _, err := ParseKMZ([]byte("kein zip"), time.UTC); err == nil {
		t.Error("accepted no zip")
	}
}

func TestCheckTrip(t *testing.T) {
	ok := List{Name: "A", DateFrom: "2025-01-01", DateTo: "2025-01-02"}
	if CheckTrip(ok) != nil {
		t.Error("refused valid trip")
	}
	for _, bad := range []List{{Name: "A"}, {DateFrom: "2025-01-01", DateTo: "2025-01-02"}, {Name: "A", DateFrom: "2025-01-02", DateTo: "2025-01-01"}, {Name: "A", DateFrom: "2025-01-01"}, {Name: "A", DateFrom: "1.1.2025", DateTo: "2025-01-02"}} {
		if CheckTrip(bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
