package velo

import (
	"testing"
)

func tours() []Tour {
	return []Tour{
		{ID: "a", BikeID: "g", Date: "2025-03-01", DistanceKm: 40, ElevationM: 300},
		{ID: "b", BikeID: "r", Date: "2025-03-15", DistanceKm: 100, ElevationM: 1200},
		{ID: "c", BikeID: "g", Date: "2026-01-05", DistanceKm: 20, ElevationM: 50},
		{ID: "d", BikeID: "g", Date: "2026-01-05", DistanceKm: 10},
	}
}

func TestSummaries(t *testing.T) {
	rows := YearRows(tours())
	if len(rows) != 2 || rows[0].Year != 2026 || rows[0].Km != 30 || rows[1].Months[2] != 140 || rows[1].Tours != 2 {
		t.Fatalf("rows %+v", rows)
	}
	if s := Summarize(tours(), 2025, "2025-03-10"); s.Km != 40 || s.Tours != 1 {
		t.Errorf("until: %+v", s)
	}
	if f := Filter(tours(), "r"); len(f) != 1 || f[0].ID != "b" {
		t.Errorf("filter %+v", f)
	}
}

func TestRecords(t *testing.T) {
	r := Records(tours())
	want := map[string]string{"Längste Tour": "100 km", "Meiste Höhenmeter": "1'200 m", "Bester Monat": "140 km", "Bestes Jahr": "140 km", "Meiste Touren in einem Monat": "2"}
	for _, rec := range r {
		if w, ok := want[rec.Label]; ok && w != rec.Value {
			t.Errorf("%s = %s, want %s", rec.Label, rec.Value, w)
		}
	}
	if r[0].Detail != "15. März 2025" || r[0].TourID != "b" {
		t.Errorf("longest %+v", r[0])
	}
	// Tie on tour count (Mar 2025 and Jan 2026 both 2): later month wins.
	for _, rec := range r {
		if rec.Label == "Meiste Touren in einem Monat" && rec.Detail != "Januar 2026" {
			t.Errorf("tie: %s", rec.Detail)
		}
	}
}

func TestHeatmapAndCumulative(t *testing.T) {
	h := Heatmap(tours(), 2026) // 2026-01-01 is a Thursday → 3 padding cells
	if h[0].Level != -1 || h[3].Date != "2026-01-01" || len(h) != 3+365 {
		t.Fatalf("heatmap start %+v len %d", h[:4], len(h))
	}
	if h[3+4].Km != 30 || h[3+4].Level == 0 {
		t.Errorf("Jan 5: %+v", h[3+4])
	}
	c := Cumulative(tours(), 2026, "2026-01-10")
	if len(c) != 10 || c[3] != 0 || c[4] != 30 || c[9] != 30 {
		t.Errorf("cumulative %v", c)
	}
	if len(Cumulative(nil, 2024, "")) != 366 {
		t.Error("leap year")
	}
}
