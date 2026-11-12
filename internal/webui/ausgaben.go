package webui

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/ausgaben"
)

// maxSplitwiseUpload limits a Splitwise CSV upload.
const maxSplitwiseUpload = 10 << 20

func (s *Server) ausgabenRoutes(mux *http.ServeMux) {
	m := func(h func(http.ResponseWriter, *http.Request, account.Account)) http.Handler {
		return s.Module(account.Ausgaben, h)
	}
	mux.Handle("GET /ausgaben", m(s.agList))
	mux.Handle("POST /ausgaben", m(s.agCreateGroup))
	mux.Handle("GET /ausgaben/{g}", m(s.agGroup))
	mux.Handle("GET /ausgaben/{g}/bearbeiten", m(s.agEditGroup))
	mux.Handle("POST /ausgaben/{g}/bearbeiten", m(s.agSaveGroup))
	mux.Handle("GET /ausgaben/{g}/archiv", m(s.agArchive))
	mux.Handle("GET /ausgaben/{g}/statistik", m(s.agStats))
	mux.Handle("GET /ausgaben/{g}/neu", m(s.agNew))
	mux.Handle("POST /ausgaben/{g}/eintraege", m(s.agSave))
	mux.Handle("GET /ausgaben/{g}/eintraege/{e}", m(s.agEdit))
	mux.Handle("POST /ausgaben/{g}/eintraege/{e}", m(s.agSave))
	mux.Handle("POST /ausgaben/{g}/eintraege/{e}/loeschen", m(s.agDelete))
	mux.Handle("GET /ausgaben/{g}/wiederkehrend/neu", m(s.agRecNew))
	mux.Handle("POST /ausgaben/{g}/wiederkehrend", m(s.agRecSave))
	mux.Handle("GET /ausgaben/{g}/wiederkehrend/{r}", m(s.agRecEdit))
	mux.Handle("POST /ausgaben/{g}/wiederkehrend/{r}", m(s.agRecSave))
	mux.Handle("POST /ausgaben/{g}/wiederkehrend/{r}/loeschen", m(s.agRecDelete))
	mux.Handle("POST /ausgaben/{g}/import", m(s.agImport))
}

func (s *Server) agPage(w http.ResponseWriter, r *http.Request, status int, name, title, errMsg string, d any) {
	p := Page{Title: title, Module: account.Ausgaben, Error: errMsg, Data: d}
	if name == "entry_form" || name == "recurring_form" {
		p.Scripts = []Script{{Src: "/static/ausgaben.js", Module: true}} // category search
	}
	s.render(w, r, status, "ausgaben/"+name, p)
}

func (s *Server) agErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ausgaben.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	s.fail(w, r, err)
}

func agProblems(err error) string {
	var inv *ausgaben.InvalidError
	if errors.As(err, &inv) {
		return strings.Join(inv.Problems, ". ") + "."
	}
	return ""
}

// agPerson is an account as shown in the module.
type agPerson struct {
	ID, Name string
	Me       bool
}

// agPeople names the members of g (disabled accounts keep their name).
func (s *Server) agPeople(r *http.Request, a account.Account, g ausgaben.Group) ([]agPerson, map[string]string, error) {
	out := make([]agPerson, 0, len(g.Members))
	names := map[string]string{}
	for _, id := range g.Members {
		o, err := s.Accounts.Repo.Get(r.Context(), id)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, agPerson{ID: id, Name: o.DisplayName(), Me: id == a.ID})
		names[id] = o.DisplayName()
	}
	return out, names, nil
}

// ── groups ────────────────────────────────────────────

type agGroupItem struct {
	ausgaben.Group
	People string
}

type agListData struct {
	Groups     []agGroupItem
	Candidates []agPerson // who a can add to a new group
	Name       string
	Chosen     map[string]bool
}

func (s *Server) agList(w http.ResponseWriter, r *http.Request, a account.Account) {
	s.agListPage(w, r, a, http.StatusOK, agListData{}, "")
}

