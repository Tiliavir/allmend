package webui

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/places"
	"github.com/tiliavir/allmend/internal/velo"
)

// cspMap allows OpenStreetMap tiles on map pages.
const cspMap = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https://tile.openstreetmap.org; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// Upload limits: one KML/KMZ file, one Timeline.json.
const (
	maxTripUpload     = 64 << 20
	maxTimelineUpload = 512 << 20
)

// Trips are readable by everyone with access to Orte; the location history
// and the heatmap only by their owner (ADR-20261115-orte), so those routes
// never take a person.
func (s *Server) orteRoutes(mux *http.ServeMux) {
	m := func(h func(http.ResponseWriter, *http.Request, account.Account)) http.Handler {
		return s.Module(account.Orte, h)
	}
	mux.Handle("GET /orte", m(s.orteTrips))
	mux.Handle("POST /orte/reisen", m(s.orteTripImport))
	mux.Handle("GET /orte/reisen/{id}", m(s.orteTrip))
	mux.Handle("GET /orte/reisen/{id}/bearbeiten", m(s.orteTripEdit))
	mux.Handle("POST /orte/reisen/{id}", m(s.orteTripSave))
	mux.Handle("POST /orte/reisen/{id}/loeschen", m(s.orteTripDelete))
	mux.Handle("GET /orte/verlauf", m(s.orteDay))
	mux.Handle("POST /orte/verlauf", m(s.orteTimelineImport))
	mux.Handle("GET /orte/verlauf/importe", m(s.orteImports))
	mux.Handle("POST /orte/verlauf/importe/{id}/loeschen", m(s.orteImportDelete))
	mux.Handle("GET /orte/karte", m(s.orteHeat))
	mux.Handle("GET /orte/karte/zellen", m(s.orteHeatCells))
	// Addresses before the rework (bookmarks, the installed app).
	mux.Handle("GET /orte/listen/{id}", m(func(w http.ResponseWriter, r *http.Request, _ account.Account) {
		to := "/orte/reisen/" + url.PathEscape(r.PathValue("id"))
		if p := r.URL.Query().Get("person"); p != "" {
			to += "?" + url.Values{"person": {p}}.Encode()
		}
		http.Redirect(w, r, to, http.StatusMovedPermanently)
	}))
	mux.Handle("GET /orte/zeitachse", m(func(w http.ResponseWriter, r *http.Request, _ account.Account) {
		http.Redirect(w, r, "/orte/verlauf", http.StatusMovedPermanently)
	}))
}

type orteData struct {
	Tab      string
	People   []awPerson
	PersonID string
	Person   string
	Own      bool
	Lists    []places.List
	List     places.List
	Places   []places.Place
	Verlauf  bool // trip: show the owner's location history of its range
	Form     places.TripForm
	Map      map[string]any
	Days     []string
	Day      string
	Prev     string
	Next     string
	Points   int
	Stays    []stayRow
	Imports  []places.Import
	Last     *places.Import
	Total    int
}

type stayRow struct {
	Label, Where, From, To string
}

func (s *Server) ortePage(w http.ResponseWriter, r *http.Request, status int, name, title, errMsg string, d orteData, scripts ...Script) {
	if len(scripts) > 0 {
		w.Header().Set(cspHeader, cspMap)
	}
	s.render(w, r, status, "orte/"+name, Page{Title: title, Module: account.Orte, Error: errMsg, Data: d, Scripts: scripts})
}

var mapScripts = []Script{{Src: "/static/vendor/leaflet-1.9.4.js"}, {Src: "/static/orte.js", Module: true}}

func (s *Server) orteErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, places.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	s.fail(w, r, err)
}

func placeProblems(err error) string {
	var inv *places.InvalidError
	if errors.As(err, &inv) {
		return strings.Join(inv.Problems, ". ") + "."
	}
	return ""
}

// orteOwner resolves ?person= to an account the viewer may read trips of.
func (s *Server) orteOwner(w http.ResponseWriter, r *http.Request, a account.Account) (orteData, bool) {
	owner := r.URL.Query().Get("person")
	if owner == "" {
		owner = a.ID
	}
	all, err := s.Accounts.Repo.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return orteData{}, false
	}
	d := orteData{Tab: "trips", PersonID: owner, Own: owner == a.ID}
	for _, c := range all {
		if c.ID == a.ID || (c.Can(account.Orte) && c.Status != account.Disabled) {
			d.People = append(d.People, awPerson{c.ID, c.DisplayName(), c.ID == owner})
			if c.ID == owner {
				d.Person = c.DisplayName()
			}
		}
	}
	if d.Person == "" {
		s.status(w, r, http.StatusNotFound)
		return d, false
	}
	return d, true
}

