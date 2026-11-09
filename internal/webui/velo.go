package webui

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/markdown"
	"github.com/tiliavir/allmend/internal/velo"
)

func (s *Server) veloRoutes(mux *http.ServeMux) {
	m := func(h func(http.ResponseWriter, *http.Request, account.Account)) http.Handler {
		return s.Module(account.Velo, h)
	}
	mux.Handle("GET /velo", m(s.veloOverview))
	mux.Handle("GET /velo/touren", m(s.veloTours))
	mux.Handle("GET /velo/touren/neu", m(s.veloTourNew))
	mux.Handle("POST /velo/touren", m(s.veloTourSave))
	mux.Handle("GET /velo/touren/{id}", m(s.veloTourEdit))
	mux.Handle("POST /velo/touren/{id}", m(s.veloTourSave))
	mux.Handle("POST /velo/touren/{id}/loeschen", m(s.veloTourDelete))
	mux.Handle("GET /velo/velos", m(s.veloBikes))
	mux.Handle("GET /velo/velos/neu", m(s.veloBikeNew))
	mux.Handle("POST /velo/velos", m(s.veloBikeSave))
	mux.Handle("GET /velo/velos/{id}", m(s.veloBike))
	mux.Handle("GET /velo/velos/{id}/bearbeiten", m(s.veloBikeEdit))
	mux.Handle("POST /velo/velos/{id}", m(s.veloBikeSave))
	mux.Handle("POST /velo/velos/{id}/loeschen", m(s.veloBikeDelete))
	mux.Handle("GET /velo/velos/{id}/reparaturen/neu", m(s.veloRepairNew))
	mux.Handle("POST /velo/velos/{id}/reparaturen", m(s.veloRepairSave))
	mux.Handle("GET /velo/reparaturen/{rid}", m(s.veloRepairEdit))
	mux.Handle("POST /velo/reparaturen/{rid}", m(s.veloRepairSave))
	mux.Handle("POST /velo/reparaturen/{rid}/loeschen", m(s.veloRepairDelete))
	mux.Handle("GET /velo/jahre/{jahr}/kommentar", m(s.veloComment))
	mux.Handle("POST /velo/jahre/{jahr}/kommentar", m(s.veloCommentSave))
	mux.Handle("GET /velo/ziel/{jahr}/{monat}", m(s.veloGoal))
	mux.Handle("POST /velo/ziel/{jahr}/{monat}", m(s.veloGoalSave))
}

func (s *Server) veloPage(w http.ResponseWriter, r *http.Request, status int, name, title, errMsg string, d any) {
	s.render(w, r, status, "velo/"+name, Page{Title: title, Module: account.Velo, Error: errMsg, Data: d})
}

func (s *Server) veloErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, velo.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	s.fail(w, r, err)
}

func problems(err error) string {
	var inv *velo.InvalidError
	if errors.As(err, &inv) {
		return strings.Join(inv.Problems, ". ") + "."
	}
	return ""
}

// ── overview ──────────────────────────────────────────

type veloOverview struct {
	velo.Overview
	YearList  []int
	Bikes     []velo.Bike
	BikeID    string
	BikeName  string
	Comment   template.HTML
	Chart     map[string]any
	MonthName string
	GoalPct   int
	GoalDash  string
	DeltaKm   float64
	HeatWeeks int
	CurYear   int
}

