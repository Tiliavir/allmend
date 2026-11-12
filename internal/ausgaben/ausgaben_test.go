package ausgaben

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

var grp = Group{ID: "g", Members: []string{"anna", "ben", "mia"}}

func TestEqual(t *testing.T) {
	for _, c := range []struct {
		amount int64
		payer  string
		who    []string
		want   []Share
	}{
		{1839, "anna", []string{"ben", "anna"}, []Share{{"anna", 919}, {"ben", 920}}},
		{1839, "ben", []string{"anna", "ben"}, []Share{{"anna", 920}, {"ben", 919}}},
		{100, "anna", []string{"anna", "ben", "mia"}, []Share{{"anna", 33}, {"ben", 34}, {"mia", 33}}},
		{101, "anna", []string{"anna", "ben", "mia"}, []Share{{"anna", 33}, {"ben", 34}, {"mia", 34}}},
		{2, "ben", []string{"ben"}, []Share{{"ben", 2}}},
		{5, "anna", []string{"ben", "mia"}, []Share{{"ben", 3}, {"mia", 2}}},
	} {
		got := Equal(grp, c.amount, c.payer, c.who)
		if !slices.Equal(got, c.want) {
			t.Errorf("Equal(%d, %s, %v) = %v, want %v", c.amount, c.payer, c.who, got, c.want)
		}
		if !IsEqual(grp, c.amount, c.payer, got) {
			t.Errorf("IsEqual false for %v", got)
		}
	}
	if Equal(grp, 100, "anna", []string{"nobody"}) != nil {
		t.Error("shares for non-members")
	}
	if IsEqual(grp, 100, "anna", []Share{{"anna", 40}, {"ben", 60}}) {
		t.Error("unequal shares reported as equal")
	}
}

func TestBalancesAndSettle(t *testing.T) {
	es := []Entry{
		{Kind: Expense, Amount: 9000, Currency: "EUR", PaidBy: "anna", Shares: Equal(grp, 9000, "anna", grp.Members)},
		{Kind: Expense, Amount: 3000, Currency: "EUR", PaidBy: "ben", Shares: []Share{{"ben", 1000}, {"mia", 2000}}},
		{Kind: Payment, Amount: 500, Currency: "EUR", PaidBy: "mia", Shares: []Share{{"anna", 500}}},
		{Kind: Expense, Amount: 1000, Currency: "CHF", PaidBy: "mia", Shares: []Share{{"ben", 1000}}},
		{Kind: Expense, Amount: 499, Currency: "EUR"}, // nicht geteilt
	}
	bs := Balances(grp, es)
	if len(bs) != 2 || bs[0].Currency != "CHF" || bs[1].Currency != "EUR" {
		t.Fatalf("currencies: %+v", bs)
	}
	// EUR: anna +9000-3000-500 = 5500; ben +3000-3000-1000 = -1000; mia -3000-2000+500 = -4500
	want := []Net{{"anna", 5500}, {"ben", -1000}, {"mia", -4500}}
	if !slices.Equal(bs[1].Nets, want) {
		t.Fatalf("EUR nets %v, want %v", bs[1].Nets, want)
	}
	tr := Settle(bs[1])
	wantTr := []Transfer{{"mia", "anna", 4500, "EUR"}, {"ben", "anna", 1000, "EUR"}}
	if !slices.Equal(tr, wantTr) {
		t.Fatalf("settle %v, want %v", tr, wantTr)
	}
	// Booking the proposals settles everything.
	for _, x := range tr {
		es = append(es, Entry{Kind: Payment, Amount: x.Amount, Currency: x.Currency, PaidBy: x.From, Shares: []Share{{x.To, x.Amount}}})
	}
	if !Balances(grp, es)[1].Settled() {
		t.Error("not settled after transfers")
	}
	if Balances(grp, es)[0].Settled() {
		t.Error("CHF settled without payment")
	}
}

