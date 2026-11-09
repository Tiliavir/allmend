package webui

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/commute"
)

func (s *Server) arbeitswegeRoutes(mux *http.ServeMux) {
	m := func(h func(http.ResponseWriter, *http.Request, account.Account)) http.Handler {
		return s.Module(account.Arbeitswege, h)
	}
	mux.Handle("GET /arbeitswege", m(s.awOverview))
	mux.Handle("GET /arbeitswege/neu", m(s.awNew))
	mux.Handle("POST /arbeitswege", m(s.awCreate))
	mux.Handle("GET /arbeitswege/{jahr}/bearbeiten", m(s.awEdit))
	mux.Handle("POST /arbeitswege/{jahr}", m(s.awSave))
	mux.Handle("POST /arbeitswege/{jahr}/loeschen", m(s.awDelete))
}

// Row labels and accessors of the month table, in display order.
var awRows = []struct {
	Key, Label string
	get        func(*commute.Month) *float64
}{
	{"workdays", "Arbeitstage", func(m *commute.Month) *float64 { return &m.Workdays }},
	{"holidays", "Urlaub", func(m *commute.Month) *float64 { return &m.Holidays }},
	{"kurs", "Kurs", func(m *commute.Month) *float64 { return &m.Kurs }},
	{"kompensation", "Kompensation", func(m *commute.Month) *float64 { return &m.Kompensation }},
	{"unfall", "Unfall", func(m *commute.Month) *float64 { return &m.Unfall }},
	{"krank", "Krank", func(m *commute.Month) *float64 { return &m.Krank }},
	{"bike", "Velo", func(m *commute.Month) *float64 { return &m.Bike }},
	{"car_train", "Auto/Zug", func(m *commute.Month) *float64 { return &m.CarTrain }},
	{"ho", "Homeoffice", func(m *commute.Month) *float64 { return &m.HomeOffice }},
}

var monthShort = [...]string{"Jan", "Feb", "Mär", "Apr", "Mai", "Jun", "Jul", "Aug", "Sep", "Okt", "Nov", "Dez"}

type awRow struct {
	Key, Label string
	Values     []float64 // 12 months
	Total      float64
}

type awPerson struct {
	ID, Name string
	Current  bool
}

type awData struct {
	People   []awPerson
	Person   string // display name of the shown owner
	PersonID string
	Own      bool
	Years    []int
	Selected int
	View     *commute.View
	Rows     []awRow
	Months   []string
	// form
	Y       commute.Year
	IsNew   bool
	Statute [2]float64
}

func (s *Server) awPeople(r *http.Request, a account.Account, current string) ([]awPerson, string, error) {
	all, err := s.Accounts.Repo.List(r.Context())
	if err != nil {
		return nil, "", err
	}
	var out []awPerson
	name := ""
	for _, c := range all {
		if c.ID == a.ID || (c.Can(account.Arbeitswege) && c.Status != account.Disabled) {
			out = append(out, awPerson{c.ID, c.DisplayName(), c.ID == current})
			if c.ID == current {
				name = c.DisplayName()
			}
		}
	}
	return out, name, nil
}

func (s *Server) awOverview(w http.ResponseWriter, r *http.Request, a account.Account) {
	owner := r.URL.Query().Get("person")
	if owner == "" {
		owner = a.ID
	}
	people, name, err := s.awPeople(r, a, owner)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if name == "" {
		s.status(w, r, http.StatusNotFound)
		return
	}
	years, err := s.Commute.Repo.Years(r.Context(), owner)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := awData{People: people, Person: name, PersonID: owner, Own: owner == a.ID, Years: years, Months: monthShort[:]}
	if len(years) > 0 {
		d.Selected = years[0]
		if q := r.URL.Query().Get("jahr"); q != "" {
			n, err := strconv.Atoi(q)
			if err != nil || !slices.Contains(years, n) {
				s.status(w, r, http.StatusNotFound)
				return
			}
			d.Selected = n
		}
		y, err := s.Commute.Repo.Get(r.Context(), owner, d.Selected)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v := commute.Compute(y)
		d.View = &v
		for _, row := range awRows {
			ar := awRow{Key: row.Key, Label: row.Label}
			for i := range y.Months {
				ar.Values = append(ar.Values, *row.get(&y.Months[i]))
			}
			ar.Total = *row.get(&v.Totals)
			d.Rows = append(d.Rows, ar)
		}
	}
	s.render(w, r, http.StatusOK, "arbeitswege/overview", Page{Title: "Arbeitswege", Module: account.Arbeitswege, Data: d})
}

func (s *Server) awNew(w http.ResponseWriter, r *http.Request, a account.Account) {
	y, err := s.Commute.Draft(r.Context(), a, time.Now().In(s.Location).Year())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.awForm(w, r, http.StatusOK, y, true, "")
}