func (s *Server) veloOverview(w http.ResponseWriter, r *http.Request, a account.Account) {
	ctx := r.Context()
	years, err := s.Velo.Years(ctx, a)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	year := years[0]
	if q := r.URL.Query().Get("jahr"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || !slices.Contains(years, n) {
			s.status(w, r, http.StatusNotFound)
			return
		}
		year = n
	}
	bikes, err := s.Velo.Repo.Bikes(ctx, a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := veloOverview{YearList: years, Bikes: bikes, BikeID: r.URL.Query().Get("velo"), CurYear: s.Velo.Today().Year()}
	if d.BikeID != "" {
		i := slices.IndexFunc(bikes, func(b velo.Bike) bool { return b.ID == d.BikeID })
		if i < 0 {
			s.status(w, r, http.StatusNotFound)
			return
		}
		d.BikeName = bikes[i].Name
	}
	if d.Overview, err = s.Velo.Overview(ctx, a, year, d.BikeID); err != nil {
		s.fail(w, r, err)
		return
	}
	d.DeltaKm = d.Stats.Km - d.Previous.Km
	for _, y := range d.Years {
		if y.Year == year && y.Comment != "" {
			res, err := markdown.Render(y.Comment, markdown.Tasks{})
			if err != nil {
				s.fail(w, r, err)
				return
			}
			d.Comment = res.HTML
		}
	}
	if d.ShowGoal {
		d.MonthName = velo.MonthNames[d.GoalMonth-1]
		if d.Goal > 0 {
			d.GoalPct = int(d.MonthKm / d.Goal * 100)
			const circ = 2 * 3.14159265 * 52
			d.GoalDash = fmt.Sprintf("%.1f %.1f", circ*min(d.MonthKm/d.Goal, 1), circ)
		}
	}
	d.HeatWeeks = (len(d.Heat) + 6) / 7
	// html/template marshals this to JSON inside the application/json block.
	d.Chart = map[string]any{"year": year, "this": d.CumThis, "prev": d.CumPrev}
	p := Page{Title: "Velo", Module: account.Velo, Data: d,
		Scripts: []Script{{Src: "/static/vendor/chart-4.6.1.umd.min.js"}, {Src: "/static/velo.js", Module: true}}}
	s.render(w, r, http.StatusOK, "velo/overview", p)
}

// ── tours ─────────────────────────────────────────────

type tourRow struct {
	velo.Tour
	Bike string
}

type toursData struct {
	Tours []tourRow
	Years []int
	Year  int
	Km    float64
	Tour  velo.Tour
	Bikes []velo.Bike
	Draft bool
}

func (s *Server) veloTours(w http.ResponseWriter, r *http.Request, a account.Account) {
	ctx := r.Context()
	years, err := s.Velo.Years(ctx, a)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	year := years[0]
	if q := r.URL.Query().Get("jahr"); q != "" {
		if year, err = strconv.Atoi(q); err != nil || !slices.Contains(years, year) {
			s.status(w, r, http.StatusNotFound)
			return
		}
	}
	all, err := s.Velo.Repo.Tours(ctx, a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	bikes, err := s.Velo.Repo.Bikes(ctx, a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names := map[string]string{}
	for _, b := range bikes {
		names[b.ID] = b.Name
	}
	d := toursData{Years: years, Year: year}
	prefix := strconv.Itoa(year)
	for _, t := range all {
		if t.Date[:4] == prefix {
			d.Tours = append(d.Tours, tourRow{t, names[t.BikeID]})
			d.Km += t.DistanceKm
		}
	}
	s.veloPage(w, r, http.StatusOK, "tours", "Touren", "", d)
}

func (s *Server) tourForm(w http.ResponseWriter, r *http.Request, a account.Account, status int, t velo.Tour, errMsg string) {
	bikes, err := s.Velo.Repo.Bikes(r.Context(), a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	title := "Neue Tour"
	if t.ID != "" {
		title = "Tour bearbeiten"
	}
	s.veloPage(w, r, status, "tour_form", title, errMsg, toursData{Tour: t, Bikes: bikes})
}

func (s *Server) veloTourNew(w http.ResponseWriter, r *http.Request, a account.Account) {
	t := velo.Tour{Date: s.Velo.Today().Format(time.DateOnly)}
	// Default to the bike of the latest tour.
	if ts, err := s.Velo.Repo.Tours(r.Context(), a.ID); err == nil && len(ts) > 0 {
		t.BikeID = ts[0].BikeID
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	s.tourForm(w, r, a, http.StatusOK, t, "")
}

func (s *Server) veloTourEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	t, err := s.Velo.Repo.Tour(r.Context(), a.ID, r.PathValue("id"))
	if err != nil {
		s.veloErr(w, r, err)
		return
	}
	s.tourForm(w, r, a, http.StatusOK, t, "")
}

func (s *Server) veloTourSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	t := velo.Tour{ID: r.PathValue("id"), BikeID: r.PostFormValue("bike"), Date: r.PostFormValue("date"),
		Title: strings.TrimSpace(r.PostFormValue("title")), Description: strings.TrimSpace(strings.ReplaceAll(r.PostFormValue("description"), "\r\n", "\n"))}
	t.Version, _ = strconv.Atoi(r.PostFormValue("version"))
	var bad []string
	km, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(r.PostFormValue("km")), ",", "."), 64)
	if err != nil {
		bad = append(bad, "Distanz ist keine Zahl")
	}
	t.DistanceKm = km
	if hm := strings.TrimSpace(r.PostFormValue("hm")); hm != "" {
		if t.ElevationM, err = strconv.Atoi(hm); err != nil {
			bad = append(bad, "Höhenmeter sind keine ganze Zahl")
		}
	}
	if len(bad) > 0 {
		s.tourForm(w, r, a, http.StatusUnprocessableEntity, t, strings.Join(bad, ". ")+".")
		return
	}
	saved, err := s.Velo.SaveTour(r.Context(), a, t)
	switch {
	case problems(err) != "":
		s.tourForm(w, r, a, http.StatusUnprocessableEntity, t, problems(err))
	case errors.Is(err, velo.ErrConflict):
		s.tourForm(w, r, a, http.StatusConflict, t, "Die Tour wurde inzwischen anderswo geändert. Lade die Seite neu, um die aktuelle Fassung zu sehen.")
	case err != nil:
		s.veloErr(w, r, err)
	default:
		s.redirect(w, r, "/velo/touren?jahr="+saved.Date[:4], "Tour gespeichert.")
	}
}

func (s *Server) veloTourDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	if err := s.Velo.Repo.DeleteTour(r.Context(), a.ID, r.PathValue("id")); err != nil {
		s.veloErr(w, r, err)
		return
	}
	s.redirect(w, r, "/velo/touren", "Tour gelöscht.")
}

