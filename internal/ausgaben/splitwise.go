package ausgaben

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Splitwise export ("Export as spreadsheet"), one group:
//
//	Date,Description,Category,Cost,Currency,<Person A>,<Person B>,…
//	(blank line)
//	2025-01-03,Wochenmarkt,Groceries,30.00,EUR,15.00,-15.00
//	…
//	(blank line)
//	2025-02-01,Total balance, , ,EUR,15.00,-15.00
//
// A person's column is their net effect: what they paid minus their share.
// Category "Payment" marks a payment between two people. Rows where every
// net is zero had no effect on anyone (Splitwise does not export who paid).

const maxImportRows = 100_000

var (
	swAmountRe = regexp.MustCompile(`^\d{1,10}\.\d{2}$`)
	swNetRe    = regexp.MustCompile(`^-?\d{1,10}\.\d{2}$`)
)

func swCents(s string, re *regexp.Regexp) (int64, bool) {
	if !re.MatchString(s) {
		return 0, false
	}
	neg := strings.HasPrefix(s, "-")
	whole, frac, _ := strings.Cut(strings.TrimPrefix(s, "-"), ".")
	w, err1 := strconv.ParseInt(whole, 10, 64)
	f, err2 := strconv.ParseInt(frac, 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	c := w*100 + f
	if neg {
		c = -c
	}
	return c, true
}

// ParseSplitwise reads a Splitwise group export into entries of g (without
// ID, version and timestamps). names maps each person column of the file to a
// member of g; every column must be mapped and every member must have a
// column. The "Total balance" rows must match the computed balances. Any
// unexpected row aborts the whole import with all problems listed by line.
func ParseSplitwise(r io.Reader, g Group, names map[string]string) ([]Entry, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, (&Problems{"Datei ist leer oder kein CSV"}).Err()
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	var p Problems
	fixed := []string{"Date", "Description", "Category", "Cost", "Currency"}
	if len(header) < len(fixed)+MinMembers || !slices.Equal(header[:len(fixed)], fixed) {
		return nil, (&Problems{"Kopfzeile passt nicht zu einem Splitwise-Export (Date,Description,Category,Cost,Currency,Personen…)"}).Err()
	}
	cols := header[len(fixed):]
	ids := make([]string, len(cols))
	for i, c := range cols {
		id, ok := names[c]
		switch {
		case !ok:
			p.add(true, fmt.Sprintf("Spalte „%s“ ist niemandem zugeordnet", c))
		case !g.IsMember(id):
			p.add(true, fmt.Sprintf("Spalte „%s“ ist niemandem aus der Gruppe zugeordnet", c))
		case slices.Contains(ids, id):
			p.add(true, fmt.Sprintf("Spalte „%s“: diese Person hat schon eine Spalte", c))
		}
		ids[i] = id
	}
	for _, m := range g.Members {
		p.add(!slices.Contains(ids, m), "Eine Person der Gruppe hat keine Spalte in der Datei")
	}
	if len(p) > 0 {
		return nil, p.Err()
	}

	var out []Entry
	computed := map[string][]int64{}
	totals := map[string][]int64{}
	for {
		rec, err := cr.Read() // skips the blank lines between Splitwise's blocks
		if errors.Is(err, io.EOF) {
			break
		}
		var pe *csv.ParseError
		if errors.As(err, &pe) {
			return nil, (&Problems{fmt.Sprintf("Zeile %d: kein gültiges CSV", pe.Line)}).Err()
		}
		if err != nil {
			return nil, err
		}
		line, _ := cr.FieldPos(0)
		if len(out) >= maxImportRows {
			return nil, (&Problems{"Zu viele Zeilen"}).Err()
		}
		bad := func(msg string) { p.add(true, fmt.Sprintf("Zeile %d: %s", line, msg)) }
		if len(rec) != len(header) {
			bad(fmt.Sprintf("%d statt %d Felder", len(rec), len(header)))
			continue
		}
		cur := rec[4]
		if !ValidCurrency(cur) {
			bad(fmt.Sprintf("Währung „%s“ ungültig", cur))
			continue
		}
		nets := make([]int64, len(ids))
		ok := true
		for i, s := range rec[len(fixed):] {
			if nets[i], ok = swCents(s, swNetRe); !ok {
				bad(fmt.Sprintf("Betrag „%s“ in Spalte „%s“ ungültig", s, cols[i]))
				break
			}
		}
		if !ok {
			continue
		}
		if rec[1] == "Total balance" {
			if _, dup := totals[cur]; dup {
				bad("Saldo für " + cur + " doppelt")
			}
			totals[cur] = nets
			continue
		}
		if len(totals) > 0 {
			bad("Buchung nach dem Saldo")
			continue
		}
		e, msg := swEntry(g, rec, ids, nets)
		if msg != "" {
			bad(msg)
			continue
		}
		if computed[cur] == nil {
			computed[cur] = make([]int64, len(ids))
		}
		for i, n := range nets {
			computed[cur][i] += n
		}
		out = append(out, e)
	}
	if len(p) > 0 {
		return nil, p.Err() // balances would only repeat the row problems
	}
	if len(totals) == 0 {
		p.add(true, "Die Saldo-Zeilen („Total balance“) fehlen – ist die Datei vollständig?")
	}
	curs := map[string]bool{}
	for c := range computed {
		curs[c] = true
	}
	for c := range totals {
		curs[c] = true
	}
	for c := range curs {
		want, have := totals[c], computed[c]
		if want == nil {
			want = make([]int64, len(ids))
		}
		if have == nil {
			have = make([]int64, len(ids))
		}
		p.add(len(totals) > 0 && !slices.Equal(want, have), "Saldo in "+c+" stimmt nicht mit den Buchungen überein – ist die Datei vollständig?")
	}
	if len(out) == 0 && len(p) == 0 {
		p.add(true, "Keine Buchungen in der Datei")
	}
	if len(p) > 0 {
		return nil, p.Err()
	}
	return out, nil
}

// swEntry converts one booking row; msg says what is wrong.
func swEntry(g Group, rec, ids []string, nets []int64) (Entry, string) {
	e := Entry{GroupID: g.ID, Date: rec[0], Description: strings.TrimSpace(rec[1]), Category: strings.TrimSpace(rec[2]), Currency: rec[4], Kind: Expense}
	if !ValidDate(e.Date) {
		return e, fmt.Sprintf("Datum „%s“ ungültig", rec[0])
	}
	cost, ok := swCents(rec[3], swAmountRe)
	if !ok || cost == 0 {
		return e, fmt.Sprintf("Betrag „%s“ ungültig", rec[3])
	}
	e.Amount = cost
	if e.Description == "" {
		return e, "Beschreibung fehlt"
	}
	var sum int64
	payer, receivers := -1, 0
	for i, n := range nets {
		sum += n
		if n > 0 {
			if payer >= 0 {
				return e, "mehrere Personen haben bezahlt – das wird nicht unterstützt"
			}
			payer = i
		}
		if n < 0 {
			receivers++
		}
	}
	if sum != 0 {
		return e, "die Personen-Spalten ergeben nicht null"
	}
	if e.Category == "Payment" {
		if payer < 0 || receivers != 1 || nets[payer] != cost {
			return e, "Zahlung passt nicht zum Betrag"
		}
		e.Kind, e.Category, e.PaidBy = Payment, "", ids[payer]
		for i, n := range nets {
			if n < 0 {
				e.Shares = []Share{{ids[i], cost}}
			}
		}
		return e, ""
	}
	if e.Category == "" {
		return e, "Kategorie fehlt"
	}
	if payer < 0 {
		return e, "" // nobody affected: nicht geteilt
	}
	e.PaidBy = ids[payer]
	if cost-nets[payer] < 0 {
		return e, "Anteil der zahlenden Person wäre negativ"
	}
	for _, m := range g.Members {
		i := slices.Index(ids, m)
		owed := -nets[i]
		if i == payer {
			owed = cost - nets[i]
		}
		if owed > 0 {
			e.Shares = append(e.Shares, Share{m, owed})
		}
	}
	return e, ""
}
