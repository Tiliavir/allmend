package webui

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/finanzen"
	"github.com/tiliavir/allmend/internal/markdown"
)

func (s *Server) finanzenRoutes(mux *http.ServeMux) {
	m := func(h func(http.ResponseWriter, *http.Request, account.Account)) http.Handler {
		return s.Module(account.Finanzen, h)
	}
	mux.Handle("GET /finanzen", m(s.finOverview))
	mux.Handle("POST /finanzen/kurse", m(s.finRefresh))
	mux.Handle("GET /finanzen/anlagen/neu", m(s.finAssetNew))
	mux.Handle("POST /finanzen/anlagen", m(s.finAssetSave))
	mux.Handle("GET /finanzen/anlagen/{id}", m(s.finAssetEdit))
	mux.Handle("POST /finanzen/anlagen/{id}", m(s.finAssetSave))
	mux.Handle("POST /finanzen/anlagen/{id}/loeschen", m(s.finAssetDelete))
	mux.Handle("GET /finanzen/wertpapiere", m(s.finSecurities))
	mux.Handle("GET /finanzen/wertpapiere/neu", m(s.finSecurityNew))
	mux.Handle("POST /finanzen/wertpapiere", m(s.finSecuritySave))
	mux.Handle("GET /finanzen/wertpapiere/{id}", m(s.finSecurity))
	mux.Handle("GET /finanzen/wertpapiere/{id}/bearbeiten", m(s.finSecurityEdit))
	mux.Handle("POST /finanzen/wertpapiere/{id}", m(s.finSecuritySave))
	mux.Handle("POST /finanzen/wertpapiere/{id}/archiv", m(s.finSecurityArchive))
	mux.Handle("POST /finanzen/wertpapiere/{id}/loeschen", m(s.finSecurityDelete))
	mux.Handle("GET /finanzen/wertpapiere/{id}/transaktionen/neu", m(s.finTxNew))
	mux.Handle("POST /finanzen/wertpapiere/{id}/transaktionen", m(s.finTxSave))
	mux.Handle("GET /finanzen/transaktionen/{tid}", m(s.finTxEdit))
	mux.Handle("POST /finanzen/transaktionen/{tid}", m(s.finTxSave))
	mux.Handle("POST /finanzen/transaktionen/{tid}/loeschen", m(s.finTxDelete))
	mux.Handle("GET /finanzen/wertpapiere/{id}/dividenden/neu", m(s.finDivNew))
	mux.Handle("POST /finanzen/wertpapiere/{id}/dividenden", m(s.finDivSave))
	mux.Handle("GET /finanzen/dividenden/{did}", m(s.finDivEdit))
	mux.Handle("POST /finanzen/dividenden/{did}", m(s.finDivSave))
	mux.Handle("POST /finanzen/dividenden/{did}/loeschen", m(s.finDivDelete))
	mux.Handle("GET /finanzen/entwicklung", m(s.finDevelopment))
	mux.Handle("GET /finanzen/diversifikation", m(s.finDiversification))
	mux.Handle("GET /finanzen/verlauf", m(s.finYears))
	mux.Handle("GET /finanzen/verlauf/neu", m(s.finYearNew))
	mux.Handle("POST /finanzen/verlauf", m(s.finYearSave))
	mux.Handle("GET /finanzen/verlauf/{jahr}", m(s.finYearEdit))
	mux.Handle("POST /finanzen/verlauf/{jahr}", m(s.finYearSave))
	mux.Handle("POST /finanzen/verlauf/{jahr}/loeschen", m(s.finYearDelete))
}

var chartScripts = []Script{{Src: "/static/vendor/chart-4.6.1.umd.min.js"}, {Src: "/static/finanzen.js", Module: true}}

func (s *Server) finPage(w http.ResponseWriter, r *http.Request, status int, name, title, errMsg string, d any, charts bool) {
	p := Page{Title: title, Module: account.Finanzen, Error: errMsg, Data: d}
	if charts {
		p.Scripts = chartScripts
	}
	s.render(w, r, status, "finanzen/"+name, p)
}

func (s *Server) finErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, finanzen.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	s.fail(w, r, err)
}

func finProblems(err error) string {
	var inv *finanzen.InvalidError
	if errors.As(err, &inv) {
		return strings.Join(inv.Problems, ". ") + "."
	}
	return ""
}