func (s *Server) agListPage(w http.ResponseWriter, r *http.Request, a account.Account, status int, d agListData, errMsg string) {
	gs, err := s.Ausgaben.Repo.Groups(r.Context(), a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, g := range gs {
		people, _, err := s.agPeople(r, a, g)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		names := make([]string, 0, len(people))
		for _, p := range people {
			names = append(names, p.Name)
		}
		d.Groups = append(d.Groups, agGroupItem{g, strings.Join(names, ", ")})
	}
	all, err := s.Accounts.Repo.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, c := range all {
		if c.ID != a.ID && c.Status == account.Active && c.Can(account.Ausgaben) {
			d.Candidates = append(d.Candidates, agPerson{ID: c.ID, Name: c.DisplayName()})
		}
	}
	s.agPage(w, r, status, "list", "Ausgaben", errMsg, d)
}

func (s *Server) agCreateGroup(w http.ResponseWriter, r *http.Request, a account.Account) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	members := r.PostForm["person"]
	g, err := s.Ausgaben.CreateGroup(r.Context(), a, name, members)
	if msg := agProblems(err); msg != "" {
		chosen := map[string]bool{}
		for _, m := range members {
			chosen[m] = true
		}
		s.agListPage(w, r, a, http.StatusUnprocessableEntity, agListData{Name: name, Chosen: chosen}, msg)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/ausgaben/"+g.ID, "Gruppe „"+g.Name+"“ angelegt.")
}

// ── edit group ────────────────────────────────────────

type agGroupForm struct {
	Group      ausgaben.Group
	People     []agPerson
	Name       string
	Candidates []agPerson // who can be added
	Chosen     map[string]bool
}

func (s *Server) agEditGroup(w http.ResponseWriter, r *http.Request, a account.Account) {
	g, people, ok := s.agLoad(w, r, a)
	if !ok {
		return
	}
	s.agGroupFormPage(w, r, http.StatusOK, agGroupForm{Group: g, People: people, Name: g.Name, Chosen: map[string]bool{}}, "")
}

func (s *Server) agGroupFormPage(w http.ResponseWriter, r *http.Request, status int, f agGroupForm, errMsg string) {
	all, err := s.Accounts.Repo.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, c := range all {
		if !f.Group.IsMember(c.ID) && c.Status == account.Active && c.Can(account.Ausgaben) {
			f.Candidates = append(f.Candidates, agPerson{ID: c.ID, Name: c.DisplayName()})
		}
	}
	s.agPage(w, r, status, "group_form", "Gruppe bearbeiten", errMsg, f)
}

func (s *Server) agSaveGroup(w http.ResponseWriter, r *http.Request, a account.Account) {
	g, people, ok := s.agLoad(w, r, a)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	add := r.PostForm["person"]
	_, err := s.Ausgaben.EditGroup(r.Context(), a, g.ID, name, add)
	if msg := agProblems(err); msg != "" {
		chosen := map[string]bool{}
		for _, m := range add {
			chosen[m] = true
		}
		s.agGroupFormPage(w, r, http.StatusUnprocessableEntity, agGroupForm{Group: g, People: people, Name: name, Chosen: chosen}, msg)
		return
	}
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	s.redirect(w, r, "/ausgaben/"+g.ID, "Gruppe gespeichert.")
}

// ── group page ────────────────────────────────────────

type agRow struct {
	ausgaben.Entry
	Payer, Receiver string
	For             string // names of the people with a share
	Mine            int64  // effect on the viewer's balance
	Unsplit         bool
}

type agBalance struct {
	Currency string
	Nets     []agNet
	Settled  bool
}

type agNet struct {
	Name   string
	Me     bool
	Amount int64
}

type agTransfer struct {
	ausgaben.Transfer
	FromName, ToName string
	Mine             bool // the viewer pays
}

type agRec struct {
	ausgaben.Recurring
	Payer, For string
}

type agGroupData struct {
	Group      ausgaben.Group
	People     []agPerson
	Balances   []agBalance
	Transfers  []agTransfer
	Recurrings []agRec
	Currencies []string // of all entries, sorted
	Currency   string   // filter; "" = all
	Rows       []agRow  // open entries (after the last settled point)
	Settled    []agSettled
	Empty      bool // no entries at all: offer the import
}

type agSettled struct{ ausgaben.Settled }

// agRowOf prepares entry e for the table as seen by a.
func agRowOf(e ausgaben.Entry, a account.Account, names map[string]string) agRow {
	row := agRow{Entry: e, Payer: names[e.PaidBy], Unsplit: e.PaidBy == ""}
	if e.Kind == ausgaben.Payment {
		row.Receiver = names[e.Receiver()]
	} else {
		row.For = agFor(e.Shares, names)
	}
	if e.PaidBy == a.ID {
		row.Mine += e.Amount
	}
	row.Mine -= e.ShareOf(a.ID)
	return row
}

// agCurrencies lists the currencies of es, sorted.
func agCurrencies(es []ausgaben.Entry) []string {
	var out []string
	for _, e := range es {
		if !slices.Contains(out, e.Currency) {
			out = append(out, e.Currency)
		}
	}
	slices.Sort(out)
	return out
}

// agCurrencyFilter reads ?waehrung=; ok is false for a currency not in curs.
func agCurrencyFilter(r *http.Request, curs []string) (string, bool) {
	c := r.URL.Query().Get("waehrung")
	return c, c == "" || slices.Contains(curs, c)
}

func (s *Server) agGroup(w http.ResponseWriter, r *http.Request, a account.Account) {
	v, err := s.Ausgaben.Open(r.Context(), a, r.PathValue("g"))
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	people, names, err := s.agPeople(r, a, v.Group)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := agGroupData{Group: v.Group, People: people, Empty: len(v.Entries) == 0, Currencies: agCurrencies(v.Entries)}
	cur, ok := agCurrencyFilter(r, d.Currencies)
	if !ok {
		s.status(w, r, http.StatusNotFound)
		return
	}
	d.Currency = cur
	for _, b := range v.Balances {
		if cur != "" && b.Currency != cur {
			continue
		}
		ab := agBalance{Currency: b.Currency, Settled: b.Settled()}
		for _, n := range b.Nets {
			ab.Nets = append(ab.Nets, agNet{Name: names[n.AccountID], Me: n.AccountID == a.ID, Amount: n.Amount})
		}
		d.Balances = append(d.Balances, ab)
	}
	for _, t := range v.Transfers {
		if cur == "" || t.Currency == cur {
			d.Transfers = append(d.Transfers, agTransfer{Transfer: t, FromName: names[t.From], ToName: names[t.To], Mine: t.From == a.ID})
		}
	}
	for _, x := range v.Recurrings {
		d.Recurrings = append(d.Recurrings, agRec{Recurring: x, Payer: names[x.PaidBy], For: agFor(x.Shares, names)})
	}
	open, settled := ausgaben.SplitSettled(v.Entries)
	for _, e := range open {
		if cur == "" || e.Currency == cur {
			d.Rows = append(d.Rows, agRowOf(e, a, names))
		}
	}
	for _, st := range settled {
		if cur == "" || st.Currency == cur {
			d.Settled = append(d.Settled, agSettled{Settled: st})
		}
	}
	s.agPage(w, r, http.StatusOK, "group", v.Group.Name, "", d)
}

// ── archive ───────────────────────────────────────────

type agArchiveData struct {
	Group      ausgaben.Group
	Currencies []string
	Currency   string
	Years      []string
	Year       string // "" = all
	Rows       []agRow
}

// agArchive lists the settled entries (before each currency's last even
// point), filtered by currency and year.
func (s *Server) agArchive(w http.ResponseWriter, r *http.Request, a account.Account) {
	v, err := s.Ausgaben.Open(r.Context(), a, r.PathValue("g"))
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	_, names, err := s.agPeople(r, a, v.Group)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	open, _ := ausgaben.SplitSettled(v.Entries)
	isOpen := map[string]bool{}
	for _, e := range open {
		isOpen[e.ID] = true
	}
	var done []ausgaben.Entry
	for _, e := range v.Entries {
		if !isOpen[e.ID] {
			done = append(done, e)
		}
	}
	d := agArchiveData{Group: v.Group, Currencies: agCurrencies(done)}
	cur, ok := agCurrencyFilter(r, d.Currencies)
	if !ok {
		s.status(w, r, http.StatusNotFound)
		return
	}
	d.Currency = cur
	for _, e := range done {
		if y := e.Date[:4]; (cur == "" || e.Currency == cur) && !slices.Contains(d.Years, y) {
			d.Years = append(d.Years, y)
		}
	}
	d.Year = r.URL.Query().Get("jahr")
	if d.Year != "" && !slices.Contains(d.Years, d.Year) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	for _, e := range done {
		if (cur == "" || e.Currency == cur) && (d.Year == "" || strings.HasPrefix(e.Date, d.Year)) {
			d.Rows = append(d.Rows, agRowOf(e, a, names))
		}
	}
	s.agPage(w, r, http.StatusOK, "archive", "Ausgeglichene Einträge", "", d)
}

func agFor(shares []ausgaben.Share, names map[string]string) string {
	out := make([]string, 0, len(shares))
	for _, sh := range shares {
		if sh.Amount > 0 {
			out = append(out, names[sh.AccountID])
		}
	}
	return strings.Join(out, ", ")
}

// ── entry form ────────────────────────────────────────

// agSplit is the "für wen" part of a form as the user filled it in.
type agSplit struct {
	Mode    string            // "gleich" or "betraege"
	For     map[string]bool   // gleich: who shares
	Amounts map[string]string // betraege: raw input per member
}

type agEntryForm struct {
	Group      ausgaben.Group
	People     []agPerson
	E          ausgaben.Entry
	Amount     string // raw
	Split      agSplit
	To         string // payment receiver
	Currencies []string
}

func (s *Server) agLoad(w http.ResponseWriter, r *http.Request, a account.Account) (ausgaben.Group, []agPerson, bool) {
	g, err := s.Ausgaben.Repo.Group(r.Context(), a.ID, r.PathValue("g"))
	if err != nil {
		s.agErr(w, r, err)
		return g, nil, false
	}
	people, _, err := s.agPeople(r, a, g)
	if err != nil {
		s.fail(w, r, err)
		return g, nil, false
	}
	return g, people, true
}

// agCurrencyList lists the base currency and those used in g so far.
func (s *Server) agCurrencyList(r *http.Request, a account.Account, g ausgaben.Group) ([]string, error) {
	es, err := s.Ausgaben.Repo.Entries(r.Context(), a.ID, g.ID)
	if err != nil {
		return nil, err
	}
	curs := []string{s.BaseCurrency}
	for _, e := range es {
		if !slices.Contains(curs, e.Currency) {
			curs = append(curs, e.Currency)
		}
	}
	return curs, nil
}

func (s *Server) agEntryPage(w http.ResponseWriter, r *http.Request, a account.Account, status int, f agEntryForm, errMsg string) {
	var err error
	if f.Currencies, err = s.agCurrencyList(r, a, f.Group); err != nil {
		s.fail(w, r, err)
		return
	}
	title := "Neue Ausgabe"
	switch {
	case f.E.Kind == ausgaben.Payment && f.E.ID == "":
		title = "Neue Zahlung"
	case f.E.Kind == ausgaben.Payment:
		title = "Zahlung bearbeiten"
	case f.E.ID != "":
		title = "Ausgabe bearbeiten"
	}
	s.agPage(w, r, status, "entry_form", title, errMsg, f)
}

func splitOf(g ausgaben.Group, amount int64, payer string, shares []ausgaben.Share) agSplit {
	sp := agSplit{Mode: "gleich", For: map[string]bool{}, Amounts: map[string]string{}}
	for _, sh := range shares {
		sp.For[sh.AccountID] = sh.Amount > 0
		sp.Amounts[sh.AccountID] = moneyInput(sh.Amount)
	}
	if len(shares) > 0 && !ausgaben.IsEqual(g, amount, payer, shares) {
		sp.Mode = "betraege"
	}
	return sp
}

func (s *Server) agNew(w http.ResponseWriter, r *http.Request, a account.Account) {
	g, people, ok := s.agLoad(w, r, a)
	if !ok {
		return
	}
	q := r.URL.Query()
	e := ausgaben.Entry{GroupID: g.ID, Kind: ausgaben.Expense, Date: s.Ausgaben.Today(), Currency: s.BaseCurrency, PaidBy: a.ID}
	f := agEntryForm{Group: g, People: people, Split: agSplit{Mode: "gleich", For: map[string]bool{}, Amounts: map[string]string{}}}
	for _, m := range g.Members {
		f.Split.For[m] = true
	}
	switch q.Get("art") {
	case "", string(ausgaben.Expense):
	case string(ausgaben.Payment):
		// Prefilled from a settle-up proposal; the user confirms the values.
		e.Kind = ausgaben.Payment
		if v := q.Get("von"); g.IsMember(v) {
			e.PaidBy = v
		}
		if v := q.Get("an"); g.IsMember(v) {
			f.To = v
		}
		if v := q.Get("waehrung"); ausgaben.ValidCurrency(v) {
			e.Currency = v
		}
		if n, err := parseMoney(q.Get("betrag")); err == nil {
			f.Amount = moneyInput(n)
		}
	default:
		s.status(w, r, http.StatusBadRequest)
		return
	}
	f.E = e
	s.agEntryPage(w, r, a, http.StatusOK, f, "")
}

func (s *Server) agEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	g, people, ok := s.agLoad(w, r, a)
	if !ok {
		return
	}
	e, err := s.Ausgaben.Repo.Entry(r.Context(), a.ID, g.ID, r.PathValue("e"))
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	f := agEntryForm{Group: g, People: people, E: e, Amount: moneyInput(e.Amount), To: e.Receiver(), Split: splitOf(g, e.Amount, e.PaidBy, e.Shares)}
	if e.PaidBy == "" {
		f.Split.Mode, f.E.PaidBy = "gleich", ""
	}
	s.agEntryPage(w, r, a, http.StatusOK, f, "")
}

