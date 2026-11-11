package webui

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/google"
	"github.com/tiliavir/allmend/internal/links"
	"github.com/tiliavir/allmend/internal/pages"
)

// ── start page ────────────────────────────────────────

type startData struct {
	Today, Greeting string
	Links           []links.Link
	Modules         []account.ModuleInfo
	Pages           []pages.Page
}

var weekdays = [...]string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}
var months = [...]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	a := currentAccount(r)
	now := time.Now().In(s.Location)
	ls, err := s.Links.Visible(r.Context(), a != nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := startData{
		Today:    weekdays[now.Weekday()] + ", " + strconv.Itoa(now.Day()) + ". " + months[now.Month()-1],
		Greeting: greeting(now.Hour()),
		Links:    ls,
	}
	if a != nil {
		for _, m := range account.Modules {
			if m.Ready && a.Can(m.ID) {
				d.Modules = append(d.Modules, m)
			}
		}
		if a.Can(account.Seiten) {
			ps, err := s.Pages.Recent(r.Context(), *a, 5)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			d.Pages = ps
		}
	}
	s.render(w, r, http.StatusOK, "start", Page{Data: d})
}

func greeting(h int) string {
	switch {
	case h < 11:
		return "Guten Morgen"
	case h < 18:
		return "Guten Tag"
	default:
		return "Guten Abend"
	}
}

// ── login ─────────────────────────────────────────────