const conflictMsg = "Der Eintrag wurde inzwischen anderswo geändert. Lade die Seite neu, um die aktuelle Fassung zu sehen."

func (s *Server) today() string { return time.Now().In(s.Location).Format(time.DateOnly) }

// formReader collects parse problems of form fields (German).
type formReader struct {
	r   *http.Request
	bad []string
}

func (f *formReader) text(name string) string { return strings.TrimSpace(f.r.PostFormValue(name)) }

func (f *formReader) cents(name, label string) int64 {
	c, err := parseMoney(f.r.PostFormValue(name))
	if err != nil {
		f.bad = append(f.bad, label+" ist kein gültiger Betrag")
	}
	return c
}

var quantityRe = regexp.MustCompile(`^\d{1,9}(?:[.,]\d{1,6})?$`)

func (f *formReader) quantity(name, label string) float64 {
	v := strings.ReplaceAll(strings.TrimSpace(f.r.PostFormValue(name)), "'", "")
	if !quantityRe.MatchString(v) {
		f.bad = append(f.bad, label+" ist keine Zahl (bis 6 Nachkommastellen)")
		return 0
	}
	q, _ := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
	return q
}

func (f *formReader) version() int {
	v, err := strconv.Atoi(f.r.PostFormValue("version"))
	if err != nil && f.r.PostFormValue("version") != "" {
		f.bad = append(f.bad, "Version ungültig")
	}
	return v
}

func (f *formReader) msg() string {
	if len(f.bad) == 0 {
		return ""
	}
	return strings.Join(f.bad, ". ") + "."
}

// ── overview ──────────────────────────────────────────

type finOverview struct {
	finanzen.Overview
	Run      *finanzen.Run
	RunAge   string
	CanFetch bool
}

func (s *Server) finOverview(w http.ResponseWriter, r *http.Request, a account.Account) {
	o, err := s.Finanzen.Overview(r.Context(), a)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := finOverview{Overview: o, CanFetch: a.IsAdmin && s.FinRefresh != nil}
	if run, err := s.Finanzen.Market.LastRun(r.Context()); err == nil {
		d.Run = &run
		d.RunAge = run.At.In(s.Location).Format("02.01.2006 15:04")
	} else if !errors.Is(err, finanzen.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	s.finPage(w, r, http.StatusOK, "overview", "Finanzen", "", d, false)
}

func (s *Server) finRefresh(w http.ResponseWriter, r *http.Request, a account.Account) {
	if !a.IsAdmin || s.FinRefresh == nil {
		s.status(w, r, http.StatusForbidden)
		return
	}
	if !s.FinRefresh() {
		s.redirect(w, r, "/finanzen", "Die Kurse werden gerade schon geladen.")
		return
	}
	s.redirect(w, r, "/finanzen", "Kurse werden geladen. Das Ergebnis steht in etwa einer Minute unten auf dieser Seite.")
}

// ── assets ────────────────────────────────────────────

type finAssetData struct {
	Asset finanzen.Asset
	Kinds any
}

func (s *Server) finAssetForm(w http.ResponseWriter, r *http.Request, status int, as finanzen.Asset, errMsg string) {
	title := "Neue Anlage"
	if as.ID != "" {
		title = as.Name + " bearbeiten"
	}
	s.finPage(w, r, status, "asset_form", title, errMsg, finAssetData{Asset: as, Kinds: finanzen.AssetKinds}, false)
}

func (s *Server) finAssetNew(w http.ResponseWriter, r *http.Request, _ account.Account) {
	as := finanzen.Asset{Kind: finanzen.Konto, Currency: s.BaseCurrency, AsOf: s.today()}
	if k := finanzen.AssetKind(r.URL.Query().Get("art")); k != "" {
		if k.Label() == string(k) {
			s.status(w, r, http.StatusNotFound)
			return
		}
		as.Kind = k
	}
	s.finAssetForm(w, r, http.StatusOK, as, "")
}

func (s *Server) finAssetEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	as, err := s.Finanzen.Repo.Asset(r.Context(), a.ID, r.PathValue("id"))
	if err != nil {
		s.finErr(w, r, err)
		return
	}
	s.finAssetForm(w, r, http.StatusOK, as, "")
}