// agParseSplit reads the "für wen" fields; amount is the parsed total.
func agParseSplit(r *http.Request, g ausgaben.Group, amount int64, payer string) (agSplit, []ausgaben.Share, []string) {
	sp := agSplit{Mode: r.PostFormValue("aufteilung"), For: map[string]bool{}, Amounts: map[string]string{}}
	var probs []string
	for _, id := range r.PostForm["fuer"] {
		if !g.IsMember(id) {
			return sp, nil, []string{"Unbekannte Person bei „Für wen“"}
		}
		sp.For[id] = true
	}
	for _, m := range g.Members {
		sp.Amounts[m] = r.PostFormValue("anteil_" + m)
	}
	switch sp.Mode {
	case "gleich":
		var ids []string
		for _, m := range g.Members {
			if sp.For[m] {
				ids = append(ids, m)
			}
		}
		return sp, ausgaben.Equal(g, amount, payer, ids), nil
	case "betraege":
		var shares []ausgaben.Share
		for _, m := range g.Members {
			n, err := parseMoney(sp.Amounts[m])
			if err != nil {
				probs = append(probs, fmt.Sprintf("Anteil „%s“ ist kein gültiger Betrag", sp.Amounts[m]))
				continue
			}
			if n > 0 {
				shares = append(shares, ausgaben.Share{AccountID: m, Amount: n})
			}
		}
		return sp, shares, probs
	}
	return sp, nil, []string{"Aufteilung wählen"}
}