func TestRecurringDates(t *testing.T) {
	if got := Clamp("2024-02", 31); got != "2024-02-29" {
		t.Errorf("leap Feb: %s", got)
	}
	if got := Clamp("2025-02", 30); got != "2025-02-28" {
		t.Errorf("Feb: %s", got)
	}
	if got := After("2025-01-31", 31); got != "2025-02-28" {
		t.Errorf("after Jan 31: %s", got)
	}
	if got := After("2025-02-28", 31); got != "2025-03-31" {
		t.Errorf("after clamped Feb: %s", got)
	}
	if got := After("2025-12-15", 15); got != "2026-01-15" {
		t.Errorf("year end: %s", got)
	}
	r := Recurring{Day: 30, Next: "2025-12-30"}
	dates, next := Due(r, "2026-03-01")
	if !slices.Equal(dates, []string{"2025-12-30", "2026-01-30", "2026-02-28"}) || next != "2026-03-30" {
		t.Errorf("due %v next %s", dates, next)
	}
	if d, n := Due(r, "2025-12-29"); d != nil || n != r.Next {
		t.Errorf("not yet due: %v %s", d, n)
	}
	r = Recurring{Day: 1, Next: "2020-01-01"}
	if d, _ := Due(r, "2026-01-01"); len(d) != maxCatchUpMon {
		t.Errorf("catch-up capped: %d", len(d))
	}
}

