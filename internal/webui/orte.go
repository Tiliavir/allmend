package webui

import (
	"errors"
	"net/http"
	"path"
	"slices"
	"strconv"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/places"
)

// cspMap allows OpenStreetMap tiles on map pages.
const cspMap = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https://tile.openstreetmap.org; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// maxUpload limits a Google Location History upload.
const maxUpload = 256 << 20

func (s *Server) orteRoutes(mux *http.ServeMux) {
	m := func(h func(http.ResponseWriter, *http.Request, account.Account)) http.Handler {
		return s.Module(account.Orte, h)
	}
	mux.Handle("GET /orte", m(s.orteLists))
	mux.Handle("GET /orte/listen/{id}", m(s.orteList))
	mux.Handle("GET /orte/zeitachse", m(s.orteTimeline))
	mux.Handle("POST /orte/import", m(s.orteImport))
}

type orteData struct {
	People   []awPerson
	PersonID string
	Person   string
	Own      bool
	Lists    []places.List
	List     places.List
	Places   []places.Place
	Map      map[string]any
	Days     []string
	Day      string
	Visits   int
	Prev     string
	Next     string
}

// orteOwner resolves ?person= to an account the viewer may read.
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
	d := orteData{PersonID: owner, Own: owner == a.ID}
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

func (s *Server) mapPage(w http.ResponseWriter, r *http.Request, status int, name, title, errMsg string, d orteData) {
	w.Header().Set(cspHeader, cspMap)
	s.render(w, r, status, name, Page{Title: title, Module: account.Orte, Error: errMsg, Data: d, Scripts: []Script{
		{Src: "/static/vendor/leaflet-1.9.4.js"}, {Src: "/static/orte.js", Module: true},
	}})
}

func (s *Server) orteLists(w http.ResponseWriter, r *http.Request, a account.Account) {
	d, ok := s.orteOwner(w, r, a)
	if !ok {
		return
	}
	var err error
	if d.Lists, err = s.Places.Lists(r.Context(), d.PersonID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "orte/lists", Page{Title: "Orte", Module: account.Orte, Data: d})
}

func (s *Server) orteList(w http.ResponseWriter, r *http.Request, a account.Account) {
	d, ok := s.orteOwner(w, r, a)
	if !ok {
		return
	}
	l, ps, err := s.Places.List(r.Context(), d.PersonID, r.PathValue("id"))
	if errors.Is(err, places.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var track []places.Visit
	if l.SourceFile != "" {
		if track, err = s.Places.VisitsBySource(r.Context(), d.PersonID, l.SourceFile); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	d.List, d.Places, d.Map = l, ps, mapData(ps, track)
	s.mapPage(w, r, http.StatusOK, "orte/list", l.Name, "", d)
}

func mapData(ps []places.Place, track []places.Visit) map[string]any {
	pts := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		pts = append(pts, map[string]any{"name": p.Name, "lat": p.Latitude, "lon": p.Longitude})
	}
	path := make([][2]float64, 0, len(track))
	for _, v := range track {
		path = append(path, [2]float64{v.Latitude, v.Longitude})
	}
	return map[string]any{"places": pts, "path": path}
}

func (s *Server) orteTimeline(w http.ResponseWriter, r *http.Request, a account.Account) {
	d, ok := s.orteOwner(w, r, a)
	if !ok {
		return
	}
	days, err := s.Places.VisitDays(r.Context(), d.PersonID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Days = days
	if len(days) == 0 {
		s.render(w, r, http.StatusOK, "orte/timeline", Page{Title: "Zeitachse", Module: account.Orte, Data: d})
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
	from, _ := time.Parse(time.DateOnly, d.Day)
	vs, err := s.Places.VisitsOn(r.Context(), d.PersonID, from, from.AddDate(0, 0, 1))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Visits, d.Map = len(vs), mapData(nil, vs)
	s.mapPage(w, r, http.StatusOK, "orte/timeline", "Zeitachse", "", d)
}

func (s *Server) orteImport(w http.ResponseWriter, r *http.Request, a account.Account) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	f, hdr, err := r.FormFile("datei")
	if err != nil {
		s.redirect(w, r, "/orte", "Keine Datei erhalten (höchstens 256 MB).")
		return
	}
	defer func() { _ = f.Close() }()
	vs, err := places.ParseGoogleLocation(f, a.ID, path.Base(hdr.Filename))
	if err != nil {
		s.redirect(w, r, "/orte", "Import abgebrochen, nichts gespeichert: "+err.Error())
		return
	}
	n, err := s.Places.InsertVisits(r.Context(), vs)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/orte/zeitachse", strconv.Itoa(n)+" von "+strconv.Itoa(len(vs))+" Punkten importiert (der Rest war schon da).")
}