func (s *Server) agSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	g, people, ok := s.agLoad(w, r, a)
	if !ok {
		return
	}
	var probs []string
	e := ausgaben.Entry{
		ID: r.PathValue("e"), GroupID: g.ID, Kind: ausgaben.Kind(r.PostFormValue("art")), Date: r.PostFormValue("date"),
		Description: strings.TrimSpace(r.PostFormValue("description")), Category: strings.TrimSpace(r.PostFormValue("category")),
		Currency: strings.TrimSpace(r.PostFormValue("currency")), PaidBy: r.PostFormValue("paid_by"),
	}
	if e.Kind != ausgaben.Expense && e.Kind != ausgaben.Payment {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	if e.ID != "" {
		v, err := strconv.Atoi(r.PostFormValue("version"))
		if err != nil {
			s.status(w, r, http.StatusBadRequest)
			return
		}
		e.Version = v
	}
	f := agEntryForm{Group: g, People: people, Amount: r.PostFormValue("amount"), To: r.PostFormValue("to"),
		Split: agSplit{Mode: "gleich", For: map[string]bool{}, Amounts: map[string]string{}}}
	amount, err := parseMoney(f.Amount)
	if err != nil || amount == 0 {
		probs = append(probs, "Betrag fehlt oder ist ungültig")
	}
	e.Amount = amount
	if e.Kind == ausgaben.Payment {
		if f.To != "" {
			e.Shares = []ausgaben.Share{{AccountID: f.To, Amount: amount}}
		}
	} else {
		var p []string
		f.Split, e.Shares, p = agParseSplit(r, g, amount, e.PaidBy)
		probs = append(probs, p...)
	}
	f.E = e
	if len(probs) > 0 {
		s.agEntryPage(w, r, a, http.StatusUnprocessableEntity, f, strings.Join(probs, ". ")+".")
		return
	}
	_, err = s.Ausgaben.SaveEntry(r.Context(), a, e)
	if msg := agProblems(err); msg != "" {
		s.agEntryPage(w, r, a, http.StatusUnprocessableEntity, f, msg)
		return
	}
	if errors.Is(err, ausgaben.ErrConflict) {
		s.agEntryPage(w, r, a, http.StatusConflict, f, "Der Eintrag wurde inzwischen anderswo geändert. Deine Eingaben stehen noch im Formular – lade die Seite neu, um die aktuelle Fassung zu sehen.")
		return
	}
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	s.redirect(w, r, "/ausgaben/"+g.ID, "Gespeichert.")
}