func TestCheckEntry(t *testing.T) {
	ok := Entry{GroupID: "g", Kind: Expense, Date: "2026-01-02", Description: "Markt", Category: "groceries", Amount: 1000, Currency: "EUR", PaidBy: "anna", Shares: []Share{{"anna", 500}, {"ben", 500}}}
	if err := CheckEntry(grp, ok); err != nil {
		t.Fatal(err)
	}
	pay := Entry{GroupID: "g", Kind: Payment, Date: "2026-01-02", Amount: 1000, Currency: "CHF", PaidBy: "ben", Shares: []Share{{"anna", 1000}}}
	if err := CheckEntry(grp, pay); err != nil {
		t.Fatal(err)
	}
	unsplit := Entry{GroupID: "g", Kind: Expense, Date: "2026-01-02", Description: "Abo", Category: "music", Amount: 499, Currency: "EUR"}
	if err := CheckEntry(grp, unsplit); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(e *Entry){
		"sum":        func(e *Entry) { e.Shares[0].Amount = 400 },
		"negative":   func(e *Entry) { e.Shares = []Share{{"anna", 1500}, {"ben", -500}} },
		"stranger":   func(e *Entry) { e.Shares[1].AccountID = "kim" },
		"duplicate":  func(e *Entry) { e.Shares[1].AccountID = "anna" },
		"payer":      func(e *Entry) { e.PaidBy = "kim" },
		"no shares":  func(e *Entry) { e.Shares = nil },
		"date":       func(e *Entry) { e.Date = "2026-02-30" },
		"currency":   func(e *Entry) { e.Currency = "eur" },
		"zero":       func(e *Entry) { e.Amount, e.Shares = 0, []Share{{"anna", 0}} },
		"desc":       func(e *Entry) { e.Description = "" },
		"desc space": func(e *Entry) { e.Description = " Markt" },
		"kind":       func(e *Entry) { e.Kind = "geschenk" },
		"group":      func(e *Entry) { e.GroupID = "x" },
		"long":       func(e *Entry) { e.Description = strings.Repeat("x", MaxDescLen+1) },
		"self pay":   func(e *Entry) { e.Kind, e.Description, e.Shares = Payment, "", []Share{{"anna", 1000}} },
		"pay two":    func(e *Entry) { e.Kind, e.Description = Payment, "" },
		"pay cat":    func(e *Entry) { e.Kind, e.Category, e.Shares = Payment, "groceries", []Share{{"ben", 1000}} },
		"no cat":     func(e *Entry) { e.Category = "" },
		"free cat":   func(e *Entry) { e.Category = "Essen" },
		"unsplit sh": func(e *Entry) { e.PaidBy = "" },
	}
	for name, mod := range bad {
		e := ok
		e.Shares = slices.Clone(ok.Shares)
		mod(&e)
		var inv *InvalidError
		if err := CheckEntry(grp, e); !errors.As(err, &inv) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
}

func TestCheckRecurring(t *testing.T) {
	r := Recurring{GroupID: "g", Description: "Miete", Category: "rent", Amount: 120000, Currency: "CHF", PaidBy: "anna", Shares: Equal(grp, 120000, "anna", []string{"anna", "ben"}), Day: 31, Next: "2026-02-28"}
	if err := CheckRecurring(grp, r); err != nil {
		t.Fatal(err)
	}
	r.Next = "2026-02-27"
	if CheckRecurring(grp, r) == nil {
		t.Error("next date not matching the day accepted")
	}
	r.Next, r.Day = "2026-02-28", 0
	if CheckRecurring(grp, r) == nil {
		t.Error("day 0 accepted")
	}
	r.Day, r.Category = 28, "Wohnen"
	if CheckRecurring(grp, r) == nil {
		t.Error("free-text category accepted")
	}
}

// A made-up export in Splitwise's format (no real data).
const swFile = "\ufeffDate,Description,Category,Cost,Currency,Anna Muster,Ben Muster,Mia Muster\n" +
	"\n" +
	"2025-01-03,Wochenmarkt ,Groceries,30.00,EUR,20.00,-10.00,-10.00\n" +
	"2025-01-05,Strom,Electricity,18.39,EUR,-9.20,9.20,0.00\n" +
	"2025-01-07,Velo,Sports,96.04,EUR,36.04,-36.04,0.00\n" +
	"2025-01-08,Abo,Music,4.99,EUR,0.00,0.00,0.00\n" +
	"2025-01-10,Ben M. paid Anna M.,Payment,10.00,EUR,-10.00,10.00,0.00\n" +
	"2025-01-11,Bergbahn,Entertainment,49.00,CHF,-24.50,0.00,24.50\n" +
	"\n" +
	"2025-02-01,Total balance, , ,EUR,36.84,-26.84,-10.00\n" +
	"2025-02-01,Total balance, , ,CHF,-24.50,0.00,24.50\n"

var swNames = map[string]string{"Anna Muster": "anna", "Ben Muster": "ben", "Mia Muster": "mia"}

func TestParseSplitwise(t *testing.T) {
	es, err := ParseSplitwise(strings.NewReader(swFile), grp, swNames)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 6 {
		t.Fatalf("want 6 entries, got %d", len(es))
	}
	for _, e := range es {
		if err := CheckEntry(grp, e); err != nil {
			t.Errorf("%s: %v", e.Description, err)
		}
	}
	m := es[0]
	if m.Description != "Wochenmarkt" || m.PaidBy != "anna" || !slices.Equal(m.Shares, []Share{{"anna", 1000}, {"ben", 1000}, {"mia", 1000}}) {
		t.Errorf("market: %+v", m)
	}
	if s := es[1]; s.PaidBy != "ben" || !slices.Equal(s.Shares, []Share{{"anna", 920}, {"ben", 919}}) {
		t.Errorf("rounding: %+v", s)
	}
	if v := es[2]; v.PaidBy != "anna" || !slices.Equal(v.Shares, []Share{{"anna", 6000}, {"ben", 3604}}) {
		t.Errorf("unequal split: %+v", v)
	}
	if a := es[3]; a.PaidBy != "" || a.Shares != nil || a.Kind != Expense {
		t.Errorf("not shared: %+v", a)
	}
	if cats := []string{es[0].Category, es[1].Category, es[2].Category, es[3].Category, es[5].Category}; !slices.Equal(cats, []string{"groceries", "electricity", "sports", "music", "entertainment_other"}) {
		t.Errorf("categories %v", cats)
	}
	if p := es[4]; p.Kind != Payment || p.PaidBy != "ben" || p.Receiver() != "anna" || p.Category != "" {
		t.Errorf("payment: %+v", p)
	}
	bs := Balances(grp, es)
	if !slices.Equal(bs[1].Nets, []Net{{"anna", 3684}, {"ben", -2684}, {"mia", -1000}}) {
		t.Errorf("EUR balance %v", bs[1].Nets)
	}
}

func TestParseSplitwiseRejects(t *testing.T) {
	cases := map[string]struct {
		file  string
		names map[string]string
		want  string
	}{
		"header":       {"Datum,Text\n", swNames, "Kopfzeile"},
		"unmapped":     {swFile, map[string]string{"Anna Muster": "anna", "Ben Muster": "ben"}, "Mia Muster"},
		"stranger":     {swFile, map[string]string{"Anna Muster": "anna", "Ben Muster": "ben", "Mia Muster": "kim"}, "niemandem aus der Gruppe"},
		"twice":        {swFile, map[string]string{"Anna Muster": "anna", "Ben Muster": "anna", "Mia Muster": "mia"}, "schon eine Spalte"},
		"truncated":    {strings.Replace(swFile, "2025-01-11,Bergbahn,Entertainment,49.00,CHF,-24.50,0.00,24.50\n", "", 1), swNames, "Saldo in CHF"},
		"no totals":    {strings.Split(swFile, "\n\n2025-02-01")[0] + "\n", swNames, "fehlen"},
		"two payers":   {strings.Replace(swFile, "30.00,EUR,20.00,-10.00,-10.00", "30.00,EUR,10.00,10.00,-20.00", 1), swNames, "Zeile 3: mehrere Personen"},
		"not zero":     {strings.Replace(swFile, "20.00,-10.00,-10.00", "20.00,-10.00,-9.00", 1), swNames, "nicht null"},
		"bad amount":   {strings.Replace(swFile, "30.00,EUR", "30,EUR", 1), swNames, "Zeile 3"},
		"bad date":     {strings.Replace(swFile, "2025-01-03", "03.01.2025", 1), swNames, "Datum"},
		"bad currency": {strings.Replace(swFile, "49.00,CHF,-24.50", "49.00,Fr,-24.50", 1), swNames, "Währung"},
		"payment":      {strings.Replace(swFile, "Payment,10.00", "Payment,12.00", 1), swNames, "Zahlung"},
		"after total":  {swFile + "2025-02-02,Brot,Groceries,3.00,EUR,1.50,-1.50,0.00\n", swNames, "nach dem Saldo"},
		"fields":       {strings.Replace(swFile, "-10.00,-10.00\n", "-10.00\n", 1), swNames, "Felder"},
		"no category":  {strings.Replace(swFile, "Strom,Electricity", "Strom,", 1), swNames, "Kategorie"},
		"unknown cat":  {strings.Replace(swFile, "Strom,Electricity", "Strom,Energie", 1), swNames, "Kategorie „Energie“ unbekannt"},
	}
	for name, c := range cases {
		_, err := ParseSplitwise(strings.NewReader(c.file), grp, c.names)
		var inv *InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %q does not mention %q", name, err, c.want)
		}
	}
}

func TestCategories(t *testing.T) {
	n := 0
	for _, m := range Mains {
		for _, s := range m.Subs {
			n++
			if got, ok := MainOf(s.Key); !ok || got.Key != m.Key {
				t.Errorf("%s: main %v", s.Key, got.Key)
			}
			if got, ok := FromSplitwise(s.Splitwise); !ok || got.Key != s.Key {
				t.Errorf("%s: splitwise %q", s.Key, s.Splitwise)
			}
		}
	}
	if n != 43 {
		t.Errorf("%d subcategories, want 43", n)
	}
	if _, ok := Category("Essen"); ok {
		t.Error("free text accepted as category")
	}
	if s, _ := FromSplitwise("Uncategorized"); s.Key != "general" {
		t.Errorf("Uncategorized -> %q", s.Key)
	}
}