// ── bikes ─────────────────────────────────────────────

type bikeRow struct {
	velo.Bike
	Km    float64
	Tours int
}

type bikesData struct {
	Bikes   []bikeRow
	Bike    velo.Bike
	Km      float64
	Tours   int
	Repairs []velo.Repair
	Costs   int64
	Repair  velo.Repair
	Base    string
}

func (s *Server) veloBikes(w http.ResponseWriter, r *http.Request, a account.Account) {
	bikes, err := s.Velo.Repo.Bikes(r.Context(), a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ts, err := s.Velo.Repo.Tours(r.Context(), a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows := make([]bikeRow, len(bikes))
	for i, b := range bikes {
		rows[i].Bike = b
		for _, t := range ts {
			if t.BikeID == b.ID {
				rows[i].Km += t.DistanceKm
				rows[i].Tours++
			}
		}
	}
	s.veloPage(w, r, http.StatusOK, "bikes", "Velos", "", bikesData{Bikes: rows})
}

func (s *Server) veloBike(w http.ResponseWriter, r *http.Request, a account.Account) {
	ctx := r.Context()
	b, err := s.Velo.Repo.Bike(ctx, a.ID, r.PathValue("id"))
	if err != nil {
		s.veloErr(w, r, err)
		return
	}
	ts, err := s.Velo.Repo.Tours(ctx, a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	reps, err := s.Velo.Repo.Repairs(ctx, a.ID, b.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := bikesData{Bike: b, Repairs: reps}
	for _, t := range velo.Filter(ts, b.ID) {
		d.Km += t.DistanceKm
		d.Tours++
	}
	for _, x := range reps {
		d.Costs += x.CostCents
	}
	d.Base = s.BaseCurrency
	s.veloPage(w, r, http.StatusOK, "bike", b.Name, "", d)
}

func (s *Server) bikeForm(w http.ResponseWriter, r *http.Request, status int, b velo.Bike, errMsg string) {
	title := "Neues Velo"
	if b.ID != "" {
		title = b.Name + " bearbeiten"
	}
	s.veloPage(w, r, status, "bike_form", title, errMsg, bikesData{Bike: b, Base: s.BaseCurrency})
}

func (s *Server) veloBikeNew(w http.ResponseWriter, r *http.Request, _ account.Account) {
	s.bikeForm(w, r, http.StatusOK, velo.Bike{Color: "#1f6feb"}, "")
}

func (s *Server) veloBikeEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	b, err := s.Velo.Repo.Bike(r.Context(), a.ID, r.PathValue("id"))
	if err != nil {
		s.veloErr(w, r, err)
		return
	}
	s.bikeForm(w, r, http.StatusOK, b, "")
}

func (s *Server) veloBikeSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	b := velo.Bike{ID: r.PathValue("id"), Name: strings.TrimSpace(r.PostFormValue("name")), Brand: strings.TrimSpace(r.PostFormValue("brand")),
		Model: strings.TrimSpace(r.PostFormValue("model")), Acquired: r.PostFormValue("acquired"), Notes: strings.TrimSpace(r.PostFormValue("notes")),
		Color: strings.ToLower(r.PostFormValue("color"))}
	b.Version, _ = strconv.Atoi(r.PostFormValue("version"))
	var bad []string
	var err error
	if b.PurchaseCents, err = parseMoney(r.PostFormValue("purchase")); err != nil {
		bad = append(bad, "Kaufpreis: "+err.Error())
	}
	if b.AccessoriesCents, err = parseMoney(r.PostFormValue("accessories")); err != nil {
		bad = append(bad, "Zubehör: "+err.Error())
	}
	if len(bad) > 0 {
		s.bikeForm(w, r, http.StatusUnprocessableEntity, b, strings.Join(bad, ". ")+".")
		return
	}
	saved, err := s.Velo.SaveBike(r.Context(), a, b)
	switch {
	case problems(err) != "":
		s.bikeForm(w, r, http.StatusUnprocessableEntity, b, problems(err))
	case errors.Is(err, velo.ErrConflict):
		s.bikeForm(w, r, http.StatusConflict, b, "Das Velo wurde inzwischen anderswo geändert. Lade die Seite neu.")
	case err != nil:
		s.veloErr(w, r, err)
	default:
		s.redirect(w, r, "/velo/velos/"+saved.ID, "Velo gespeichert.")
	}
}

func (s *Server) veloBikeDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	err := s.Velo.Repo.DeleteBike(r.Context(), a.ID, id)
	if errors.Is(err, velo.ErrInUse) {
		s.redirect(w, r, "/velo/velos/"+id, "Velos mit Touren oder Reparaturen lassen sich nicht löschen.")
		return
	}
	if err != nil {
		s.veloErr(w, r, err)
		return
	}
	s.redirect(w, r, "/velo/velos", "Velo gelöscht.")
}

// ── repairs ───────────────────────────────────────────

func (s *Server) repairForm(w http.ResponseWriter, r *http.Request, a account.Account, status int, x velo.Repair, errMsg string) {
	b, err := s.Velo.Repo.Bike(r.Context(), a.ID, x.BikeID)
	if err != nil {
		s.veloErr(w, r, err)
		return
	}
	s.veloPage(w, r, status, "repair_form", "Reparatur · "+b.Name, errMsg, bikesData{Bike: b, Repair: x, Base: s.BaseCurrency})
}

func (s *Server) veloRepairNew(w http.ResponseWriter, r *http.Request, a account.Account) {
	s.repairForm(w, r, a, http.StatusOK, velo.Repair{BikeID: r.PathValue("id"), Date: s.Velo.Today().Format(time.DateOnly)}, "")
}

func (s *Server) veloRepairEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	x, err := s.Velo.Repo.Repair(r.Context(), a.ID, r.PathValue("rid"))
	if err != nil {
		s.veloErr(w, r, err)
		return
	}
	s.repairForm(w, r, a, http.StatusOK, x, "")
}