func (s *Server) agDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	v, err := strconv.Atoi(r.PostFormValue("version"))
	if err != nil || r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	g := r.PathValue("g")
	err = s.Ausgaben.DeleteEntry(r.Context(), a, g, r.PathValue("e"), v)
	if errors.Is(err, ausgaben.ErrConflict) {
		s.redirect(w, r, "/ausgaben/"+g+"/eintraege/"+r.PathValue("e"), "Der Eintrag wurde inzwischen geändert – bitte prüfen und nochmals löschen.")
		return
	}
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	s.redirect(w, r, "/ausgaben/"+g, "Gelöscht.")
}

// ── recurring ─────────────────────────────────────────

type agRecForm struct {
	Group      ausgaben.Group
	People     []agPerson
	R          ausgaben.Recurring
	Amount     string
	Split      agSplit
	Currencies []string
}

func (s *Server) agRecPage(w http.ResponseWriter, r *http.Request, a account.Account, status int, f agRecForm, errMsg string) {
	var err error
	if f.Currencies, err = s.agCurrencyList(r, a, f.Group); err != nil {
		s.fail(w, r, err)
		return
	}
	title := "Neue wiederkehrende Ausgabe"
	if f.R.ID != "" {
		title = "Wiederkehrende Ausgabe bearbeiten"
	}
	s.agPage(w, r, status, "recurring_form", title, errMsg, f)
}