// ── trips ─────────────────────────────────────────────

func (s *Server) orteTrips(w http.ResponseWriter, r *http.Request, a account.Account) {
	d, ok := s.orteOwner(w, r, a)
	if !ok {
		return
	}
	s.renderTrips(w, r, http.StatusOK, d, "")
}

func (s *Server) renderTrips(w http.ResponseWriter, r *http.Request, status int, d orteData, errMsg string) {
	var err error
	if d.Lists, err = s.Places.Repo.Lists(r.Context(), d.PersonID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.ortePage(w, r, status, "lists", "Orte", errMsg, d)
}

func (s *Server) orteTripImport(w http.ResponseWriter, r *http.Request, a account.Account) {
	d := orteData{Tab: "trips", PersonID: a.ID, Own: true}
	r.Body = http.MaxBytesReader(w, r.Body, maxTripUpload)
	f, hdr, err := r.FormFile("datei")
	d.Form = places.TripForm{Name: r.PostFormValue("name"), Description: normText(r.PostFormValue("beschreibung")), DateFrom: r.PostFormValue("von"), DateTo: r.PostFormValue("bis")}
	if err != nil {
		s.renderTrips(w, r, http.StatusUnprocessableEntity, d, "Keine Datei erhalten (höchstens 64 MB).")
		return
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		s.renderTrips(w, r, http.StatusUnprocessableEntity, d, "Datei unvollständig (höchstens 64 MB).")
		return
	}
	l, err := s.Places.ImportTrip(r.Context(), a, hdr.Filename, data, d.Form)
	switch p := placeProblems(err); {
	case p != "":
		s.renderTrips(w, r, http.StatusUnprocessableEntity, d, "Nichts gespeichert. "+p)
	case errors.Is(err, places.ErrDuplicate):
		s.renderTrips(w, r, http.StatusConflict, d, "Diese Datei ist schon als Reise importiert.")
	case err != nil:
		s.fail(w, r, err)
	default:
		s.redirect(w, r, "/orte/reisen/"+l.ID, "Reise „"+l.Name+"“ mit "+strconv.Itoa(l.PlaceCount)+" Orten importiert.")
	}
}

func (s *Server) orteTrip(w http.ResponseWriter, r *http.Request, a account.Account) {
	d, ok := s.orteOwner(w, r, a)
	if !ok {
		return
	}
	l, ps, err := s.Places.Repo.List(r.Context(), d.PersonID, r.PathValue("id"))
	if err != nil {
		s.orteErr(w, r, err)
		return
	}
	track, err := s.Places.Repo.Track(r.Context(), d.PersonID, l.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.List, d.Places = l, ps
	d.Map = mapData(ps, trackPath(track))
	// Only the owner sees their own history, and only for a full range.
	if d.Own && !l.MissingRange() && r.URL.Query().Get("verlauf") == "1" {
		pts, err := s.Places.Repo.PointsBetween(r.Context(), a.ID, l.DateFrom, l.DateTo)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		d.Verlauf, d.Map["verlauf"] = true, pointPath(pts)
	}
	s.ortePage(w, r, http.StatusOK, "list", l.Name, "", d, mapScripts...)
}

func (s *Server) orteTripEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	l, _, err := s.Places.Repo.List(r.Context(), a.ID, r.PathValue("id"))
	if err != nil {
		s.orteErr(w, r, err)
		return
	}
	s.ortePage(w, r, http.StatusOK, "trip_form", "Reise bearbeiten", "", orteData{Tab: "trips", Own: true, PersonID: a.ID, List: l})
}

func (s *Server) orteTripSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	l := places.List{ID: r.PathValue("id"), Name: strings.TrimSpace(r.PostFormValue("name")), Description: normText(r.PostFormValue("beschreibung")),
		DateFrom: r.PostFormValue("von"), DateTo: r.PostFormValue("bis")}
	_, err := s.Places.SaveTrip(r.Context(), a, l)
	if p := placeProblems(err); p != "" {
		s.ortePage(w, r, http.StatusUnprocessableEntity, "trip_form", "Reise bearbeiten", p, orteData{Tab: "trips", Own: true, PersonID: a.ID, List: l})
		return
	}
	if err != nil {
		s.orteErr(w, r, err)
		return
	}
	s.redirect(w, r, "/orte/reisen/"+l.ID, "Reise gespeichert.")
}