func (s *Server) veloRepairSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	x := velo.Repair{ID: r.PathValue("rid"), BikeID: r.PathValue("id"), Date: r.PostFormValue("date"), Title: strings.TrimSpace(r.PostFormValue("title")),
		Comment: strings.TrimSpace(r.PostFormValue("comment"))}
	if x.ID != "" {
		old, err := s.Velo.Repo.Repair(r.Context(), a.ID, x.ID)
		if err != nil {
			s.veloErr(w, r, err)
			return
		}
		x.BikeID = old.BikeID
	}
	x.Version, _ = strconv.Atoi(r.PostFormValue("version"))
	var bad []string
	var err error
	if x.CostCents, err = parseMoney(r.PostFormValue("cost")); err != nil {
		bad = append(bad, "Kosten: "+err.Error())
	}
	if odo := strings.TrimSpace(strings.ReplaceAll(r.PostFormValue("odometer"), ",", ".")); odo != "" {
		v, err := strconv.ParseFloat(odo, 64)
		if err != nil {
			bad = append(bad, "Kilometerstand ist keine Zahl")
		}
		x.OdometerKm = &v
	}
	if len(bad) > 0 {
		s.repairForm(w, r, a, http.StatusUnprocessableEntity, x, strings.Join(bad, ". ")+".")
		return
	}
	saved, err := s.Velo.SaveRepair(r.Context(), a, x)
	switch {
	case problems(err) != "":
		s.repairForm(w, r, a, http.StatusUnprocessableEntity, x, problems(err))
	case errors.Is(err, velo.ErrConflict):
		s.repairForm(w, r, a, http.StatusConflict, x, "Die Reparatur wurde inzwischen anderswo geändert. Lade die Seite neu.")
	case err != nil:
		s.veloErr(w, r, err)
	default:
		s.redirect(w, r, "/velo/velos/"+saved.BikeID, "Reparatur gespeichert.")
	}
}

