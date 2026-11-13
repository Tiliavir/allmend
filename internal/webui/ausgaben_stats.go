package webui

import (
	"cmp"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/ausgaben"
)

// agOpt is one choice of a segmented filter (a link keeping the others).
type agOpt struct {
	Label, Href string
	Current     bool
}

// agSlice is a main category in the donut and its legend.
type agSlice struct {
	Main   ausgaben.Main
	Amount int64
	Share  float64 // of the total, 0–1
	Dash   string  // stroke-dasharray of the ring segment (circumference 100)
	Offset string  // stroke-dashoffset
	Href   string  // selects it for the breakdown
	Chosen bool
}

// agSubRow is a subcategory in the breakdown of the chosen main category.
type agSubRow struct {
	Cat    agCat
	Amount int64
	Width  string // bar length in % of the biggest
}

type agStatsData struct {
	Group                     ausgaben.Group
	Years, Views, Currencies  []agOpt
	Currency, YearLabel, View string
	Total                     int64
	Slices                    []agSlice
	Chosen                    *agSlice
	Subs                      []agSubRow
}

func f3(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

// agStats shows the expenses of a group by category: one currency, one year
// or all, the group total or the viewer's own share.
func (s *Server) agStats(w http.ResponseWriter, r *http.Request, a account.Account) {
	v, err := s.Ausgaben.Open(r.Context(), a, r.PathValue("g"))
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	var curs, years []string
	for _, e := range v.Entries {
		if e.Kind != ausgaben.Expense {
			continue
		}
		if !slices.Contains(curs, e.Currency) {
			curs = append(curs, e.Currency)
		}
		if y := e.Date[:4]; !slices.Contains(years, y) {
			years = append(years, y)
		}
	}
	slices.Sort(curs)
	slices.Sort(years)
	slices.Reverse(years)
	q := r.URL.Query()
	cur, year, view, main := q.Get("waehrung"), q.Get("jahr"), q.Get("sicht"), q.Get("bereich")
	if cur == "" && len(curs) > 0 {
		cur = curs[0]
		if slices.Contains(curs, s.BaseCurrency) {
			cur = s.BaseCurrency
		}
	}
	if year == "" && len(years) > 0 {
		year = years[0]
	}
	if view == "" {
		view = "gruppe"
	}
	_, mainOK := ausgaben.MainByKey(main)
	if (len(curs) > 0 && !slices.Contains(curs, cur)) || (year != "alle" && len(years) > 0 && !slices.Contains(years, year)) ||
		(view != "gruppe" && view != "ich") || (main != "" && !mainOK) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	base := "/ausgaben/" + v.Group.ID + "/statistik"
	link := func(k, val string) string {
		p := url.Values{"waehrung": {cur}, "jahr": {year}, "sicht": {view}, "bereich": {main}}
		p.Set(k, val)
		if k != "bereich" {
			p.Del("bereich")
		}
		for _, key := range []string{"waehrung", "jahr", "sicht", "bereich"} {
			if p.Get(key) == "" {
				p.Del(key)
			}
		}
		return base + "?" + p.Encode()
	}
	d := agStatsData{Group: v.Group, Currency: cur, View: view, YearLabel: year}
	if year == "alle" {
		d.YearLabel = "alle Jahre"
	}
	for _, y := range slices.Concat(years, []string{"alle"}) {
		label := y
		if y == "alle" {
			label = "Alle"
		}
		d.Years = append(d.Years, agOpt{label, link("jahr", y), y == year})
	}
	d.Views = []agOpt{{"Gruppe", link("sicht", "gruppe"), view == "gruppe"}, {"Mein Anteil", link("sicht", "ich"), view == "ich"}}
	for _, c := range curs {
		d.Currencies = append(d.Currencies, agOpt{c, link("waehrung", c), c == cur})
	}
	viewer, y := "", year
	if view == "ich" {
		viewer = a.ID
	}
	if y == "alle" {
		y = ""
	}
	bySub := ausgaben.Spending(v.Entries, cur, y, viewer)
	for _, m := range ausgaben.Mains {
		var sum int64
		for _, sub := range m.Subs {
			sum += bySub[sub.Key]
		}
		if sum > 0 {
			d.Slices = append(d.Slices, agSlice{Main: m, Amount: sum, Href: link("bereich", m.Key) + "#aufteilung"})
			d.Total += sum
		}
	}
	slices.SortStableFunc(d.Slices, func(x, y agSlice) int { return cmp.Compare(y.Amount, x.Amount) })
	var done float64
	for i := range d.Slices {
		sl := &d.Slices[i]
		sl.Share = float64(sl.Amount) / float64(d.Total)
		p := sl.Share * 100
		sl.Dash, sl.Offset = f3(p)+" "+f3(100-p), f3(25-done)
		done += p
		if sl.Main.Key == main || (main == "" && i == 0) {
			sl.Chosen = true
			d.Chosen = sl
		}
	}
	if d.Chosen != nil {
		var top int64
		for _, sub := range d.Chosen.Main.Subs {
			top = max(top, bySub[sub.Key])
		}
		for _, sub := range d.Chosen.Main.Subs {
			if n := bySub[sub.Key]; n > 0 {
				d.Subs = append(d.Subs, agSubRow{Cat: agCatOf(sub.Key), Amount: n, Width: f3(float64(n) / float64(top) * 100)})
			}
		}
		slices.SortStableFunc(d.Subs, func(x, y agSubRow) int { return cmp.Compare(y.Amount, x.Amount) })
	}
	s.agPage(w, r, http.StatusOK, "stats", "Statistik", "", d)
}