type loginData struct {
	Google bool
	Email  string
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if currentAccount(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login", Page{Title: "Anmelden", Data: loginData{Google: s.Google != nil}})
}

func (s *Server) loginLocal(w http.ResponseWriter, r *http.Request) {
	email, pw := r.PostFormValue("email"), r.PostFormValue("password")
	a, err := s.Accounts.LoginLocal(r.Context(), email, pw)
	if errors.Is(err, account.ErrLoginFailed) {
		slog.Warn("local login failed")
		s.render(w, r, http.StatusUnauthorized, "login", Page{Title: "Anmelden", Error: "E-Mail oder Passwort stimmt nicht.", Data: loginData{Google: s.Google != nil, Email: email}})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Sessions.Start(r.Context(), w, a.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	slog.Info("login", "method", "local", "account", a.ID)
	to := "/"
	if a.PasswordMustChange {
		to = "/konto"
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) loginGoogle(w http.ResponseWriter, r *http.Request) {
	if s.Google == nil {
		s.status(w, r, http.StatusNotFound)
		return
	}
	f, err := s.Sessions.NewFlow(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, s.Google.AuthCodeURL(f.State, f.Nonce, f.Verifier), http.StatusSeeOther)
}

func (s *Server) loginGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if s.Google == nil {
		s.status(w, r, http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	denied := func(msg string) {
		s.render(w, r, http.StatusUnauthorized, "login", Page{Title: "Anmelden", Error: msg, Data: loginData{Google: true}})
	}
	f, err := s.Sessions.TakeFlow(r.Context(), q.Get("state"))
	if err != nil {
		denied("Die Anmeldung ist abgelaufen. Bitte versuche es nochmals.")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		denied("Die Anmeldung bei Google wurde abgebrochen.")
		return
	}
	c, err := s.Google.Login(r.Context(), q.Get("code"), f.Verifier, f.Nonce)
	if err != nil {
		slog.Warn("google login rejected", "err", err)
		if errors.Is(err, google.ErrInvalid) {
			denied("Google hat die Anmeldung nicht bestätigt.")
			return
		}
		denied("Google ist gerade nicht erreichbar. Mit Passwort geht es trotzdem, falls eines gesetzt ist.")
		return
	}
	a, err := s.Accounts.LoginGoogle(r.Context(), account.GoogleIdentity{Subject: c.Subject, Email: c.Email, Name: c.Name, EmailVerified: c.EmailVerified})
	if errors.Is(err, account.ErrLoginFailed) {
		slog.Warn("google login without account")
		denied("Für dieses Google-Konto gibt es keinen Zugang. Ein Admin kann dich einladen.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Sessions.Start(r.Context(), w, a.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	slog.Info("login", "method", "google", "account", a.ID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.Sessions.End(w, r); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ── own account ───────────────────────────────────────

type kontoData struct {
	MinLength int
	Themes    []account.Theme
}

func newKontoData() kontoData { return kontoData{account.MinPasswordLength, account.Themes} }

func (s *Server) konto(w http.ResponseWriter, r *http.Request, _ account.Account) {
	s.render(w, r, http.StatusOK, "konto", Page{Title: "Konto", Data: newKontoData()})
}

func (s *Server) changeTheme(w http.ResponseWriter, r *http.Request, a account.Account) {
	_, err := s.Accounts.SetTheme(r.Context(), a, account.Theme(r.PostFormValue("theme")))
	if errors.Is(err, account.ErrUnknownTheme) {
		s.render(w, r, http.StatusUnprocessableEntity, "konto", Page{Title: "Konto", Error: "Bitte wähle eine der Darstellungen.", Data: newKontoData()})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/konto", "Darstellung gespeichert.")
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, a account.Account) {
	_, err := s.Accounts.ChangePassword(r.Context(), a, r.PostFormValue("current"), r.PostFormValue("next"))
	var msg string
	switch {
	case errors.Is(err, account.ErrWeakPassword):
		msg = "Das neue Passwort ist zu kurz."
	case errors.Is(err, account.ErrLoginFailed):
		msg = "Das aktuelle Passwort stimmt nicht."
	case err != nil:
		s.fail(w, r, err)
		return
	default:
		s.redirect(w, r, "/konto", "Passwort gespeichert.")
		return
	}
	s.render(w, r, http.StatusUnprocessableEntity, "konto", Page{Title: "Konto", Error: msg, Data: newKontoData()})
}

// ── admin: accounts ───────────────────────────────────

type accountsData struct {
	Accounts   []account.Account
	AllModules []account.ModuleInfo
	Selected   map[account.Module]bool
	Target     account.Account
	Statuses   []account.Status
	OneTime    string
	Self       bool // Target is the acting admin
}

func (s *Server) adminIndex(w http.ResponseWriter, r *http.Request, _ account.Account) {
	s.render(w, r, http.StatusOK, "admin/index", Page{Title: "Verwaltung"})
}

func (s *Server) adminAccounts(w http.ResponseWriter, r *http.Request, _ account.Account) {
	s.renderAccounts(w, r, http.StatusOK, "")
}

func (s *Server) renderAccounts(w http.ResponseWriter, r *http.Request, status int, errMsg string) {
	all, err := s.Accounts.Repo.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, status, "admin/accounts", Page{Title: "Konten", Error: errMsg, Data: accountsData{Accounts: all, AllModules: account.Modules, Selected: map[account.Module]bool{}}})
}

func changesFromForm(r *http.Request) account.Changes {
	_ = r.ParseForm()
	c := account.Changes{
		Email:   r.PostForm.Get("email"),
		Name:    strings.TrimSpace(r.PostForm.Get("name")),
		IsAdmin: r.PostForm.Get("admin") == "1",
		Status:  account.Status(r.PostForm.Get("status")),
	}
	for _, m := range r.PostForm["module"] {
		c.Modules = append(c.Modules, account.Module(m))
	}
	return c
}

func accountError(err error) string {
	switch {
	case errors.Is(err, account.ErrInvalidEmail):
		return "Das ist keine gültige E-Mail-Adresse."
	case errors.Is(err, account.ErrEmailTaken):
		return "Diese E-Mail-Adresse gehört schon zu einem Konto."
	case errors.Is(err, account.ErrUnknownMod):
		return "Unbekanntes Modul."
	case errors.Is(err, account.ErrLastAdmin):
		return "Es muss immer mindestens ein aktiver Admin bleiben."
	case errors.Is(err, account.ErrSelf):
		return "Du kannst dich nicht selbst sperren oder löschen."
	case errors.Is(err, account.ErrHasData):
		return "Das Konto hat noch Daten (z. B. Touren, Seiten, Spiele oder Ausgaben). Sperre es stattdessen."
	}
	return ""
}

func (s *Server) adminInvite(w http.ResponseWriter, r *http.Request, actor account.Account) {
	c := changesFromForm(r)
	if len([]rune(c.Name)) > 60 {
		s.renderAccounts(w, r, http.StatusUnprocessableEntity, "Der Name ist zu lang.")
		return
	}
	a, err := s.Accounts.Invite(r.Context(), actor, c)
	if msg := accountError(err); msg != "" {
		s.renderAccounts(w, r, http.StatusUnprocessableEntity, msg)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slog.Info("account invited", "account", a.ID, "by", actor.ID)
	s.redirect(w, r, "/verwaltung/konten", a.Email+" ist eingeladen.")
}

func (s *Server) adminAccount(w http.ResponseWriter, r *http.Request, _ account.Account) {
	s.renderAccount(w, r, http.StatusOK, r.PathValue("id"), "", "")
}

func (s *Server) renderAccount(w http.ResponseWriter, r *http.Request, status int, id, errMsg, oneTime string) {
	a, err := s.Accounts.Repo.Get(r.Context(), id)
	if errors.Is(err, account.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sel := map[account.Module]bool{}
	for _, m := range a.Modules {
		sel[m] = true
	}
	s.render(w, r, status, "admin/account", Page{Title: a.DisplayName(), Error: errMsg, Data: accountsData{
		Target: a, AllModules: account.Modules, Selected: sel, OneTime: oneTime,
		Self:     currentAccount(r) != nil && currentAccount(r).ID == a.ID,
		Statuses: []account.Status{account.Invited, account.Active, account.Disabled},
	}})
}

func (s *Server) adminAccountSave(w http.ResponseWriter, r *http.Request, actor account.Account) {
	id := r.PathValue("id")
	c := changesFromForm(r)
	if len([]rune(c.Name)) > 60 {
		s.renderAccount(w, r, http.StatusUnprocessableEntity, id, "Der Name ist zu lang.", "")
		return
	}
	a, err := s.Accounts.Update(r.Context(), actor, id, c)
	if errors.Is(err, account.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if msg := accountError(err); msg != "" {
		s.renderAccount(w, r, http.StatusUnprocessableEntity, id, msg, "")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slog.Info("account updated", "account", a.ID, "by", actor.ID)
	s.redirect(w, r, "/verwaltung/konten/"+a.ID, "Gespeichert.")
}

func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request, actor account.Account) {
	id := r.PathValue("id")
	pw, err := s.Accounts.ResetPassword(r.Context(), actor, id)
	if errors.Is(err, account.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if msg := accountError(err); msg != "" {
		s.renderAccount(w, r, http.StatusUnprocessableEntity, id, msg, "")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slog.Info("one-time password set", "account", id, "by", actor.ID)
	w.Header().Set("Cache-Control", "no-store")
	s.renderAccount(w, r, http.StatusOK, id, "", pw)
}

func (s *Server) adminRemovePassword(w http.ResponseWriter, r *http.Request, actor account.Account) {
	id := r.PathValue("id")
	err := s.Accounts.RemovePassword(r.Context(), actor, id)
	if errors.Is(err, account.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/verwaltung/konten/"+id, "Passwort-Anmeldung entfernt.")
}

func (s *Server) adminAccountDelete(w http.ResponseWriter, r *http.Request, actor account.Account) {
	id := r.PathValue("id")
	if r.PostFormValue("confirm") != "1" {
		s.renderAccount(w, r, http.StatusUnprocessableEntity, id, "Bitte bestätige das Löschen.", "")
		return
	}
	err := s.Accounts.Delete(r.Context(), actor, id)
	if errors.Is(err, account.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if msg := accountError(err); msg != "" {
		s.renderAccount(w, r, http.StatusUnprocessableEntity, id, msg, "")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slog.Info("account deleted", "account", id, "by", actor.ID)
	s.redirect(w, r, "/verwaltung/konten", "Konto gelöscht.")
}

// ── admin: links ──────────────────────────────────────

type linksData struct {
	Links []links.Link
	New   links.Link
	Link  links.Link
}

func linkFromForm(r *http.Request) links.Link {
	return links.Link{
		Name: r.PostFormValue("name"), URL: r.PostFormValue("url"), Description: r.PostFormValue("description"),
		Icon: r.PostFormValue("icon"), Public: r.PostFormValue("public") == "1",
	}
}

func linkError(err error) string {
	var inv *links.InvalidError
	if errors.As(err, &inv) {
		return strings.Join(inv.Problems, ". ") + "."
	}
	return ""
}

func (s *Server) adminLinks(w http.ResponseWriter, r *http.Request, _ account.Account) {
	s.renderLinks(w, r, http.StatusOK, links.Link{Icon: "link"}, "")
}

func (s *Server) renderLinks(w http.ResponseWriter, r *http.Request, status int, draft links.Link, errMsg string) {
	all, err := s.Links.Repo.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, status, "admin/links", Page{Title: "Links", Error: errMsg, Data: linksData{Links: all, New: draft}})
}

func (s *Server) adminLinkCreate(w http.ResponseWriter, r *http.Request, _ account.Account) {
	l, err := s.Links.Create(r.Context(), linkFromForm(r))
	if msg := linkError(err); msg != "" {
		s.renderLinks(w, r, http.StatusUnprocessableEntity, l, msg)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/verwaltung/links", l.Name+" hinzugefügt.")
}

func (s *Server) adminLink(w http.ResponseWriter, r *http.Request, _ account.Account) {
	l, err := s.Links.Repo.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, links.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin/link", Page{Title: l.Name, Data: linksData{Link: l}})
}

func (s *Server) adminLinkSave(w http.ResponseWriter, r *http.Request, _ account.Account) {
	in := linkFromForm(r)
	in.ID = r.PathValue("id")
	l, err := s.Links.Save(r.Context(), in)
	if errors.Is(err, links.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if msg := linkError(err); msg != "" {
		s.render(w, r, http.StatusUnprocessableEntity, "admin/link", Page{Title: in.Name, Error: msg, Data: linksData{Link: l}})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/verwaltung/links", l.Name+" gespeichert.")
}

func (s *Server) adminLinkMove(delta int) func(http.ResponseWriter, *http.Request, account.Account) {
	return func(w http.ResponseWriter, r *http.Request, _ account.Account) {
		err := s.Links.Move(r.Context(), r.PathValue("id"), delta)
		if errors.Is(err, links.ErrNotFound) {
			s.status(w, r, http.StatusNotFound)
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.redirect(w, r, "/verwaltung/links", "")
	}
}

func (s *Server) adminLinkDelete(w http.ResponseWriter, r *http.Request, _ account.Account) {
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	err := s.Links.Delete(r.Context(), r.PathValue("id"))
	if errors.Is(err, links.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/verwaltung/links", "Link gelöscht.")
}
