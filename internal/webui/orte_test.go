package webui_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const orteKML = `<kml><Document><name>Rhein</name><description>Mit dem Velo</description>
<Placemark><name>Basel</name><Point><coordinates>7.5886,47.5596</coordinates></Point></Placemark>
<Placemark><name>Weg</name><LineString><coordinates>7.58,47.55 7.60,47.57</coordinates></LineString></Placemark></Document></kml>`

const orteTimeline = `{"semanticSegments":[
{"startTime":"2024-05-03T10:00:00.000+02:00","endTime":"2024-05-03T10:10:00.000+02:00","timelinePath":[{"point":"47.5°, 7.6°","time":"2024-05-03T10:00:00.000+02:00"},{"point":"47.51°, 7.61°","time":"2024-05-03T10:10:00.000+02:00"}]},
{"startTime":"2024-05-03T10:15:00.000+02:00","endTime":"2024-05-03T12:00:00.000+02:00","visit":{"hierarchyLevel":0,"topCandidate":{"semanticType":"WORK","placeLocation":{"latLng":"47.51°, 7.61°"}}}}]}`

// orteSeed imports a trip, an old trip without range and a Timeline.json
// through the forms and returns the pages to render.
func orteSeed(t *testing.T, e *env, aid string) map[string]string {
	t.Helper()
	admin := e.loginAdmin()
	r := e.uploadAs(admin, "/orte/reisen", map[string]string{"von": "2024-05-01", "bis": "2024-05-04"}, "rhein.kml", orteKML)
	if r.StatusCode != http.StatusSeeOther || !strings.Contains(flashOf(r), "Rhein") {
		t.Fatalf("trip import: %d %q", r.StatusCode, flashOf(r))
	}
	trip := r.Header.Get("Location")
	if r := e.uploadAs(admin, "/orte/reisen", map[string]string{"von": "2024-05-01", "bis": "2024-05-04"}, "nochmal.kml", orteKML); r.StatusCode != http.StatusConflict {
		t.Errorf("same file twice: %d", r.StatusCode)
	}
	if r := e.uploadAs(admin, "/orte/reisen", nil, "ohne.kml", strings.Replace(orteKML, "Rhein", "Ohne", 1)); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("trip without range: %d", r.StatusCode)
	}
	if _, err := e.st.DB().ExecContext(context.Background(), `INSERT INTO place_lists (id, owner_id, name, created_at, updated_at) VALUES ('alt', ?, 'Alt', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`, aid); err == nil {
		t.Error("trigger let a new trip without range in")
	}
	r = e.uploadAs(admin, "/orte/verlauf", nil, "Timeline.json", orteTimeline)
	if r.StatusCode != http.StatusSeeOther || !strings.Contains(flashOf(r), "2 neue Punkte und 1 Aufenthalte") {
		t.Fatalf("timeline import: %d %q", r.StatusCode, flashOf(r))
	}
	if r := e.uploadAs(admin, "/orte/verlauf", nil, "Timeline.json", orteTimeline); !strings.Contains(flashOf(r), "Nichts Neues") {
		t.Errorf("second import: %q", flashOf(r))
	}
	if r := e.uploadAs(admin, "/orte/verlauf", nil, "Records.json", `{"locations":[]}`); !strings.Contains(flashOf(r), "semanticSegments") {
		t.Errorf("old format: %q", flashOf(r))
	}
	if _, body := e.get(admin, "/orte/verlauf?tag=2024-05-03"); !strings.Contains(body, "Arbeit") || !strings.Contains(body, "10:15") {
		t.Errorf("day page lacks the stay")
	}
	if r, body := e.get(admin, "/orte/karte/zellen?z=10&s=47&w=7&n=48&e=8"); r.StatusCode != 200 || !strings.HasPrefix(body, "[[") {
		t.Errorf("cells: %d %s", r.StatusCode, body)
	}
	if r, _ := e.get(admin, "/orte/karte/zellen?z=10&s=47&w=7&n=99&e=8"); r.StatusCode != http.StatusBadRequest {
		t.Errorf("bad box: %d", r.StatusCode)
	}
	return map[string]string{
		"orte-reise":         trip,
		"orte-reise-verlauf": trip + "?verlauf=1",
		"orte-reise-edit":    trip + "/bearbeiten",
		"orte-tag":           "/orte/verlauf?tag=2024-05-03",
		"orte-importe":       "/orte/verlauf/importe",
		"orte-karte":         "/orte/karte",
		"orte-reisen":        "/orte",
	}
}

// TestOrteScope: others read trips, never the location history.
func TestOrteScope(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	admin := e.loginAdmin()
	aid := adminID(e)
	pages := orteSeed(t, e, aid)
	kim, _ := e.agLogin(admin, "kim.orte@example.org", "orte")
	trip := pages["orte-reise"]
	if r, body := e.get(kim, trip+"?person="+aid+"&verlauf=1"); r.StatusCode != 200 || !strings.Contains(body, "Basel") || strings.Contains(body, `"verlauf"`) || strings.Contains(body, "bearbeiten") {
		t.Errorf("kim reads trip: %d", r.StatusCode)
	}
	if _, body := e.get(kim, "/orte/verlauf"); strings.Contains(body, "Arbeit") {
		t.Error("kim sees admin's history")
	}
	if _, body := e.get(kim, "/orte/verlauf?tag=2024-05-03&person="+aid); strings.Contains(body, "Arbeit") {
		t.Error("kim sees admin's day")
	}
	if r, _ := e.get(kim, "/orte?person=nobody"); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown person: %d", r.StatusCode)
	}
	if r, _ := e.get(admin, "/orte/zeitachse"); r.StatusCode != http.StatusMovedPermanently || r.Header.Get("Location") != "/orte/verlauf" {
		t.Errorf("old address: %d", r.StatusCode)
	}
	if r, _ := e.get(admin, "/orte/verlauf"); !strings.Contains(r.Header.Get("Content-Security-Policy"), "tile.openstreetmap.org") {
		t.Error("map page without tile CSP")
	}
	if r, body := e.get(kim, "/orte/karte/zellen?z=10&s=-90&w=-180&n=90&e=180"); r.StatusCode != 200 || body != "[]\n" {
		t.Errorf("kim's cells: %s", body)
	}
	id := strings.TrimPrefix(trip, "/orte/reisen/")
	if r := e.post(kim, "/orte/reisen/"+id+"/loeschen", map[string][]string{"confirm": {"1"}}); r.StatusCode != http.StatusNotFound {
		t.Errorf("kim deletes admin's trip: %d", r.StatusCode)
	}
	if r := e.post(admin, "/orte/reisen/"+id, map[string][]string{"name": {"Rhein"}, "von": {"2024-05-01"}, "bis": {""}}); r.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("save without range: %d", r.StatusCode)
	}
	if r := e.post(admin, "/orte/reisen/"+id+"/loeschen", map[string][]string{"confirm": {"1"}}); r.StatusCode != http.StatusSeeOther {
		t.Errorf("delete trip: %d", r.StatusCode)
	}
}