func (s *Server) finAssetSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	f := &formReader{r: r}
	as := finanzen.Asset{ID: r.PathValue("id"), Name: f.text("name"), Kind: finanzen.AssetKind(f.text("kind")), Category: f.text("category"),
		Currency: strings.ToUpper(f.text("currency")), AsOf: f.text("as_of"), MaturityDate: f.text("maturity"),
		Comment: strings.ReplaceAll(f.text("comment"), "\r\n", "\n"), Version: f.version()}
	as.ValueCents = f.cents("value", "Wert")
	as.TaxAllowanceCents = f.cents("tax_allowance", "Steuerfreibetrag")
	as.CostCents = f.cents("cost", "Kosten")
	as.PaidInCents = f.cents("paid_in", "Eingezahlt")
	as.DistributionsCents = f.cents("distributions", "Ausschüttungen")
	if m := f.msg(); m != "" {
		s.finAssetForm(w, r, http.StatusUnprocessableEntity, as, m)
		return
	}
	_, err := s.Finanzen.SaveAsset(r.Context(), a, as)
	switch {
	case finProblems(err) != "":
		s.finAssetForm(w, r, http.StatusUnprocessableEntity, as, finProblems(err))
	case errors.Is(err, finanzen.ErrConflict):
		s.finAssetForm(w, r, http.StatusConflict, as, conflictMsg)
	case err != nil:
		s.finErr(w, r, err)
	default:
		s.redirect(w, r, "/finanzen", "Anlage gespeichert.")
	}
}

func (s *Server) finAssetDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	if err := s.Finanzen.Repo.DeleteAsset(r.Context(), a.ID, r.PathValue("id")); err != nil {
		s.finErr(w, r, err)
		return
	}
	s.redirect(w, r, "/finanzen", "Anlage gelöscht.")
}

// ── securities ────────────────────────────────────────

type finSecuritiesData struct {
	Securities []finanzen.Summary
	Archived   bool
	Base       string
}

func (s *Server) finSecurities(w http.ResponseWriter, r *http.Request, a account.Account) {
	archived := r.URL.Query().Get("archiv") == "1"
	if q := r.URL.Query().Get("archiv"); q != "" && q != "1" {
		s.status(w, r, http.StatusNotFound)
		return
	}
	list, err := s.Finanzen.Securities(r.Context(), a, archived)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	title := "Wertpapiere"
	if archived {
		title = "Archivierte Wertpapiere"
	}
	s.finPage(w, r, http.StatusOK, "securities", title, "", finSecuritiesData{Securities: list, Archived: archived, Base: s.BaseCurrency}, false)
}

type finDetailData struct {
	finanzen.Detail
	Chart    map[string]any
	Dims     any
	TaxLabel string
}

func (s *Server) finSecurity(w http.ResponseWriter, r *http.Request, a account.Account) {
	d, err := s.Finanzen.Security(r.Context(), a, r.PathValue("id"))
	if err != nil {
		s.finErr(w, r, err)
		return
	}
	data := finDetailData{Detail: d, Dims: finanzen.Dimensions, TaxLabel: strconv.Itoa(finanzen.TaxPercent) + " %"}
	charts := len(d.Prices) > 1
	if charts {
		labels := make([]string, len(d.Prices))
		vals := make([]float64, len(d.Prices))
		for i, p := range d.Prices {
			labels[i], vals[i] = p.Date, p.Close
		}
		data.Chart = map[string]any{"type": "line", "labels": labels, "unit": d.Currency,
			"series": []map[string]any{{"label": "Schlusskurs", "data": vals}}}
	}
	s.finPage(w, r, http.StatusOK, "security", d.Name, "", data, charts)
}

type finSecurityForm struct {
	Security finanzen.Security
	Kinds    any
	Dims     any
	Weights  map[string]string // dimension → textarea content
}

func (s *Server) finSecurityForm(w http.ResponseWriter, r *http.Request, status int, sec finanzen.Security, weights map[string]string, errMsg string) {
	if weights == nil {
		weights = formatWeights(sec.Weights)
	}
	title := "Neues Wertpapier"
	if sec.ID != "" {
		title = sec.Name + " bearbeiten"
	}
	s.finPage(w, r, status, "security_form", title, errMsg,
		finSecurityForm{Security: sec, Kinds: finanzen.SecurityKinds, Dims: finanzen.Dimensions, Weights: weights}, false)
}

func (s *Server) finSecurityNew(w http.ResponseWriter, r *http.Request, _ account.Account) {
	s.finSecurityForm(w, r, http.StatusOK, finanzen.Security{Kind: finanzen.ETF, Currency: s.BaseCurrency}, nil, "")
}

