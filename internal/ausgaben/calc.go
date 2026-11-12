package ausgaben

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

// Equal splits amount equally among the members in for (in group order).
// Remainder units go one each to the first members that did not pay, then
// to the payer — so the payer never carries a rounding cent for others.
func Equal(g Group, amount int64, payer string, forIDs []string) []Share {
	var who []string
	for _, m := range g.Members {
		if slices.Contains(forIDs, m) {
			who = append(who, m)
		}
	}
	if len(who) == 0 {
		return nil
	}
	n := int64(len(who))
	base, rem := amount/n, amount%n
	out := make([]Share, len(who))
	for i, m := range who {
		out[i] = Share{AccountID: m, Amount: base}
	}
	for pass := 0; pass < 2 && rem > 0; pass++ {
		for i := range out {
			if rem == 0 {
				break
			}
			if (out[i].AccountID == payer) == (pass == 1) {
				out[i].Amount++
				rem--
			}
		}
	}
	return out
}

// IsEqual reports whether shares are what Equal gives for the same people.
func IsEqual(g Group, amount int64, payer string, shares []Share) bool {
	ids := make([]string, 0, len(shares))
	for _, s := range shares {
		ids = append(ids, s.AccountID)
	}
	return slices.Equal(Equal(g, amount, payer, ids), shares)
}

// Net is one member's balance: positive means the others owe them.
type Net struct {
	AccountID string
	Amount    int64
}

// Balance is the state of one currency.
type Balance struct {
	Currency string
	Nets     []Net // in member order; they add up to zero
}

// Settled reports whether nobody owes anything.
func (b Balance) Settled() bool {
	for _, n := range b.Nets {
		if n.Amount != 0 {
			return false
		}
	}
	return true
}

// Balances computes per currency (sorted) what each member paid minus what
// they owe.
func Balances(g Group, es []Entry) []Balance {
	by := map[string]map[string]int64{}
	for _, e := range es {
		if e.PaidBy == "" {
			continue
		}
		m := by[e.Currency]
		if m == nil {
			m = map[string]int64{}
			by[e.Currency] = m
		}
		m[e.PaidBy] += e.Amount
		for _, s := range e.Shares {
			m[s.AccountID] -= s.Amount
		}
	}
	out := make([]Balance, 0, len(by))
	for cur, m := range by {
		b := Balance{Currency: cur}
		for _, id := range g.Members {
			b.Nets = append(b.Nets, Net{id, m[id]})
		}
		out = append(out, b)
	}
	slices.SortFunc(out, func(a, b Balance) int { return cmp.Compare(a.Currency, b.Currency) })
	return out
}

// Transfer is a payment that settles (part of) a balance.
type Transfer struct {
	From, To string
	Amount   int64
	Currency string
}

// Settle proposes few payments that bring b to zero: the biggest debtor pays
// the biggest creditor until everyone is even. Ties keep member order.
func Settle(b Balance) []Transfer {
	type acc struct {
		id  string
		amt int64
	}
	var debt, cred []acc
	for _, n := range b.Nets {
		switch {
		case n.Amount < 0:
			debt = append(debt, acc{n.AccountID, -n.Amount})
		case n.Amount > 0:
			cred = append(cred, acc{n.AccountID, n.Amount})
		}
	}
	byAmt := func(a, b acc) int { return cmp.Compare(b.amt, a.amt) }
	var out []Transfer
	for len(debt) > 0 && len(cred) > 0 {
		slices.SortStableFunc(debt, byAmt)
		slices.SortStableFunc(cred, byAmt)
		x := min(debt[0].amt, cred[0].amt)
		out = append(out, Transfer{From: debt[0].id, To: cred[0].id, Amount: x, Currency: b.Currency})
		debt[0].amt -= x
		cred[0].amt -= x
		if debt[0].amt == 0 {
			debt = debt[1:]
		}
		if cred[0].amt == 0 {
			cred = cred[1:]
		}
	}
	return out
}

// Clamp returns the date of day in month ("YYYY-MM"), or the month's last
// day if it is shorter. month must be valid.
func Clamp(month string, day int) string {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		panic(fmt.Sprintf("ausgaben.Clamp: bad month %q", month))
	}
	last := t.AddDate(0, 1, -1).Day()
	return t.AddDate(0, 0, min(day, last)-1).Format(time.DateOnly)
}

// After returns the booking date in the month after date.
func After(date string, day int) string {
	t, err := time.Parse(time.DateOnly, date)
	if err != nil {
		panic(fmt.Sprintf("ausgaben.After: bad date %q", date))
	}
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	return Clamp(first.Format("2006-01"), day)
}

// Due returns the booking dates of r up to and including today and the next
// date after them. It books at most maxCatchUpMon months at once.
func Due(r Recurring, today string) (dates []string, next string) {
	next = r.Next
	for next <= today && len(dates) < maxCatchUpMon {
		dates = append(dates, next)
		next = After(next, r.Day)
	}
	return dates, next
}

// Settled is where a currency was last even: the balance of everyone was
// zero after the first Count entries of that currency (oldest first).
type Settled struct {
	Currency string
	Date     string // date of the entry that evened it
	Count    int    // entries up to and including that one
}

// SplitSettled separates the entries after the last point at which their
// currency's balance was zero for everyone ("open") from those before it.
// es may come in any order; open keeps the order of es. Entries are ordered
// by date, then creation time, then id. Unsplit entries do not change a
// balance; before such a point they count as settled. A currency that was
// never even has no Settled. Nothing is stored: a back-dated entry moves the
// point by itself.
func SplitSettled(es []Entry) (open []Entry, settled []Settled) {
	byCur := map[string][]Entry{}
	for _, e := range es {
		byCur[e.Currency] = append(byCur[e.Currency], e)
	}
	done := map[string]bool{}
	for cur, list := range byCur {
		list = slices.Clone(list)
		slices.SortStableFunc(list, func(a, b Entry) int {
			if c := cmp.Compare(a.Date, b.Date); c != 0 {
				return c
			}
			if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
				return c
			}
			return cmp.Compare(a.ID, b.ID)
		})
		nets := map[string]int64{}
		last := -1
		for i, e := range list {
			if e.PaidBy == "" {
				continue
			}
			nets[e.PaidBy] += e.Amount
			for _, s := range e.Shares {
				nets[s.AccountID] -= s.Amount
			}
			even := true
			for _, n := range nets {
				if n != 0 {
					even = false
					break
				}
			}
			if even {
				last = i
			}
		}
		if last < 0 {
			continue
		}
		settled = append(settled, Settled{Currency: cur, Date: list[last].Date, Count: last + 1})
		for _, e := range list[:last+1] {
			done[e.ID] = true
		}
	}
	slices.SortFunc(settled, func(a, b Settled) int { return cmp.Compare(a.Currency, b.Currency) })
	for _, e := range es {
		if !done[e.ID] {
			open = append(open, e)
		}
	}
	return open, settled
}

// Spending sums the expenses in currency per subcategory key. year ("YYYY")
// limits to one year, "" means all. With viewer set it sums that member's
// shares (unsplit expenses count nothing), otherwise the full amounts.
// Payments never count.
func Spending(es []Entry, currency, year, viewer string) map[string]int64 {
	out := map[string]int64{}
	for _, e := range es {
		if e.Kind != Expense || e.Currency != currency || (year != "" && e.Date[:4] != year) {
			continue
		}
		n := e.Amount
		if viewer != "" {
			n = e.ShareOf(viewer)
		}
		if n != 0 {
			out[e.Category] += n
		}
	}
	return out
}