func (s *Server) awForm(w http.ResponseWriter, r *http.Request, status int, y commute.Year, isNew bool, errMsg string) {
	low, high := commute.StatutoryRates(y.Year)
	rows := make([]awRow, 0, len(awRows))
	for _, row := range awRows {
		ar := awRow{Key: row.Key, Label: row.Label}
		for i := range y.Months {
			ar.Values = append(ar.Values, *row.get(&y.Months[i]))
		}
		rows = append(rows, ar)
	}
	title := "Neues Jahr"
	if !isNew {
		title = fmt.Sprintf("%d bearbeiten", y.Year)
	}
	s.render(w, r, status, "arbeitswege/form", Page{Title: title, Module: account.Arbeitswege, Error: errMsg,
		Data: awData{Y: y, IsNew: isNew, Rows: rows, Months: monthShort[:], Statute: [2]float64{low, high}}})
}

// awParse reads the year form strictly; problems are listed in German.
func awParse(r *http.Request) (commute.Year, int, []string) {
	var y commute.Year
	var probs []string
	f := func(name, label string, lo, hi float64, opt bool) *float64 {
		raw := strings.TrimSpace(strings.ReplaceAll(r.PostFormValue(name), ",", "."))
		if raw == "" {
			if opt {
				return nil
			}
			v := 0.0
			return &v
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < lo || v > hi {
			probs = append(probs, fmt.Sprintf("%s: %q ist keine Zahl zwischen %g und %g", label, raw, lo, hi))
			return nil
		}
		return &v
	}
	year, err := strconv.Atoi(r.PostFormValue("year"))
	if err != nil || year < 1900 || year > 2200 {
		probs = append(probs, "Jahr fehlt oder ist ungültig")
	}
	y.Year = year
	base, _ := strconv.Atoi(r.PostFormValue("base"))
	for _, d := range []struct {
		dst   *float64
		name  string
		label string
	}{{&y.CarDistKm, "car_dist_km", "Distanz Auto"}, {&y.TrainDistKm, "train_dist_km", "Distanz Zug"}, {&y.BikeDistKm, "bike_dist_km", "Distanz Velo"}} {
		if v := f(d.name, d.label, 0, 500, false); v != nil {
			*d.dst = *v
		}
	}
	y.RateLowCents = f("rate_low_cents", "Satz bis Grenze", 0, 200, true)
	y.RateHighCents = f("rate_high_cents", "Satz ab Grenze", 0, 200, true)
	y.ThresholdKm = f("threshold_km", "Grenze", 0, 500, true)
	if (y.RateLowCents == nil) != (y.RateHighCents == nil) {
		probs = append(probs, "Beide Sätze angeben oder beide leer lassen (gesetzlicher Satz)")
	}
	for i := range y.Months {
		for _, row := range awRows {
			if v := f(fmt.Sprintf("%s_%d", row.Key, i), row.Label+" "+monthShort[i], 0, 31, false); v != nil {
				*row.get(&y.Months[i]) = *v
			}
		}
	}
	return y, base, probs
}

func (s *Server) awCreate(w http.ResponseWriter, r *http.Request, a account.Account) {
	y, _, probs := awParse(r)
	if len(probs) > 0 {
		s.awForm(w, r, http.StatusUnprocessableEntity, y, true, strings.Join(probs, ". ")+".")
		return
	}
	y, err := s.Commute.Create(r.Context(), a, y)
	if errors.Is(err, commute.ErrExists) {
		s.awForm(w, r, http.StatusUnprocessableEntity, y, true, fmt.Sprintf("Das Jahr %d gibt es schon.", y.Year))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, fmt.Sprintf("/arbeitswege?jahr=%d", y.Year), fmt.Sprintf("%d angelegt.", y.Year))
}

func (s *Server) awYear(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("jahr"))
	if err != nil {
		s.status(w, r, http.StatusNotFound)
		return 0, false
	}
	return n, true
}

func (s *Server) awEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	n, ok := s.awYear(w, r)
	if !ok {
		return
	}
	y, err := s.Commute.Repo.Get(r.Context(), a.ID, n)
	if errors.Is(err, commute.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.awForm(w, r, http.StatusOK, y, false, "")
}

func (s *Server) awSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	n, ok := s.awYear(w, r)
	if !ok {
		return
	}
	y, base, probs := awParse(r)
	if y.Year != n {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	y.Version = base
	if len(probs) > 0 {
		s.awForm(w, r, http.StatusUnprocessableEntity, y, false, strings.Join(probs, ". ")+".")
		return
	}
	saved, err := s.Commute.Save(r.Context(), a, y, base)
	switch {
	case errors.Is(err, commute.ErrConflict):
		y.Version = saved.Version
		s.awForm(w, r, http.StatusConflict, y, false, "Das Jahr wurde inzwischen anderswo geändert. Deine Eingaben stehen noch im Formular – prüfe sie und speichere nochmals.")
		return
	case errors.Is(err, commute.ErrNotFound):
		s.status(w, r, http.StatusNotFound)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, fmt.Sprintf("/arbeitswege?jahr=%d", n), "Gespeichert.")
}

func (s *Server) awDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	n, ok := s.awYear(w, r)
	if !ok {
		return
	}
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	err := s.Commute.Delete(r.Context(), a, n)
	if errors.Is(err, commute.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/arbeitswege", fmt.Sprintf("%d gelöscht.", n))
}