func (s *Server) finSecurityEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	sec, err := s.Finanzen.Repo.Security(r.Context(), a.ID, r.PathValue("id"))
	if err != nil {
		s.finErr(w, r, err)
		return
	}
	s.finSecurityForm(w, r, http.StatusOK, sec, nil, "")
}

func (s *Server) finSecuritySave(w http.ResponseWriter, r *http.Request, a account.Account) {
	f := &formReader{r: r}
	sec := finanzen.Security{ID: r.PathValue("id"), Name: f.text("name"), ISIN: strings.ToUpper(f.text("isin")), Ticker: f.text("ticker"),
		Kind: f.text("kind"), Currency: strings.ToUpper(f.text("currency")), Symbol: f.text("symbol"), Version: f.version()}
	if v := f.text("risk"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			f.bad = append(f.bad, "Risikoklasse ist keine Zahl")
		}
		sec.RiskOverride = n
	}
	if sec.ID != "" {
		old, err := s.Finanzen.Repo.Security(r.Context(), a.ID, sec.ID)
		if err != nil {
			s.finErr(w, r, err)
			return
		}
		sec.Archived, sec.ArchivedAt = old.Archived, old.ArchivedAt
	}
	raw := map[string]string{}
	for _, d := range finanzen.Dimensions {
		raw[d.ID] = r.PostFormValue("w_" + d.ID)
		ws, problems := parseWeights(d.ID, d.Label, raw[d.ID])
		sec.Weights = append(sec.Weights, ws...)
		f.bad = append(f.bad, problems...)
	}
	if m := f.msg(); m != "" {
		s.finSecurityForm(w, r, http.StatusUnprocessableEntity, sec, raw, m)
		return
	}
	saved, err := s.Finanzen.SaveSecurity(r.Context(), a, sec)
	switch {
	case finProblems(err) != "":
		s.finSecurityForm(w, r, http.StatusUnprocessableEntity, sec, raw, finProblems(err))
	case errors.Is(err, finanzen.ErrConflict):
		s.finSecurityForm(w, r, http.StatusConflict, sec, raw, conflictMsg)
	case err != nil:
		s.finErr(w, r, err)
	default:
		s.redirect(w, r, "/finanzen/wertpapiere/"+saved.ID, "Wertpapier gespeichert.")
	}
}

var weightLineRe = regexp.MustCompile(`^(.+?)\s*[=:]\s*(\d{1,3}(?:[.,]\d{1,4})?)\s*%?$`)

// parseWeights reads lines "Label = 30" (percent) into weights.
func parseWeights(dim, label, text string) ([]finanzen.Weight, []string) {
	var ws []finanzen.Weight
	var bad []string
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := weightLineRe.FindStringSubmatch(line)
		if m == nil {
			bad = append(bad, fmt.Sprintf("%s, Zeile %d: erwartet »Bezeichnung = Prozent«", label, i+1))
			continue
		}
		p, _ := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", "."), 64)
		ws = append(ws, finanzen.Weight{Dimension: dim, Label: strings.TrimSpace(m[1]), Weight: p / 100})
	}
	return ws, bad
}

func formatWeights(ws []finanzen.Weight) map[string]string {
	out := map[string]string{}
	for _, w := range ws {
		out[w.Dimension] += w.Label + " = " + strings.ReplaceAll(strconv.FormatFloat(math.Round(w.Weight*1e6)/1e4, 'f', -1, 64), ".", ",") + "\n"
	}
	return out
}

func (s *Server) finSecurityArchive(w http.ResponseWriter, r *http.Request, a account.Account) {
	v := r.PostFormValue("archived")
	if v != "0" && v != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	err := s.Finanzen.SetArchived(r.Context(), a, id, v == "1", s.today())
	switch {
	case finProblems(err) != "":
		s.redirect(w, r, "/finanzen/wertpapiere/"+id, finProblems(err))
	case err != nil:
		s.finErr(w, r, err)
	case v == "1":
		s.redirect(w, r, "/finanzen/wertpapiere/"+id, "Wertpapier archiviert.")
	default:
		s.redirect(w, r, "/finanzen/wertpapiere/"+id, "Wertpapier wieder aktiv.")
	}
}

