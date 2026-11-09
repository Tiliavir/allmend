// Package ausgaben shares expenses within a group of family members
// ("Ausgaben"): who paid how much for whom, the balance per person and
// currency, payments that settle it, monthly recurring expenses and a
// one-time import of a Splitwise CSV export.
//
// Only members of a group see and edit it. Amounts are int64 minor units;
// currencies are never converted.
package ausgaben

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Kind of an entry.
type Kind string

// Entry kinds.
const (
	Expense Kind = "ausgabe" // someone paid for the shares of others
	Payment Kind = "zahlung" // someone paid someone back
)

// Group is a set of members who share expenses ("Gruppe").
type Group struct {
	ID        string
	Name      string
	Members   []string // account ids in display order; fixed after creation
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsMember reports whether account id belongs to g.
func (g Group) IsMember(id string) bool { return slices.Contains(g.Members, id) }

// Share is what one member owes of an entry.
type Share struct {
	AccountID string
	Amount    int64
}

// Entry is an expense or a payment.
type Entry struct {
	ID          string
	GroupID     string
	Kind        Kind
	Date        string // YYYY-MM-DD
	Description string
	Category    string // free text; empty for payments
	Amount      int64
	Currency    string // ISO 4217
	// PaidBy is the member who paid. Empty only for imported expenses that
	// Splitwise records without effect on anyone's balance ("nicht geteilt");
	// the export does not say who paid those.
	PaidBy string
	// Shares in member order; they add up to Amount. A payment has exactly
	// one share: the receiver. Empty iff PaidBy is empty.
	Shares      []Share
	RecurringID string // set when booked from a recurring expense
	Version     int
	CreatedBy   string
	UpdatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ShareOf returns what account id owes of e.
func (e Entry) ShareOf(id string) int64 {
	for _, s := range e.Shares {
		if s.AccountID == id {
			return s.Amount
		}
	}
	return 0
}

// Receiver is the member a payment went to ("" for expenses).
func (e Entry) Receiver() string {
	if e.Kind != Payment || len(e.Shares) != 1 {
		return ""
	}
	return e.Shares[0].AccountID
}

// Recurring books the same expense every month.
type Recurring struct {
	ID          string
	GroupID     string
	Description string
	Category    string
	Amount      int64
	Currency    string
	PaidBy      string
	Shares      []Share
	Day         int    // day of month 1–31; shorter months use their last day
	Next        string // date of the next booking, YYYY-MM-DD
	Version     int
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Errors.
var (
	ErrNotFound = errors.New("ausgaben: not found")
	ErrConflict = errors.New("ausgaben: changed meanwhile")
	ErrNotEmpty = errors.New("ausgaben: group already has entries")
)

// Limits.
const (
	MaxAmount     = 100_000_000_00 // 100 million in major units
	MaxNameLen    = 60
	MaxDescLen    = 120
	MaxCatLen     = 60
	MinMembers    = 2
	MaxMembers    = 20
	maxCatchUpMon = 24 // a recurring expense books at most this many months at once
)

// Problems lists what is wrong with user input (German).
type Problems []string

func (p *Problems) add(cond bool, msg string) {
	if cond {
		*p = append(*p, msg)
	}
}

// Err returns nil or an error carrying the problems.
func (p Problems) Err() error {
	if len(p) == 0 {
		return nil
	}
	return &InvalidError{p}
}

// InvalidError lists input problems.
type InvalidError struct{ Problems Problems }

func (e *InvalidError) Error() string { return "ausgaben: invalid: " + strings.Join(e.Problems, "; ") }

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// ValidCurrency reports whether c looks like an ISO 4217 code.
func ValidCurrency(c string) bool { return currencyRe.MatchString(c) }

// ValidDate reports whether s is a date YYYY-MM-DD.
func ValidDate(s string) bool {
	if len(s) != 10 {
		return false
	}
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

func runes(s string) int { return len([]rune(s)) }

func checkGroupName(p *Problems, name string) {
	p.add(strings.TrimSpace(name) == "" || runes(name) > MaxNameLen || name != strings.TrimSpace(name), "Name fehlt oder ist zu lang")
}

// checkShares validates that shares belong to members, are unique, not
// negative and add up to amount.
func checkShares(p *Problems, g Group, amount int64, shares []Share) {
	seen := map[string]bool{}
	var sum int64
	for _, s := range shares {
		p.add(!g.IsMember(s.AccountID), "Ein Anteil gehört niemandem aus der Gruppe")
		p.add(seen[s.AccountID], "Eine Person hat zwei Anteile")
		p.add(s.Amount < 0, "Anteile dürfen nicht negativ sein")
		seen[s.AccountID] = true
		sum += s.Amount
	}
	p.add(sum != amount, "Die Anteile ergeben nicht den Betrag")
}

func checkCommon(p *Problems, desc, cat string, amount int64, currency string) {
	p.add(runes(desc) > MaxDescLen || desc != strings.TrimSpace(desc), "Beschreibung zu lang")
	p.add(runes(cat) > MaxCatLen || cat != strings.TrimSpace(cat), "Kategorie zu lang")
	p.add(amount <= 0 || amount > MaxAmount, "Betrag muss grösser als 0 sein")
	p.add(!ValidCurrency(currency), "Währung ungültig (drei Grossbuchstaben, z. B. EUR)")
}

// CheckEntry validates an entry of group g.
func CheckEntry(g Group, e Entry) error {
	var p Problems
	p.add(e.GroupID != g.ID, "Falsche Gruppe")
	p.add(!ValidDate(e.Date), "Datum fehlt oder ist ungültig")
	checkCommon(&p, e.Description, e.Category, e.Amount, e.Currency)
	switch e.Kind {
	case Expense:
		p.add(e.Description == "", "Beschreibung fehlt")
		if e.PaidBy == "" {
			p.add(len(e.Shares) != 0, "Ohne Zahlende keine Anteile")
			break
		}
		p.add(!g.IsMember(e.PaidBy), "Bezahlt hat niemand aus der Gruppe")
		p.add(len(e.Shares) == 0, "Für wen? Mindestens eine Person wählen")
		checkShares(&p, g, e.Amount, e.Shares)
	case Payment:
		p.add(e.Category != "", "Zahlungen haben keine Kategorie")
		p.add(!g.IsMember(e.PaidBy), "Bezahlt hat niemand aus der Gruppe")
		p.add(len(e.Shares) != 1 || e.Shares[0].AccountID == e.PaidBy, "Eine Zahlung geht an genau eine andere Person")
		checkShares(&p, g, e.Amount, e.Shares)
	default:
		p.add(true, "Unbekannte Art")
	}
	return p.Err()
}

// CheckRecurring validates a recurring expense of group g.
func CheckRecurring(g Group, r Recurring) error {
	var p Problems
	p.add(r.GroupID != g.ID, "Falsche Gruppe")
	p.add(r.Description == "", "Beschreibung fehlt")
	checkCommon(&p, r.Description, r.Category, r.Amount, r.Currency)
	p.add(!g.IsMember(r.PaidBy), "Bezahlt hat niemand aus der Gruppe")
	p.add(len(r.Shares) == 0, "Für wen? Mindestens eine Person wählen")
	checkShares(&p, g, r.Amount, r.Shares)
	p.add(r.Day < 1 || r.Day > 31, "Tag im Monat muss zwischen 1 und 31 liegen")
	p.add(!ValidDate(r.Next), "Nächstes Datum fehlt oder ist ungültig")
	if ValidDate(r.Next) && r.Day >= 1 && r.Day <= 31 {
		p.add(Clamp(r.Next[:7], r.Day) != r.Next, "Nächstes Datum passt nicht zum Tag im Monat")
	}
	return p.Err()
}