func (s *Server) agRecNew(w http.ResponseWriter, r *http.Request, a account.Account) {
	g, people, ok := s.agLoad(w, r, a)
	if !ok {
		return
	}
	f := agRecForm{Group: g, People: people, R: ausgaben.Recurring{GroupID: g.ID, Currency: s.BaseCurrency, PaidBy: a.ID, Next: s.Ausgaben.Today()},
		Split: agSplit{Mode: "gleich", For: map[string]bool{}, Amounts: map[string]string{}}}
	for _, m := range g.Members {
		f.Split.For[m] = true
	}
	s.agRecPage(w, r, a, http.StatusOK, f, "")
}

func (s *Server) agRecEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	g, people, ok := s.agLoad(w, r, a)
	if !ok {
		return
	}
	x, err := s.Ausgaben.Repo.Recurring(r.Context(), a.ID, g.ID, r.PathValue("r"))
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	s.agRecPage(w, r, a, http.StatusOK, agRecForm{Group: g, People: people, R: x, Amount: moneyInput(x.Amount), Split: splitOf(g, x.Amount, x.PaidBy, x.Shares)}, "")
}

func (s *Server) agRecSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	g, people, ok := s.agLoad(w, r, a)
	if !ok {
		return
	}
	var probs []string
	x := ausgaben.Recurring{
		ID: r.PathValue("r"), GroupID: g.ID, Description: strings.TrimSpace(r.PostFormValue("description")),
		Category: strings.TrimSpace(r.PostFormValue("category")), Currency: strings.TrimSpace(r.PostFormValue("currency")),
		PaidBy: r.PostFormValue("paid_by"), Next: r.PostFormValue("next"),
	}
	if ausgaben.ValidDate(x.Next) {
		x.Day, _ = strconv.Atoi(x.Next[8:])
	}
	if x.ID != "" {
		v, err := strconv.Atoi(r.PostFormValue("version"))
		if err != nil {
			s.status(w, r, http.StatusBadRequest)
			return
		}
		x.Version = v
		// An unchanged next date keeps the day (31 stays 31 after a short month).
		old, err := s.Ausgaben.Repo.Recurring(r.Context(), a.ID, g.ID, x.ID)
		if err != nil {
			s.agErr(w, r, err)
			return
		}
		if old.Next == x.Next {
			x.Day = old.Day
		}
	}
	f := agRecForm{Group: g, People: people, Amount: r.PostFormValue("amount")}
	amount, err := parseMoney(f.Amount)
	if err != nil || amount == 0 {
		probs = append(probs, "Betrag fehlt oder ist ungültig")
	}
	x.Amount = amount
	var p []string
	f.Split, x.Shares, p = agParseSplit(r, g, amount, x.PaidBy)
	probs = append(probs, p...)
	f.R = x
	if len(probs) > 0 {
		s.agRecPage(w, r, a, http.StatusUnprocessableEntity, f, strings.Join(probs, ". ")+".")
		return
	}
	_, err = s.Ausgaben.SaveRecurring(r.Context(), a, x)
	if msg := agProblems(err); msg != "" {
		s.agRecPage(w, r, a, http.StatusUnprocessableEntity, f, msg)
		return
	}
	if errors.Is(err, ausgaben.ErrConflict) {
		s.agRecPage(w, r, a, http.StatusConflict, f, "Die wiederkehrende Ausgabe wurde inzwischen geändert oder gebucht. Lade die Seite neu und prüfe deine Eingaben.")
		return
	}
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	s.redirect(w, r, "/ausgaben/"+g.ID, "Gespeichert. Fällige Monate werden beim Öffnen der Gruppe gebucht.")
}