func (s *Server) finSecurityDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	err := s.Finanzen.Repo.DeleteSecurity(r.Context(), a.ID, id)
	switch {
	case errors.Is(err, finanzen.ErrInUse):
		s.redirect(w, r, "/finanzen/wertpapiere/"+id, "Löschen geht nur ohne Transaktionen und Dividenden. Archiviere das Wertpapier stattdessen.")
	case err != nil:
		s.finErr(w, r, err)
	default:
		s.redirect(w, r, "/finanzen/wertpapiere", "Wertpapier gelöscht.")
	}
}

// ── transactions ──────────────────────────────────────

type finTxData struct {
	Security finanzen.Security
	Tx       finanzen.Transaction
}

func (s *Server) finTxForm(w http.ResponseWriter, r *http.Request, a account.Account, status int, t finanzen.Transaction, errMsg string) {
	sec, err := s.Finanzen.Repo.Security(r.Context(), a.ID, t.SecurityID)
	if err != nil {
		s.finErr(w, r, err)
		return
	}
	title := "Kauf erfassen"
	switch {
	case t.ID != "":
		title = "Transaktion bearbeiten"
	case t.Type == finanzen.Sell:
		title = "Verkauf erfassen"
	}
	s.finPage(w, r, status, "tx_form", title, errMsg, finTxData{Security: sec, Tx: t}, false)
}

func (s *Server) finTxNew(w http.ResponseWriter, r *http.Request, a account.Account) {
	t := finanzen.Transaction{SecurityID: r.PathValue("id"), Date: s.today(), Type: finanzen.Buy}
	switch r.URL.Query().Get("typ") {
	case "", "kauf":
	case "verkauf":
		t.Type = finanzen.Sell
	default:
		s.status(w, r, http.StatusNotFound)
		return
	}
	s.finTxForm(w, r, a, http.StatusOK, t, "")
}

func (s *Server) finTxEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	t, err := s.Finanzen.Repo.Transaction(r.Context(), a.ID, r.PathValue("tid"))
	if err != nil {
		s.finErr(w, r, err)
		return
	}
	s.finTxForm(w, r, a, http.StatusOK, t, "")
}

func (s *Server) finTxSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	f := &formReader{r: r}
	t := finanzen.Transaction{ID: r.PathValue("tid"), SecurityID: r.PathValue("id"), Date: f.text("date"), Type: f.text("type"), Version: f.version()}
	if t.ID != "" {
		old, err := s.Finanzen.Repo.Transaction(r.Context(), a.ID, t.ID)
		if err != nil {
			s.finErr(w, r, err)
			return
		}
		t.SecurityID = old.SecurityID
	}
	t.Quantity = f.quantity("quantity", "Stückzahl")
	t.PriceCents = f.cents("price", "Kurs")
	t.FeesCents = f.cents("fees", "Gebühren")
	t.TaxesCents = f.cents("taxes", "Steuern")
	if m := f.msg(); m != "" {
		s.finTxForm(w, r, a, http.StatusUnprocessableEntity, t, m)
		return
	}
	_, err := s.Finanzen.SaveTransaction(r.Context(), a, t)
	switch {
	case finProblems(err) != "":
		s.finTxForm(w, r, a, http.StatusUnprocessableEntity, t, finProblems(err))
	case errors.Is(err, finanzen.ErrConflict):
		s.finTxForm(w, r, a, http.StatusConflict, t, conflictMsg)
	case err != nil:
		s.finErr(w, r, err)
	default:
		s.redirect(w, r, "/finanzen/wertpapiere/"+t.SecurityID, "Transaktion gespeichert.")
	}
}

func (s *Server) finTxDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	t, err := s.Finanzen.DeleteTransaction(r.Context(), a, r.PathValue("tid"))
	switch {
	case finProblems(err) != "":
		s.redirect(w, r, "/finanzen/wertpapiere/"+t.SecurityID, "Nicht gelöscht: "+finProblems(err))
	case err != nil:
		s.finErr(w, r, err)
	default:
		s.redirect(w, r, "/finanzen/wertpapiere/"+t.SecurityID, "Transaktion gelöscht.")
	}
}

// ── dividends ─────────────────────────────────────────

type finDivData struct {
	Security finanzen.Security
	Div      finanzen.Dividend
}