func (s *Server) veloRepairDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	x, err := s.Velo.Repo.Repair(r.Context(), a.ID, r.PathValue("rid"))
	if err != nil {
		s.veloErr(w, r, err)
		return
	}
	if err := s.Velo.Repo.DeleteRepair(r.Context(), a.ID, x.ID); err != nil {
		s.veloErr(w, r, err)
		return
	}
	s.redirect(w, r, "/velo/velos/"+x.BikeID, "Reparatur gelöscht.")
}

// ── year comment, monthly goal ────────────────────────

type commentData struct {
	Year     int
	Month    int
	Text     string
	Km       float64
	MonthStr string
}

func pathInt(r *http.Request, name string, lo, hi int) (int, bool) {
	n, err := strconv.Atoi(r.PathValue(name))
	return n, err == nil && n >= lo && n <= hi
}

func (s *Server) veloComment(w http.ResponseWriter, r *http.Request, a account.Account) {
	y, ok := pathInt(r, "jahr", 1900, 2200)
	if !ok {
		s.status(w, r, http.StatusNotFound)
		return
	}
	c, err := s.Velo.Repo.Comment(r.Context(), a.ID, y)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.veloPage(w, r, http.StatusOK, "comment_form", fmt.Sprintf("Kommentar %d", y), "", commentData{Year: y, Text: c})
}

func (s *Server) veloCommentSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	y, ok := pathInt(r, "jahr", 1900, 2200)
	if !ok {
		s.status(w, r, http.StatusNotFound)
		return
	}
	text := strings.TrimSpace(strings.ReplaceAll(r.PostFormValue("comment"), "\r\n", "\n"))
	if err := s.Velo.SetComment(r.Context(), a, y, text); err != nil {
		if msg := problems(err); msg != "" {
			s.veloPage(w, r, http.StatusUnprocessableEntity, "comment_form", fmt.Sprintf("Kommentar %d", y), msg, commentData{Year: y, Text: text})
			return
		}
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, fmt.Sprintf("/velo?jahr=%d", y), "Kommentar gespeichert.")
}

func (s *Server) veloGoal(w http.ResponseWriter, r *http.Request, a account.Account) {
	y, ok1 := pathInt(r, "jahr", 1900, 2200)
	m, ok2 := pathInt(r, "monat", 1, 12)
	if !ok1 || !ok2 {
		s.status(w, r, http.StatusNotFound)
		return
	}
	km, err := s.Velo.Repo.Goal(r.Context(), a.ID, y, m)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.veloPage(w, r, http.StatusOK, "goal_form", "Monatsziel", "", commentData{Year: y, Month: m, Km: km, MonthStr: velo.MonthNames[m-1]})
}

func (s *Server) veloGoalSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	y, ok1 := pathInt(r, "jahr", 1900, 2200)
	m, ok2 := pathInt(r, "monat", 1, 12)
	if !ok1 || !ok2 {
		s.status(w, r, http.StatusNotFound)
		return
	}
	raw := strings.TrimSpace(strings.ReplaceAll(r.PostFormValue("km"), ",", "."))
	km := 0.0
	var err error
	if raw != "" {
		km, err = strconv.ParseFloat(raw, 64)
	}
	if err == nil {
		err = s.Velo.SetGoal(r.Context(), a, y, m, km)
	}
	if err != nil {
		msg := problems(err)
		if msg == "" && errors.Is(err, strconv.ErrSyntax) {
			msg = "Das ist keine Zahl."
		}
		if msg == "" {
			s.fail(w, r, err)
			return
		}
		s.veloPage(w, r, http.StatusUnprocessableEntity, "goal_form", "Monatsziel", msg, commentData{Year: y, Month: m, MonthStr: velo.MonthNames[m-1]})
		return
	}
	s.redirect(w, r, fmt.Sprintf("/velo?jahr=%d", y), "Ziel gespeichert.")
}
