package velo

import (
	"math"
	"sort"
	"strconv"
	"time"
)

// MonthNames are German month names.
var MonthNames = [12]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}

// YearRow sums one year.
type YearRow struct {
	Year       int
	Km         float64
	Tours      int
	ElevationM int
	Months     [12]float64
	Comment    string
}

// Record is a personal best.
type Record struct {
	Label, Value, Detail, TourID string
}

// HeatCell is one day of the heatmap; Level 0–4 (-1: padding before Jan 1).
type HeatCell struct {
	Date  string
	Km    float64
	Level int
}

// Overview is everything the Velo start page shows for one year.
type Overview struct {
	Year      int
	Stats     YearRow
	Previous  YearRow // same period of the previous year (up to today if Year is the current year)
	Years     []YearRow
	Records   []Record
	Heat      []HeatCell
	CumThis   []float64 // cumulative km per day of year
	CumPrev   []float64
	MonthKm   float64 // current month (only if Year is current)
	Goal      float64
	ShowGoal  bool
	GoalMonth int
}

// Filter keeps tours of one bike ("" = all).
func Filter(ts []Tour, bikeID string) []Tour {
	if bikeID == "" {
		return ts
	}
	out := make([]Tour, 0, len(ts))
	for _, t := range ts {
		if t.BikeID == bikeID {
			out = append(out, t)
		}
	}
	return out
}

func ym(date string) (int, int) {
	y, _ := strconv.Atoi(date[:4])
	m, _ := strconv.Atoi(date[5:7])
	return y, m
}

// YearRows sums tours per year, newest first.
func YearRows(ts []Tour) []YearRow {
	by := map[int]*YearRow{}
	for _, t := range ts {
		y, m := ym(t.Date)
		r := by[y]
		if r == nil {
			r = &YearRow{Year: y}
			by[y] = r
		}
		r.Km += t.DistanceKm
		r.Tours++
		r.ElevationM += t.ElevationM
		r.Months[m-1] += t.DistanceKm
	}
	out := make([]YearRow, 0, len(by))
	for _, r := range by {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Year > out[j].Year })
	return out
}

// Summarize sums tours with date <= until (YYYY-MM-DD, "" = no limit) in year.
func Summarize(ts []Tour, year int, until string) YearRow {
	r := YearRow{Year: year}
	prefix := strconv.Itoa(year)
	for _, t := range ts {
		if t.Date[:4] != prefix || (until != "" && t.Date > until) {
			continue
		}
		_, m := ym(t.Date)
		r.Km += t.DistanceKm
		r.Tours++
		r.ElevationM += t.ElevationM
		r.Months[m-1] += t.DistanceKm
	}
	return r
}

// Records computes all-time bests.
func Records(ts []Tour) []Record {
	var longest, highest Tour
	monthKm, monthElev, monthCount := map[string]float64{}, map[string]int{}, map[string]int{}
	yearKm := map[int]float64{}
	for _, t := range ts {
		if t.DistanceKm > longest.DistanceKm {
			longest = t
		}
		if t.ElevationM > highest.ElevationM {
			highest = t
		}
		k := t.Date[:7]
		monthKm[k] += t.DistanceKm
		monthElev[k] += t.ElevationM
		monthCount[k]++
		y, _ := ym(t.Date)
		yearKm[y] += t.DistanceKm
	}
	recs := []Record{}
	if longest.ID != "" {
		recs = append(recs, Record{"Längste Tour", fmtKm(longest.DistanceKm), DayLabel(longest.Date), longest.ID})
	}
	if highest.ID != "" {
		recs = append(recs, Record{"Meiste Höhenmeter", FmtInt(highest.ElevationM) + " m", DayLabel(highest.Date), highest.ID})
	}
	if k, v := bestF(monthKm); k != "" {
		recs = append(recs, Record{"Bester Monat", fmtKm(v), MonthLabel(k), ""})
	}
	if k, v := bestI(monthCount); k != "" {
		recs = append(recs, Record{"Meiste Touren in einem Monat", strconv.Itoa(v), MonthLabel(k), ""})
	}
	if k, v := bestI(monthElev); k != "" && v > 0 {
		recs = append(recs, Record{"Meiste Höhenmeter in einem Monat", FmtInt(v) + " m", MonthLabel(k), ""})
	}
	bestYear, bestYearKm := 0, 0.0
	for y, v := range yearKm {
		if v > bestYearKm || (v == bestYearKm && y > bestYear) {
			bestYear, bestYearKm = y, v
		}
	}
	if bestYear != 0 {
		recs = append(recs, Record{"Bestes Jahr", fmtKm(bestYearKm), strconv.Itoa(bestYear), ""})
	}
	return recs
}

