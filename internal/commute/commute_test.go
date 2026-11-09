package commute

import "testing"

func f(v float64) *float64 { return &v }

func TestCompute(t *testing.T) {
	y := Year{Year: 2026, CarDistKm: 7.6, TrainDistKm: 15, BikeDistKm: 18}
	y.Months[0] = Month{Workdays: 20, CarTrain: 13, HomeOffice: 7}
	v := Compute(y)
	jan := v.Derived[0]
	if jan.PendlerCents != 10868 { // 13 days × 22 km × 38 ct
		t.Errorf("pendler %d", jan.PendlerCents)
	}
	if jan.KmCar != 13*7.6*2 || !jan.Check || jan.HomeOfficePct != 7.0/20 || !v.AllChecksOK {
		t.Errorf("derived %+v", jan)
	}
}

func TestRatesByYear(t *testing.T) {
	for year, want := range map[int]int64{2020: 660, 2021: 670, 2025: 676, 2026: 836} {
		y := Year{Year: year, CarDistKm: 7, TrainDistKm: 15}
		y.Months[0] = Month{Workdays: 1, CarTrain: 1}
		if got := Compute(y).Derived[0].PendlerCents; got != want {
			t.Errorf("%d: %d, want %d", year, got, want)
		}
	}
}

func TestOverride(t *testing.T) {
	y := Year{Year: 2026, CarDistKm: 7, TrainDistKm: 15, RateLowCents: f(30), RateHighCents: f(38), ThresholdKm: f(10)}
	y.Months[0] = Month{Workdays: 1, CarTrain: 1}
	if got := Compute(y).Derived[0].PendlerCents; got != 756 { // 10×30 + 12×38
		t.Errorf("override %d", got)
	}
}