func (s *Server) finDivForm(w http.ResponseWriter, r *http.Request, a account.Account, status int, d finanzen.Dividend, errMsg string) {
	sec, err := s.Finanzen.Repo.Security(r.Context(), a.ID, d.SecurityID)
	if err != nil {
		s.finErr(w, r, err)
		return
	}
	title := "Dividende erfassen"
	if d.ID != "" {
		title = "Dividende bearbeiten"
	}
	s.finPage(w, r, status, "div_form", title, errMsg, finDivData{Security: sec, Div: d}, false)
}

func (s *Server) finDivNew(w http.ResponseWriter, r *http.Request, a account.Account) {
	s.finDivForm(w, r, a, http.StatusOK, finanzen.Dividend{SecurityID: r.PathValue("id"), Date: s.today()}, "")
}

func (s *Server) finDivEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	d, err := s.Finanzen.Repo.Dividend(r.Context(), a.ID, r.PathValue("did"))
	if err != nil {
		s.finErr(w, r, err)
		return
	}
	s.finDivForm(w, r, a, http.StatusOK, d, "")
}

func (s *Server) finDivSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	f := &formReader{r: r}
	d := finanzen.Dividend{ID: r.PathValue("did"), SecurityID: r.PathValue("id"), Date: f.text("date"), Version: f.version()}
	if d.ID != "" {
		old, err := s.Finanzen.Repo.Dividend(r.Context(), a.ID, d.ID)
		if err != nil {
			s.finErr(w, r, err)
			return
		}
		d.SecurityID = old.SecurityID
	}
	d.AmountCents = f.cents("amount", "Betrag")
	if m := f.msg(); m != "" {
		s.finDivForm(w, r, a, http.StatusUnprocessableEntity, d, m)
		return
	}
	_, err := s.Finanzen.SaveDividend(r.Context(), a, d)
	switch {
	case finProblems(err) != "":
		s.finDivForm(w, r, a, http.StatusUnprocessableEntity, d, finProblems(err))
	case errors.Is(err, finanzen.ErrConflict):
		s.finDivForm(w, r, a, http.StatusConflict, d, conflictMsg)
	case err != nil:
		s.finErr(w, r, err)
	default:
		s.redirect(w, r, "/finanzen/wertpapiere/"+d.SecurityID, "Dividende gespeichert.")
	}
}

func (s *Server) finDivDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	d, err := s.Finanzen.Repo.Dividend(r.Context(), a.ID, r.PathValue("did"))
	if err != nil {
		s.finErr(w, r, err)
		return
	}
	if err := s.Finanzen.Repo.DeleteDividend(r.Context(), a.ID, d.ID); err != nil {
		s.finErr(w, r, err)
		return
	}
	s.redirect(w, r, "/finanzen/wertpapiere/"+d.SecurityID, "Dividende gelöscht.")
}

// ── development ───────────────────────────────────────

type finDevData struct {
	finanzen.Development
	Filter     finanzen.Filter
	Range      string
	Kinds      any
	ValueChart map[string]any
	GainChart  map[string]any
	IndexChart map[string]any
	Last       int // index of the last point, -1 if none
	Ranges     []struct{ ID, Label string }
	Omitted    int // indexed series beyond the palette
}

var devRanges = []struct{ ID, Label string }{{"1m", "1 Monat"}, {"6m", "6 Monate"}, {"1j", "1 Jahr"}, {"3j", "3 Jahre"}, {"5j", "5 Jahre"}, {"alle", "Alles"}}