// bestF/bestI pick the largest value; ties go to the later key.
func bestF(m map[string]float64) (string, float64) {
	k, v := "", 0.0
	for kk, vv := range m {
		if vv > v || (vv == v && kk > k) {
			k, v = kk, vv
		}
	}
	return k, v
}

func bestI(m map[string]int) (string, int) {
	k, v := "", 0
	for kk, vv := range m {
		if vv > v || (vv == v && kk > k) {
			k, v = kk, vv
		}
	}
	return k, v
}

// Heatmap returns the days of year, starting on the Monday of the first week
// (padding cells have Level -1). Levels split the non-zero days in quartiles.
func Heatmap(ts []Tour, year int) []HeatCell {
	daily := map[string]float64{}
	prefix := strconv.Itoa(year)
	for _, t := range ts {
		if t.Date[:4] == prefix {
			daily[t.Date] += t.DistanceKm
		}
	}
	var vals []float64
	for _, v := range daily {
		vals = append(vals, v)
	}
	sort.Float64s(vals)
	q := func(p float64) float64 {
		if len(vals) == 0 {
			return 0
		}
		return vals[int(p*float64(len(vals)-1))]
	}
	q1, q2, q3 := q(0.25), q(0.5), q(0.75)
	first := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	pad := (int(first.Weekday()) + 6) % 7 // Monday = 0
	cells := make([]HeatCell, 0, pad+366)
	for range pad {
		cells = append(cells, HeatCell{Level: -1})
	}
	for d := first; d.Year() == year; d = d.AddDate(0, 0, 1) {
		ds := d.Format(time.DateOnly)
		km := daily[ds]
		lvl := 0
		switch {
		case km <= 0:
		case km <= q1:
			lvl = 1
		case km <= q2:
			lvl = 2
		case km <= q3:
			lvl = 3
		default:
			lvl = 4
		}
		cells = append(cells, HeatCell{Date: ds, Km: km, Level: lvl})
	}
	return cells
}

// Cumulative returns cumulative km for each day of year up to the last day
// (until "" = whole year; otherwise the series ends at until).
func Cumulative(ts []Tour, year int, until string) []float64 {
	first := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	days := first.AddDate(1, 0, 0).Sub(first).Hours() / 24
	daily := make([]float64, int(days))
	prefix := strconv.Itoa(year)
	for _, t := range ts {
		if t.Date[:4] != prefix {
			continue
		}
		d, _ := time.Parse(time.DateOnly, t.Date)
		daily[d.YearDay()-1] += t.DistanceKm
	}
	end := len(daily)
	if until != "" && until[:4] == prefix {
		d, _ := time.Parse(time.DateOnly, until)
		end = d.YearDay()
	}
	out := make([]float64, end)
	sum := 0.0
	for i := range end {
		sum += daily[i]
		out[i] = math.Round(sum*10) / 10
	}
	return out
}

// MonthLabel turns "2024-03" into "März 2024".
func MonthLabel(k string) string {
	y, m := ym(k + "-01")
	return MonthNames[m-1] + " " + strconv.Itoa(y)
}

// DayLabel turns "2024-03-07" into "7. März 2024".
func DayLabel(d string) string {
	y, m := ym(d)
	day, _ := strconv.Atoi(d[8:10])
	return strconv.Itoa(day) + ". " + MonthNames[m-1] + " " + strconv.Itoa(y)
}

func fmtKm(v float64) string { return FmtInt(int(math.Round(v))) + " km" }

// FmtInt formats with Swiss apostrophes: 12'345.
func FmtInt(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.Itoa(n)
	var out []byte
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '\'')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
