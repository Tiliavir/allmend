package quotes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tiliavir/allmend/internal/finanzen"
)

func TestParseDailyStrict(t *testing.T) {
	ps, err := ParseDaily(map[string]map[string]string{"2025-01-03": {"4. close": "101.5"}, "2025-01-02": {"4. close": "100"}})
	if err != nil || len(ps) != 2 || ps[0].Date != "2025-01-02" || ps[1].Close != 101.5 {
		t.Fatal(ps, err)
	}
	for i, bad := range []map[string]map[string]string{
		{},
		{"2025-02-30": {"4. close": "1"}},
		{"2025-01-02": {"1. open": "1"}},
		{"2025-01-02": {"4. close": "abc"}},
		{"2025-01-02": {"4. close": "0"}},
		{"2025-01-02": {"4. close": "-3"}},
	} {
		if _, err := ParseDaily(bad); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func raw(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseETFProfile(t *testing.T) {
	ws, err := ParseETFProfile(raw(t, `{"net_assets":"1","sectors":[{"sector":"INFORMATION TECHNOLOGY","weight":"0.31"},{"sector":"FINANCIALS","weight":0.15}],
	"holdings":[{"symbol":"GOOGL","description":"ALPHABET INC","weight":"0.02"},{"symbol":"GOOG","description":"ALPHABET INC","weight":"0.018"},{"symbol":"X","description":"n/a","weight":"0.01"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if ws[0].Label != "Information Technology" || ws[2].Label != "Alphabet Inc" || ws[2].Weight < 0.0379 || ws[3].Label != "X" {
		t.Fatalf("%+v", ws)
	}
	for i, bad := range []string{
		`{}`,
		`{"sectors":[{"sector":"A","weight":"x"}]}`,
		`{"sectors":[{"sector":"A","weight":"1.5"}]}`,
		`{"sectors":[{"sector":"","weight":"0.5"}]}`,
		`{"sectors":[{"sector":"A","weight":"0.8"},{"sector":"B","weight":"0.8"}]}`,
		`{"sectors":"none"}`,
	} {
		if _, err := ParseETFProfile(raw(t, bad)); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestOverviewAndFrankfurter(t *testing.T) {
	ws, err := ParseOverview(raw(t, `{"Symbol":"IBM","Sector":"TECHNOLOGY","Country":"USA","Currency":"USD"}`))
	if err != nil || len(ws) != 3 || ws[1].Dimension != finanzen.Country {
		t.Fatal(ws, err)
	}
	if _, err := ParseOverview(raw(t, `{"Symbol":"X","Sector":"None"}`)); err == nil {
		t.Error("empty overview accepted")
	}
	r, err := ParseFrankfurter([]byte(`{"amount":1,"base":"USD","date":"2025-01-02","rates":{"EUR":0.95}}`), "USD", "EUR")
	if err != nil || r.Rate != 0.95 || r.Date != "2025-01-02" {
		t.Fatal(r, err)
	}
	for i, bad := range []string{
		`{"base":"CHF","date":"2025-01-02","rates":{"EUR":0.95}}`,
		`{"base":"USD","date":"latest","rates":{"EUR":0.95}}`,
		`{"base":"USD","date":"2025-01-02","rates":{"GBP":0.8}}`,
		`{"base":"USD","date":"2025-01-02","rates":{"EUR":0}}`,
		`not json`,
	} {
		if _, err := ParseFrankfurter([]byte(bad), "USD", "EUR"); err == nil {
			t.Errorf("rate case %d accepted", i)
		}
	}
}

func TestAlphaVantageHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("apikey") != "k":
			http.Error(w, "no", http.StatusUnauthorized)
		case q.Get("symbol") == "LIMIT":
			_, _ = w.Write([]byte(`{"Information":"We have detected your API key ... 25 requests per day."}`))
		default:
			_, _ = w.Write([]byte(`{"Meta Data":{},"Time Series (Daily)":{"2025-01-02":{"1. open":"1","4. close":"2.5"}}}`))
		}
	}))
	defer srv.Close()
	av := &AlphaVantage{BaseURL: srv.URL, Key: "k"}
	ps, err := av.Daily(context.Background(), "VWCE.DEX")
	if err != nil || len(ps) != 1 || ps[0].Close != 2.5 {
		t.Fatal(ps, err)
	}
	if _, err := av.Daily(context.Background(), "LIMIT"); err == nil || !strings.Contains(err.Error(), "25 requests") {
		t.Fatalf("rate limit: %v", err)
	}
	av.Key = "wrong"
	if _, err := av.Daily(context.Background(), "X"); err == nil || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("key leaked or no error: %v", err)
	}
}
