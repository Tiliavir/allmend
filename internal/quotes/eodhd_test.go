package quotes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseEODHDStrict(t *testing.T) {
	t.Parallel()
	ps, err := ParseEODHD([]byte(`[{"date":"2026-11-20","open":1,"close":263.4,"adjusted_close":260,"volume":5},{"date":"2026-11-19","close":262}]`))
	if err != nil || len(ps) != 2 || ps[0].Date != "2026-11-19" || ps[1].Close != 263.4 {
		t.Fatal(ps, err)
	}
	for i, bad := range []string{
		`[]`,
		`{"error":"x"}`,
		`Ticker Not Found.`,
		`[{"date":"2026-11-20"}]`,
		`[{"date":"2026-11-20","close":"263"}]`,
		`[{"date":"20.11.2026","close":1}]`,
		`[{"date":"2026-11-20","close":0}]`,
		`[{"date":"2026-11-20","close":1},{"date":"2026-11-20","close":2}]`,
	} {
		if _, err := ParseEODHD([]byte(bad)); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestEODHDClient(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("api_token") != "k":
			http.Error(w, "Unauthenticated", http.StatusUnauthorized)
		case r.URL.Path == "/api/eod/NONE.SW":
			http.Error(w, "Ticker Not Found.", http.StatusNotFound)
		case r.URL.Path != "/api/eod/RO.SW" || q.Get("fmt") != "json" || q.Get("period") != "d" || q.Get("from") != "2025-11-21":
			http.Error(w, "bad request "+r.URL.String(), http.StatusBadRequest)
		default:
			_, _ = w.Write([]byte(`[{"date":"2026-11-20","close":263.4}]`))
		}
	}))
	defer srv.Close()
	e := &EODHD{BaseURL: srv.URL, Key: "k"}
	ps, err := e.DailySince(context.Background(), "RO.SW", "2025-11-21")
	if err != nil || len(ps) != 1 || ps[0].Close != 263.4 {
		t.Fatal(ps, err)
	}
	if _, err := e.DailySince(context.Background(), "NONE.SW", "2025-11-21"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("unknown ticker: %v", err)
	}
	if _, err := e.DailySince(context.Background(), "RO.SW", "21.11.2025"); err == nil {
		t.Fatal("invalid start date accepted")
	}
	e.Key = "wrong"
	if _, err := e.DailySince(context.Background(), "RO.SW", "2025-11-21"); err == nil || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("key leaked or no error: %v", err)
	}
}