func (s *Server) orteTripDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	if err := s.Places.Repo.DeleteTrip(r.Context(), a.ID, r.PathValue("id")); err != nil {
		s.orteErr(w, r, err)
		return
	}
	s.redirect(w, r, "/orte", "Reise gelöscht.")
}

func normText(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")) }

// ── map data ──────────────────────────────────────────

func mapData(ps []places.Place, path [][2]float64) map[string]any {
	pts := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		pts = append(pts, map[string]any{"name": p.Name, "lat": p.Latitude, "lon": p.Longitude})
	}
	if path == nil {
		path = [][2]float64{}
	}
	return map[string]any{"places": pts, "path": path}
}

func trackPath(t []places.TrackPoint) [][2]float64 {
	out := make([][2]float64, 0, len(t))
	for _, p := range t {
		out = append(out, [2]float64{p.Latitude, p.Longitude})
	}
	return out
}

func pointPath(pts []places.Point) [][2]float64 {
	out := make([][2]float64, 0, len(pts))
	for _, p := range pts {
		out = append(out, [2]float64{p.Latitude, p.Longitude})
	}
	return out
}

// ── location history ──────────────────────────────────

func (s *Server) orteDay(w http.ResponseWriter, r *http.Request, a account.Account) {
	d := orteData{Tab: "history", Own: true, PersonID: a.ID}
	ctx := r.Context()
	days, err := s.Places.Repo.Days(ctx, a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	imps, err := s.Places.Repo.Imports(ctx, a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(imps) > 0 {
		d.Last = &imps[0]
	}
	d.Days = days
	if len(days) == 0 {
		s.ortePage(w, r, http.StatusOK, "timeline", "Verlauf", "", d)
		return
	}
	d.Day = days[len(days)-1]
	if q := r.URL.Query().Get("tag"); q != "" {
		if !slices.Contains(days, q) {
			s.status(w, r, http.StatusNotFound)
			return
		}
		d.Day = q
	}
	i := slices.Index(days, d.Day)
	if i > 0 {
		d.Prev = days[i-1]
	}
	if i < len(days)-1 {
		d.Next = days[i+1]
	}
	pts, err := s.Places.Repo.PointsOn(ctx, a.ID, d.Day)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stays, err := s.Places.Repo.StaysOn(ctx, a.ID, d.Day)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Points, d.Map = len(pts), mapData(nil, pointPath(pts))
	marks := make([]map[string]any, 0, len(stays))
	for _, st := range stays {
		row := stayRow{Label: st.Label(), Where: coord(st.Latitude, st.Longitude), From: stayTime(st.Start, st.Offset, st.Day, d.Day), To: stayTime(st.End, st.Offset, st.EndDay, d.Day)}
		d.Stays = append(d.Stays, row)
		name := row.Label
		if name == "" {
			name = "Aufenthalt"
		}
		marks = append(marks, map[string]any{"name": name + " " + row.From + "–" + row.To, "lat": st.Latitude, "lon": st.Longitude})
	}
	d.Map["stays"] = marks
	s.ortePage(w, r, http.StatusOK, "timeline", "Verlauf", "", d, mapScripts...)
}

// stayTime shows a stay's time in its own zone; a time on another day
// than the one shown gets the date.
func stayTime(t time.Time, offset int, day, shown string) string {
	lt := t.In(time.FixedZone("", offset*60))
	if day != shown {
		return lt.Format("02.01. 15:04")
	}
	return lt.Format("15:04")
}

func coord(lat, lon float64) string {
	return strconv.FormatFloat(lat, 'f', 4, 64) + "°, " + strconv.FormatFloat(lon, 'f', 4, 64) + "°"
}

func (s *Server) orteTimelineImport(w http.ResponseWriter, r *http.Request, a account.Account) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTimelineUpload)
	f, hdr, err := r.FormFile("datei")
	if err != nil {
		s.redirect(w, r, "/orte/verlauf", "Keine Datei erhalten (höchstens 512 MB).")
		return
	}
	defer func() { _ = f.Close() }()
	res, err := s.Places.ImportTimeline(r.Context(), a, hdr.Filename, f)
	if p := placeProblems(err); p != "" {
		s.redirect(w, r, "/orte/verlauf", p)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/orte/verlauf", importMessage(res, s.Location))
}

func importMessage(res places.ImportResult, loc *time.Location) string {
	imp := res.Import
	var msg string
	if imp.ID == "" {
		msg = "Nichts Neues: alle " + velo.FmtInt(imp.Skipped) + " Einträge der Datei sind schon da."
	} else {
		msg = velo.FmtInt(imp.Points) + " neue Punkte und " + velo.FmtInt(imp.Stays) + " Aufenthalte übernommen"
		if imp.Skipped > 0 {
			msg += ", " + velo.FmtInt(imp.Skipped) + " ältere übersprungen"
		}
		msg += "."
	}
	if res.Gap {
		msg += " Lücke: Zwischen " + res.Mark.In(loc).Format("02.01.2006 15:04") + " und " + res.First.In(loc).Format("02.01.2006 15:04") + " fehlen Daten (die Datei beginnt später)."
	}
	return msg
}

func (s *Server) orteImports(w http.ResponseWriter, r *http.Request, a account.Account) {
	imps, err := s.Places.Repo.Imports(r.Context(), a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := orteData{Tab: "history", Own: true, PersonID: a.ID, Imports: imps}
	s.ortePage(w, r, http.StatusOK, "imports", "Importe", "", d)
}

func (s *Server) orteImportDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	if err := s.Places.Repo.DeleteImport(r.Context(), a.ID, r.PathValue("id")); err != nil {
		s.orteErr(w, r, err)
		return
	}
	s.redirect(w, r, "/orte/verlauf/importe", "Import mit seinen Punkten und Aufenthalten entfernt.")
}

// ── heatmap ───────────────────────────────────────────

func (s *Server) orteHeat(w http.ResponseWriter, r *http.Request, a account.Account) {
	d := orteData{Tab: "heat", Own: true, PersonID: a.ID}
	b, ok, err := s.Places.Repo.Bounds(r.Context(), a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		s.ortePage(w, r, http.StatusOK, "heat", "Karte", "", d)
		return
	}
	// The first view: everything, at a zoom that fits it. The page asks
	// for finer cells when the map moves (not in the static demo).
	z := fitZoom(b)
	cells, err := s.Places.Heat(r.Context(), a, z, places.Box{South: -90, West: -180, North: 90, East: 180})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, c := range cells {
		d.Total += c.N
	}
	d.Map = map[string]any{"bounds": [][2]float64{{b.South, b.West}, {b.North, b.East}}, "zoom": z, "cells": heatCells(cells), "live": !s.Demo}
	s.ortePage(w, r, http.StatusOK, "heat", "Karte", "", d,
		Script{Src: "/static/vendor/leaflet-1.9.4.js"}, Script{Src: "/static/vendor/leaflet-heat-0.2.0.js"}, Script{Src: "/static/orte-karte.js", Module: true})
}

// fitZoom is the zoom at which b fits a map about 700 px wide.
func fitZoom(b places.Box) int {
	span := max(b.East-b.West, (b.North-b.South)*1.5, 0.005)
	z := int(math.Floor(math.Log2(360 / span * 700 / 256)))
	return min(max(z, 1), 15)
}

func heatCells(cells []places.Cell) [][3]float64 {
	out := make([][3]float64, 0, len(cells))
	for _, c := range cells {
		out = append(out, [3]float64{math.Round(c.Latitude*1e5) / 1e5, math.Round(c.Longitude*1e5) / 1e5, float64(c.N)})
	}
	return out
}

func (s *Server) orteHeatCells(w http.ResponseWriter, r *http.Request, a account.Account) {
	q := r.URL.Query()
	z, err := strconv.Atoi(q.Get("z"))
	var b places.Box
	var bad bool
	for _, f := range []struct {
		key string
		dst *float64
	}{{"s", &b.South}, {"w", &b.West}, {"n", &b.North}, {"e", &b.East}} {
		v, perr := strconv.ParseFloat(q.Get(f.key), 64)
		bad = bad || perr != nil || math.IsNaN(v)
		*f.dst = v
	}
	if err != nil || bad {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	cells, err := s.Places.Heat(r.Context(), a, z, b)
	if placeProblems(err) != "" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(heatCells(cells))
}