func (s *Server) agRecDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	v, err := strconv.Atoi(r.PostFormValue("version"))
	if err != nil || r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	g := r.PathValue("g")
	err = s.Ausgaben.DeleteRecurring(r.Context(), a, g, r.PathValue("r"), v)
	if errors.Is(err, ausgaben.ErrConflict) {
		s.redirect(w, r, "/ausgaben/"+g+"/wiederkehrend/"+r.PathValue("r"), "Inzwischen geändert oder gebucht – bitte nochmals beenden.")
		return
	}
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	s.redirect(w, r, "/ausgaben/"+g, "Beendet. Bereits gebuchte Einträge bleiben.")
}

// ── import ────────────────────────────────────────────

func (s *Server) agImport(w http.ResponseWriter, r *http.Request, a account.Account) {
	gid := r.PathValue("g")
	g, err := s.Ausgaben.Repo.Group(r.Context(), a.ID, gid)
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSplitwiseUpload)
	f, _, err := r.FormFile("datei")
	if err != nil {
		s.redirect(w, r, "/ausgaben/"+g.ID, "Keine Datei erhalten (höchstens 10 MB).")
		return
	}
	defer func() { _ = f.Close() }()
	names := map[string]string{}
	for _, m := range g.Members {
		n := strings.TrimSpace(r.PostFormValue("name_" + m))
		if n == "" {
			s.redirect(w, r, "/ausgaben/"+g.ID, "Import abgebrochen: für jede Person den Namen aus Splitwise angeben.")
			return
		}
		if _, dup := names[n]; dup {
			s.redirect(w, r, "/ausgaben/"+g.ID, "Import abgebrochen: zwei Personen mit demselben Namen „"+n+"“.")
			return
		}
		names[n] = m
	}
	n, err := s.Ausgaben.Import(r.Context(), a, g.ID, f, names)
	var inv *ausgaben.InvalidError
	if errors.As(err, &inv) {
		// The message travels in a cookie: show the first problems only.
		ps := inv.Problems
		more := ""
		if len(ps) > 8 {
			more = fmt.Sprintf(" … und %d weitere.", len(ps)-8)
			ps = ps[:8]
		}
		s.redirect(w, r, "/ausgaben/"+g.ID, "Import abgebrochen, nichts gespeichert: "+strings.Join(ps, ". ")+"."+more)
		return
	}
	if errors.Is(err, ausgaben.ErrNotEmpty) {
		s.redirect(w, r, "/ausgaben/"+g.ID, "Import abgebrochen: die Gruppe hat schon Einträge. Importiere nur in eine neue, leere Gruppe.")
		return
	}
	if err != nil {
		s.agErr(w, r, err)
		return
	}
	s.redirect(w, r, "/ausgaben/"+g.ID, strconv.Itoa(n)+" Einträge importiert.")
}