func (s *Server) finDevelopment(w http.ResponseWriter, r *http.Request, a account.Account) {
	q := r.URL.Query()
	f := finanzen.Filter{SecurityID: q.Get("wertpapier"), Kind: q.Get("art")}
	if f.Kind != "" && finanzen.KindLabel(f.Kind) == f.Kind {
		s.status(w, r, http.StatusNotFound)
		return
	}
	rng := q.Get("zeitraum")
	if rng == "" {
		rng = "1j"
	}
	now := time.Now().In(s.Location)
	switch rng {
	case "1m":
		f.From = now.AddDate(0, -1, 0).Format(time.DateOnly)
	case "6m":
		f.From = now.AddDate(0, -6, 0).Format(time.DateOnly)
	case "1j":
		f.From = now.AddDate(-1, 0, 0).Format(time.DateOnly)
	case "3j":
		f.From = now.AddDate(-3, 0, 0).Format(time.DateOnly)
	case "5j":
		f.From = now.AddDate(-5, 0, 0).Format(time.DateOnly)
	case "alle":
	default:
		s.status(w, r, http.StatusNotFound)
		return
	}
	dev, err := s.Finanzen.Development(r.Context(), a, f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if f.SecurityID != "" && !slices.ContainsFunc(dev.Securities, func(x finanzen.Security) bool { return x.ID == f.SecurityID }) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	d := finDevData{Development: dev, Filter: f, Range: rng, Kinds: finanzen.SecurityKinds, Ranges: devRanges, Last: len(dev.Dates) - 1}
	charts := len(dev.Dates) > 1
	if charts {
		d.ValueChart = map[string]any{"type": "line", "labels": dev.Dates, "unit": dev.Base,
			"series": []map[string]any{{"label": "Depotwert", "data": dev.Value, "fill": true}}}
		d.GainChart = map[string]any{"type": "line", "labels": dev.Dates, "unit": dev.Base, "stacked": true,
			"series": []map[string]any{
				{"label": "Kursgewinn (offen)", "data": dev.Unrealized},
				{"label": "Realisiert", "data": dev.Realized},
				{"label": "Dividenden", "data": dev.Dividends},
			}}
		var series []map[string]any
		for i, ix := range dev.Indexed {
			if i == 8 {
				d.Omitted = len(dev.Indexed) - 8
				break
			}
			series = append(series, map[string]any{"label": ix.Name, "data": ix.Values})
		}
		if len(series) > 0 {
			d.IndexChart = map[string]any{"type": "line", "labels": dev.Dates, "unit": "", "series": series, "baseline": 100}
		}
	}
	s.finPage(w, r, http.StatusOK, "development", "Entwicklung", "", d, charts)
}

// ── diversification ───────────────────────────────────

type finDivers struct {
	finanzen.Diversification
	Dims  any
	Kinds []struct{ ID, Label string }
	Risk1 string
}

func (s *Server) finDiversification(w http.ResponseWriter, r *http.Request, a account.Account) {
	q := r.URL.Query()
	kinds := []struct{ ID, Label string }{{"wertpapiere", "Wertpapiere"}}
	for _, k := range finanzen.AssetKinds {
		kinds = append(kinds, struct{ ID, Label string }{string(k.ID), finanzen.AssetClassLabels[string(k.ID)]})
	}
	for _, k := range q["ohne_art"] {
		if !slices.ContainsFunc(kinds, func(x struct{ ID, Label string }) bool { return x.ID == k }) {
			s.status(w, r, http.StatusNotFound)
			return
		}
	}
	dv, err := s.Finanzen.Diversification(r.Context(), a, q["ohne"], q["ohne_art"])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, x := range q["ohne"] {
		if !slices.ContainsFunc(dv.Securities, func(sec finanzen.Security) bool { return sec.ID == x }) {
			s.status(w, r, http.StatusNotFound)
			return
		}
	}
	d := finDivers{Diversification: dv, Dims: finanzen.Dimensions, Kinds: kinds}
	if dv.WeightedRisk > 0 {
		d.Risk1 = strings.ReplaceAll(strconv.FormatFloat(dv.WeightedRisk, 'f', 1, 64), ".", ",")
	}
	s.finPage(w, r, http.StatusOK, "diversification", "Diversifikation & Risiko", "", d, false)
}

// ── years ─────────────────────────────────────────────

type finYearRow struct {
	finanzen.Year
	HTML any
}

type finYearsData struct {
	Years []finYearRow
	Chart map[string]any
	Base  string
	Year  finanzen.Year
}

func (s *Server) finYears(w http.ResponseWriter, r *http.Request, a account.Account) {
	ys, err := s.Finanzen.Repo.Years(r.Context(), a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := finYearsData{Base: s.BaseCurrency}
	labels := make([]string, len(ys))
	cats := []struct {
		label string
		get   func(finanzen.Year) int64
	}{
		{"Pensionskasse", func(y finanzen.Year) int64 { return y.PensionCents }},
		{"Barmittel", func(y finanzen.Year) int64 { return y.CashCents }},
		{"Bauspar", func(y finanzen.Year) int64 { return y.BausparCents }},
		{"Aktien", func(y finanzen.Year) int64 { return y.StocksCents }},
		{"Fonds", func(y finanzen.Year) int64 { return y.FundsCents }},
		{"Grundstück", func(y finanzen.Year) int64 { return y.RealEstateCents }},
	}
	series := make([]map[string]any, len(cats))
	vals := make([][]float64, len(cats))
	for i := range cats {
		vals[i] = make([]float64, len(ys))
	}
	for i := len(ys) - 1; i >= 0; i-- {
		y := ys[i]
		row := finYearRow{Year: y}
		if y.Comment != "" {
			res, err := markdown.Render(y.Comment, markdown.Tasks{})
			if err != nil {
				s.fail(w, r, err)
				return
			}
			row.HTML = res.HTML
		}
		d.Years = append(d.Years, row)
	}
	for i, y := range ys {
		labels[i] = strconv.Itoa(y.Year)
		for c, cat := range cats {
			vals[c][i] = float64(cat.get(y)) / 100
		}
	}
	for c, cat := range cats {
		series[c] = map[string]any{"label": cat.label, "data": vals[c]}
	}
	if len(ys) > 0 {
		d.Chart = map[string]any{"type": "bar", "labels": labels, "unit": s.BaseCurrency, "stacked": true, "series": series}
	}
	s.finPage(w, r, http.StatusOK, "years", "Verlauf", "", d, len(ys) > 0)
}

func (s *Server) finYearForm(w http.ResponseWriter, r *http.Request, status int, y finanzen.Year, errMsg string) {
	title := "Neues Jahr"
	if y.Version > 0 {
		title = "Jahr " + strconv.Itoa(y.Year)
	}
	s.finPage(w, r, status, "year_form", title, errMsg, finYearsData{Year: y, Base: s.BaseCurrency}, false)
}

func (s *Server) finYearNew(w http.ResponseWriter, r *http.Request, _ account.Account) {
	s.finYearForm(w, r, http.StatusOK, finanzen.Year{Year: time.Now().In(s.Location).Year() - 1}, "")
}

func pathYear(r *http.Request) (int, bool) {
	v := r.PathValue("jahr")
	n, err := strconv.Atoi(v)
	return n, err == nil && len(v) == 4
}

func (s *Server) finYearEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	n, ok := pathYear(r)
	if !ok {
		s.status(w, r, http.StatusNotFound)
		return
	}
	y, err := s.Finanzen.Repo.Year(r.Context(), a.ID, n)
	if err != nil {
		s.finErr(w, r, err)
		return
	}
	s.finYearForm(w, r, http.StatusOK, y, "")
}

func (s *Server) finYearSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	f := &formReader{r: r}
	y := finanzen.Year{Comment: strings.ReplaceAll(f.text("comment"), "\r\n", "\n"), Version: f.version()}
	if r.PathValue("jahr") != "" {
		n, ok := pathYear(r)
		if !ok {
			s.status(w, r, http.StatusNotFound)
			return
		}
		y.Year = n
	} else {
		n, err := strconv.Atoi(f.text("year"))
		if err != nil {
			f.bad = append(f.bad, "Jahr ist keine Zahl")
		}
		y.Year, y.Version = n, 0
	}
	y.IncomeCents = f.cents("income", "Einkommen")
	y.TaxCents = f.cents("tax", "Steuern")
	y.PensionCents = f.cents("pension", "Pensionskasse")
	y.CashCents = f.cents("cash", "Barmittel")
	y.BausparCents = f.cents("bauspar", "Bauspar")
	y.StocksCents = f.cents("stocks", "Aktien")
	y.FundsCents = f.cents("funds", "Fonds")
	y.RealEstateCents = f.cents("real_estate", "Grundstück")
	if m := f.msg(); m != "" {
		s.finYearForm(w, r, http.StatusUnprocessableEntity, y, m)
		return
	}
	saved, err := s.Finanzen.SaveYear(r.Context(), a, y)
	switch {
	case finProblems(err) != "":
		s.finYearForm(w, r, http.StatusUnprocessableEntity, y, finProblems(err))
	case errors.Is(err, finanzen.ErrConflict):
		s.finYearForm(w, r, http.StatusConflict, y, conflictMsg)
	case err != nil:
		s.finErr(w, r, err)
	default:
		s.redirect(w, r, "/finanzen/verlauf", "Jahr "+strconv.Itoa(saved.Year)+" gespeichert.")
	}
}

func (s *Server) finYearDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	n, ok := pathYear(r)
	if !ok || r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	if err := s.Finanzen.Repo.DeleteYear(r.Context(), a.ID, n); err != nil {
		s.finErr(w, r, err)
		return
	}
	s.redirect(w, r, "/finanzen/verlauf", "Jahr gelöscht.")
}
