package webui

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/markdown"
	"github.com/tiliavir/allmend/internal/pages"
)

func (s *Server) seitenRoutes(mux *http.ServeMux) {
	m := func(h func(http.ResponseWriter, *http.Request, account.Account)) http.Handler {
		return s.Module(account.Seiten, h)
	}
	mux.Handle("GET /seiten", m(s.seitenList))
	mux.Handle("GET /seiten/neu", m(s.seitenNew))
	mux.Handle("POST /seiten", m(s.seitenCreate))
	mux.Handle("GET /seiten/{slug}", m(s.seitenView))
	mux.Handle("GET /seiten/{slug}/bearbeiten", m(s.seitenEdit))
	mux.Handle("POST /seiten/{slug}/bearbeiten", m(s.seitenSave))
	mux.Handle("POST /seiten/{slug}/haken", m(s.seitenToggle))
	mux.Handle("GET /seiten/{slug}/rechte", m(s.seitenRights))
	mux.Handle("POST /seiten/{slug}/rechte", m(s.seitenRightsSave))
	mux.Handle("GET /seiten/{slug}/versionen", m(s.seitenRevisions))
	mux.Handle("GET /seiten/{slug}/versionen/{v}", m(s.seitenRevision))
	mux.Handle("POST /seiten/{slug}/versionen/{v}/wiederherstellen", m(s.seitenRestore))
	mux.Handle("POST /seiten/{slug}/loeschen", m(s.seitenDelete))
	mux.Handle("GET /seiten/{slug}/verschieben", m(s.seitenMove))
	mux.Handle("POST /seiten/{slug}/verschieben", m(s.seitenMoveSave))
}

type seiteRow struct {
	Page    pages.Page
	Updated string
	By      string
}

// treeRow is a page in the tree on the list.
type treeRow struct {
	seiteRow
	Movable  bool
	Children []treeRow
}

type seitenData struct {
	Tree      []treeRow
	Pages     []seiteRow
	Parent    *pages.Page  // new page: the page it goes below
	Ancestors []pages.Page // view: breadcrumbs
	Children  []pages.Page // view: pages directly below
	Slots     []slotGroup  // move form
	Page      pages.Page
	HTML      template.HTML
	Updated   string
	By        string
	Owner     string
	CanWrite  bool
	CanManage bool
	// edit form
	Title, Body string
	Base        int
	Current     *pages.Page
	// rights fieldsets (new page, rights form)
	Rights rightsForm
	// revisions
	Revisions []revRow
	Revision  pages.Revision
}

type revRow struct {
	Rev     pages.Revision
	When    string
	By      string
	Current bool
}

func (s *Server) names(r *http.Request) (map[string]string, []account.Account, error) {
	all, err := s.Accounts.Repo.List(r.Context())
	if err != nil {
		return nil, nil, err
	}
	m := map[string]string{}
	for _, a := range all {
		m[a.ID] = a.DisplayName()
	}
	return m, all, nil
}

func (s *Server) when(t time.Time) string { return t.In(s.Location).Format("02.01.2006, 15:04") }

func (s *Server) pageError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, pages.ErrNotFound):
		s.status(w, r, http.StatusNotFound)
	case errors.Is(err, pages.ErrForbidden):
		s.status(w, r, http.StatusForbidden)
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) seitenList(w http.ResponseWriter, r *http.Request, a account.Account) {
	nodes, err := s.Pages.Tree(r.Context(), a)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names, _, err := s.names(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var rows func([]pages.Node) []treeRow
	rows = func(ns []pages.Node) []treeRow {
		var out []treeRow
		for _, n := range ns {
			p := n.Page
			out = append(out, treeRow{seiteRow{p, s.when(p.UpdatedAt), names[p.UpdatedBy]}, p.CanWrite(a), rows(n.Children)})
		}
		return out
	}
	pg := Page{Title: "Seiten", Module: account.Seiten, Data: seitenData{Tree: rows(nodes)}}
	if len(nodes) > 0 {
		pg.Scripts = []Script{{Src: "/static/seiten-baum.js", Module: true}}
	}
	s.render(w, r, http.StatusOK, "seiten/list", pg)
}

func (s *Server) seitenNew(w http.ResponseWriter, r *http.Request, a account.Account) {
	d := seitenData{}
	_, all, err := s.names(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Rights = newRightsForm(pages.Public, a.ID, all, nil, a)
	if slug := r.URL.Query().Get("unter"); slug != "" {
		p, err := s.Pages.Get(r.Context(), a, slug)
		if err != nil {
			s.pageError(w, r, err)
			return
		}
		d.Parent = &p
		d.Rights = newRightsForm(p.ChildRights(a.ID), a.ID, all, &p, a)
	}
	s.render(w, r, http.StatusOK, "seiten/edit", Page{Title: "Neue Seite", Module: account.Seiten, Data: d})
}

func (s *Server) seitenCreate(w http.ResponseWriter, r *http.Request, a account.Account) {
	if err := r.ParseForm(); err != nil {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	parentID := r.PostForm.Get("parent")
	if !pageID(parentID) {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	_, all, err := s.names(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rights, ok := parseRights(r, all)
	if !ok {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	title, body := r.PostForm.Get("title"), r.PostForm.Get("body")
	again := func(msg string, parent *pages.Page) {
		d := seitenData{Title: title, Body: body, Parent: parent, Rights: newRightsForm(rights, a.ID, all, parent, a)}
		s.render(w, r, http.StatusUnprocessableEntity, "seiten/edit", Page{Title: "Neue Seite", Module: account.Seiten,
			Error: msg, Data: d})
	}
	var parent *pages.Page
	if parentID != "" {
		pp, err := s.Pages.ByID(r.Context(), a, parentID)
		switch {
		case errors.Is(err, pages.ErrPlace):
			rights = pages.Public
			again(parentGone, nil)
			return
		case err != nil:
			s.fail(w, r, err)
			return
		}
		parent = &pp
	}
	p, err := s.Pages.Create(r.Context(), a, parentID, title, body, rights)
	switch {
	case errors.Is(err, pages.ErrInvalid):
		again(invalidPage, parent)
	case errors.Is(err, pages.ErrWider):
		rights = parent.ChildRights(a.ID)
		again("Eine Unterseite dürfen höchstens die Leute lesen, die auch „"+parent.Title+"“ lesen dürfen. Prüfe die Rechte und speichere nochmals.", parent)
	case errors.Is(err, pages.ErrPlace):
		rights = pages.Public
		again(parentGone, nil)
	case err != nil:
		s.fail(w, r, err)
	default:
		s.redirect(w, r, "/seiten/"+p.Slug, "Seite angelegt.")
	}
}

const parentGone = "Die übergeordnete Seite gibt es nicht mehr. Die neue Seite kommt nach oben, wenn du nochmals speicherst."

var invalidPage = fmt.Sprintf("Titel fehlt oder ist länger als %d Zeichen, oder der Text ist zu lang.", pages.MaxTitle)

func (s *Server) seitenView(w http.ResponseWriter, r *http.Request, a account.Account) {
	p, err := s.Pages.Get(r.Context(), a, r.PathValue("slug"))
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	tasks := markdown.Tasks{Text: pages.TaskText, Version: p.Version}
	if p.CanWrite(a) {
		tasks.Action = "/seiten/" + p.Slug + "/haken"
	}
	res, err := markdown.Render(p.Body, tasks)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names, _, err := s.names(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	up, err := s.Pages.Ancestors(r.Context(), a, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	down, err := s.Pages.Children(r.Context(), a, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pg := Page{Title: p.Title, Module: account.Seiten, Data: seitenData{
		Page: p, HTML: res.HTML, Ancestors: up, Children: down, Updated: s.when(p.UpdatedAt), By: names[p.UpdatedBy], Owner: names[p.OwnerID],
		CanWrite: p.CanWrite(a), CanManage: p.CanManage(a),
	}}
	if res.HasMermaid {
		pg.Scripts = append(pg.Scripts, Script{Src: "/static/vendor/mermaid.min.js"})
	}
	if res.HasMermaid || res.HasMarkmap { // seiten.js imports Markmap itself, only when needed
		pg.Scripts = append(pg.Scripts, Script{Src: "/static/seiten.js", Module: true})
		// Mermaid and Markmap add <style> elements; scripts stay strictly 'self'.
		w.Header().Set(cspHeader, cspDiagram)
	}
	s.render(w, r, http.StatusOK, "seiten/view", pg)
}

func (s *Server) seitenEdit(w http.ResponseWriter, r *http.Request, a account.Account) {
	p, err := s.Pages.Get(r.Context(), a, r.PathValue("slug"))
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	if !p.CanWrite(a) {
		s.status(w, r, http.StatusForbidden)
		return
	}
	s.render(w, r, http.StatusOK, "seiten/edit", Page{Title: p.Title + " bearbeiten", Module: account.Seiten,
		Data: seitenData{Page: p, Title: p.Title, Body: p.Body, Base: p.Version}})
}

func (s *Server) seitenSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	slug := r.PathValue("slug")
	base, err := strconv.Atoi(r.PostFormValue("base"))
	if err != nil {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	title, body := r.PostFormValue("title"), r.PostFormValue("body")
	p, err := s.Pages.Save(r.Context(), a, slug, base, title, body)
	switch {
	case errors.Is(err, pages.ErrConflict):
		cur := p
		s.render(w, r, http.StatusConflict, "seiten/edit", Page{Title: p.Title + " bearbeiten", Module: account.Seiten,
			Error: fmt.Sprintf("Jemand hat die Seite inzwischen geändert (jetzt Version %d). Deine Fassung steht unten im Formular, die aktuelle darunter – übernimm, was fehlt, und speichere dann.", p.Version),
			Data:  seitenData{Page: p, Title: title, Body: body, Base: p.Version, Current: &cur}})
		return
	case errors.Is(err, pages.ErrInvalid):
		s.render(w, r, http.StatusUnprocessableEntity, "seiten/edit", Page{Title: p.Title + " bearbeiten", Module: account.Seiten,
			Error: invalidPage, Data: seitenData{Page: p, Title: title, Body: body, Base: base}})
		return
	case err != nil:
		s.pageError(w, r, err)
		return
	}
	s.redirect(w, r, "/seiten/"+p.Slug, "Gespeichert.")
}

func (s *Server) seitenToggle(w http.ResponseWriter, r *http.Request, a account.Account) {
	slug := r.PathValue("slug")
	version, err1 := strconv.Atoi(r.PostFormValue("version"))
	line, err2 := strconv.Atoi(r.PostFormValue("line"))
	if err1 != nil || err2 != nil {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	_, err := s.Pages.Toggle(r.Context(), a, slug, version, line, r.PostFormValue("text"))
	back := "/seiten/" + slug
	switch {
	case errors.Is(err, pages.ErrNoTask):
		s.redirect(w, r, back, "Diesen Eintrag gibt es nicht mehr – die Seite wurde inzwischen geändert.")
	case errors.Is(err, pages.ErrAmbiguous):
		s.redirect(w, r, back, "Den Eintrag gibt es mehrfach – bitte in „Bearbeiten“ abhaken.")
	case errors.Is(err, pages.ErrConflict):
		s.redirect(w, r, back, "Die Seite wurde gerade geändert – bitte nochmals abhaken.")
	case err != nil:
		s.pageError(w, r, err)
	default:
		s.redirect(w, r, back+"#aufgabe-"+strconv.Itoa(line), "")
	}
}

func (s *Server) seitenRights(w http.ResponseWriter, r *http.Request, a account.Account) {
	p, err := s.Pages.Get(r.Context(), a, r.PathValue("slug"))
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	if !p.CanManage(a) {
		s.status(w, r, http.StatusForbidden)
		return
	}
	s.rightsPage(w, r, a, p, http.StatusOK, "")
}

// rightsPage renders the rights form of p, with its current rights.
func (s *Server) rightsPage(w http.ResponseWriter, r *http.Request, a account.Account, p pages.Page, status int, msg string) {
	names, all, err := s.names(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var limit *pages.Page
	parent, err := s.Pages.Parent(r.Context(), p)
	switch {
	case err == nil:
		limit = &parent
	case !errors.Is(err, pages.ErrNotFound):
		s.fail(w, r, err)
		return
	}
	d := seitenData{Page: p, Owner: names[p.OwnerID],
		Rights: newRightsForm(pages.Rights{ReadAll: p.ReadAll, WriteAll: p.WriteAll, Readers: p.Readers, Writers: p.Writers}, p.OwnerID, all, limit, a)}
	s.render(w, r, status, "seiten/rights", Page{Title: "Rechte: " + p.Title, Module: account.Seiten, Error: msg, Data: d})
}

func (s *Server) seitenRightsSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	if err := r.ParseForm(); err != nil {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	_, all, err := s.names(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rights, ok := parseRights(r, all)
	if !ok {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	p, changed, err := s.Pages.SetRights(r.Context(), a, r.PathValue("slug"), rights)
	var locked *pages.LockedError
	switch {
	case errors.Is(err, pages.ErrWider):
		s.rightsPage(w, r, a, p, http.StatusUnprocessableEntity, "Diese Seite dürfen höchstens die Leute lesen, die auch die übergeordnete Seite lesen dürfen. Die Liste hat sich vielleicht inzwischen geändert.")
		return
	case errors.As(err, &locked):
		s.rightsPage(w, r, a, p, http.StatusUnprocessableEntity, lockedMessage(a, locked))
		return
	case err != nil:
		s.pageError(w, r, err)
		return
	}
	s.redirect(w, r, "/seiten/"+p.Slug, "Rechte gespeichert."+narrowedMessage(a, changed))
}

// lockedMessage explains a refused narrowing without naming pages a may not read.
func lockedMessage(a account.Account, e *pages.LockedError) string {
	return "Das geht nicht: Wer " + titles(a, e.Pages, "Unterseite", "Unterseiten") +
		" angelegt hat, dürfte die eigene Seite dann nicht mehr lesen. Gib diesen Personen Leserechte oder verschiebe die Unterseiten zuerst."
}

// narrowedMessage names the pages whose rights were narrowed along, if any.
func narrowedMessage(a account.Account, changed []pages.Page) string {
	if len(changed) == 0 {
		return ""
	}
	return " Angepasst, damit sie nicht mehr Leute lesen als die Seite darüber: " + titles(a, changed, "Unterseite", "Unterseiten") + "."
}

// titles lists the titles of the pages a may read and counts the others.
func titles(a account.Account, ps []pages.Page, one, many string) string {
	var names []string
	hidden := 0
	for _, p := range ps {
		if p.CanRead(a) {
			names = append(names, "„"+p.Title+"“")
		} else {
			hidden++
		}
	}
	switch {
	case hidden == 0:
	case len(names) == 0 && hidden == 1:
		names = append(names, "eine "+one)
	case len(names) == 0:
		names = append(names, strconv.Itoa(hidden)+" "+many)
	case hidden == 1:
		names = append(names, "eine weitere "+one)
	default:
		names = append(names, strconv.Itoa(hidden)+" weitere "+many)
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " und " + names[len(names)-1]
}

// rightsForm fills the read/edit fieldsets ("seitenrechte" in partials.html).
type rightsForm struct {
	ReadAll, WriteAll bool
	Readers, Writers  map[string]bool
	Candidates        []account.Account // everyone who may get rights, without the owner
	Limited           bool              // below a page not everyone may read: only its readers
	LimitTitle        string            // that page's title, if the viewer may read it
}

// newRightsForm offers rights for a page owned by owner below limit (nil for
// the top level), shown to viewer.
func newRightsForm(r pages.Rights, owner string, all []account.Account, limit *pages.Page, viewer account.Account) rightsForm {
	f := rightsForm{ReadAll: r.ReadAll || r.WriteAll, WriteAll: r.WriteAll, Readers: set(r.Readers), Writers: set(r.Writers)}
	aud := pages.Audience{All: true}
	if limit != nil {
		aud = limit.Audience()
		if limit.CanRead(viewer) {
			f.LimitTitle = limit.Title
		}
	}
	if !aud.All {
		f.Limited, f.ReadAll, f.WriteAll = true, false, false
	}
	for _, c := range all {
		if c.ID != owner && c.Can(account.Seiten) && c.Status != account.Disabled && aud.Has(c.ID) {
			f.Candidates = append(f.Candidates, c)
		}
	}
	return f
}

// parseRights reads the fieldsets from a parsed form. Both choices are
// required and every account must exist; anything else is a bad request.
func parseRights(r *http.Request, all []account.Account) (pages.Rights, bool) {
	read, write := r.PostForm.Get("read"), r.PostForm.Get("write")
	if !slices.Contains([]string{"all", "some"}, read) || !slices.Contains([]string{"all", "some"}, write) {
		return pages.Rights{}, false
	}
	known := map[string]bool{}
	for _, c := range all {
		known[c.ID] = true
	}
	for _, id := range append(slices.Clone(r.PostForm["reader"]), r.PostForm["writer"]...) {
		if !known[id] {
			return pages.Rights{}, false
		}
	}
	return pages.Rights{ReadAll: read == "all", WriteAll: write == "all", Readers: r.PostForm["reader"], Writers: r.PostForm["writer"]}, true
}

func set(ids []string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func (s *Server) seitenRevisions(w http.ResponseWriter, r *http.Request, a account.Account) {
	p, revs, err := s.Pages.Revisions(r.Context(), a, r.PathValue("slug"))
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	names, _, err := s.names(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := seitenData{Page: p, CanWrite: p.CanWrite(a)}
	for _, v := range revs {
		d.Revisions = append(d.Revisions, revRow{v, s.when(v.CreatedAt), names[v.AuthorID], v.Version == p.Version})
	}
	s.render(w, r, http.StatusOK, "seiten/revisions", Page{Title: "Versionen: " + p.Title, Module: account.Seiten, Data: d})
}

func (s *Server) findRevision(w http.ResponseWriter, r *http.Request, a account.Account) (pages.Page, pages.Revision, bool) {
	v, err := strconv.Atoi(r.PathValue("v"))
	if err != nil {
		s.status(w, r, http.StatusNotFound)
		return pages.Page{}, pages.Revision{}, false
	}
	p, revs, err := s.Pages.Revisions(r.Context(), a, r.PathValue("slug"))
	if err != nil {
		s.pageError(w, r, err)
		return p, pages.Revision{}, false
	}
	i := slices.IndexFunc(revs, func(x pages.Revision) bool { return x.Version == v })
	if i < 0 {
		s.status(w, r, http.StatusNotFound)
		return p, pages.Revision{}, false
	}
	return p, revs[i], true
}

func (s *Server) seitenRevision(w http.ResponseWriter, r *http.Request, a account.Account) {
	p, rev, ok := s.findRevision(w, r, a)
	if !ok {
		return
	}
	res, err := markdown.Render(rev.Body, markdown.Tasks{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names, _, err := s.names(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "seiten/revision", Page{Title: fmt.Sprintf("%s – Version %d", p.Title, rev.Version), Module: account.Seiten,
		Data: seitenData{Page: p, Revision: rev, HTML: res.HTML, Updated: s.when(rev.CreatedAt), By: names[rev.AuthorID], CanWrite: p.CanWrite(a)}})
}

func (s *Server) seitenRestore(w http.ResponseWriter, r *http.Request, a account.Account) {
	p, rev, ok := s.findRevision(w, r, a)
	if !ok {
		return
	}
	_, err := s.Pages.Save(r.Context(), a, p.Slug, p.Version, rev.Title, rev.Body)
	if errors.Is(err, pages.ErrConflict) {
		s.redirect(w, r, "/seiten/"+p.Slug+"/versionen", "Die Seite wurde gerade geändert – bitte nochmals versuchen.")
		return
	}
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	s.redirect(w, r, "/seiten/"+p.Slug, fmt.Sprintf("Version %d wiederhergestellt.", rev.Version))
}

func (s *Server) seitenDelete(w http.ResponseWriter, r *http.Request, a account.Account) {
	if r.PostFormValue("confirm") != "1" {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	if err := s.Pages.Delete(r.Context(), a, r.PathValue("slug")); err != nil {
		s.pageError(w, r, err)
		return
	}
	s.redirect(w, r, "/seiten", "Seite gelöscht.")
}

// pageID reports whether v is empty or looks like a page id (32 hex digits).
func pageID(v string) bool {
	if v == "" {
		return true
	}
	if len(v) != 32 {
		return false
	}
	for _, c := range v {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// slotGroup lists the places below one parent where a page can go.
type slotGroup struct {
	Label string
	Slots []slot
}

type slot struct {
	Value, Label string
	Selected     bool
}

// moveSlots lists every place the page p can be moved to: before each
// sibling and last, at the top level and below every page outside p's own
// subtree. The current place is selected.
func moveSlots(nodes []pages.Node, p pages.Page) []slotGroup {
	var out []slotGroup
	var walk func(parent, label string, ns []pages.Node)
	walk = func(parent, label string, ns []pages.Node) {
		g := slotGroup{Label: label}
		here := false // p comes right before the next slot
		for _, n := range ns {
			switch {
			case n.Page.ID == p.ID:
				here = p.ParentID == parent
			case n.Page.ParentID == parent: // not a page whose parent is hidden
				g.Slots = append(g.Slots, slot{parent + "/" + n.Page.ID, "vor „" + n.Page.Title + "“", here})
				here = false
			}
		}
		last := "als letzte"
		if len(g.Slots) == 0 {
			last = "hierhin"
		}
		g.Slots = append(g.Slots, slot{parent + "/", last, here})
		out = append(out, g)
		for _, n := range ns {
			if n.Page.ID == p.ID {
				continue
			}
			path := n.Page.Title
			if parent != "" {
				path = strings.TrimSuffix(strings.TrimPrefix(label, "Unter „"), "“") + " › " + n.Page.Title
			}
			walk(n.Page.ID, "Unter „"+path+"“", n.Children)
		}
	}
	walk("", "Oberste Ebene", nodes)
	return out
}

func (s *Server) seitenMove(w http.ResponseWriter, r *http.Request, a account.Account) {
	p, err := s.Pages.Get(r.Context(), a, r.PathValue("slug"))
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	if !p.CanWrite(a) {
		s.status(w, r, http.StatusForbidden)
		return
	}
	nodes, err := s.Pages.Tree(r.Context(), a)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "seiten/move", Page{Title: "Verschieben: " + p.Title, Module: account.Seiten,
		Data: seitenData{Page: p, Slots: moveSlots(nodes, p)}})
}

func (s *Server) seitenMoveSave(w http.ResponseWriter, r *http.Request, a account.Account) {
	slug := r.PathValue("slug")
	parent, before, ok := strings.Cut(r.PostFormValue("ziel"), "/")
	back := r.PostFormValue("zurueck")
	if !ok || !pageID(parent) || !pageID(before) || (back != "liste" && back != "seite") {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	p, changed, err := s.Pages.Move(r.Context(), a, slug, parent, before)
	var locked *pages.LockedError
	switch {
	case errors.Is(err, pages.ErrPlace):
		s.redirect(w, r, "/seiten", "Dorthin lässt sich die Seite nicht verschieben – die Liste hat sich vielleicht inzwischen geändert.")
		return
	case errors.Is(err, pages.ErrManage):
		s.redirect(w, r, "/seiten/"+slug, "Dort dürften weniger Leute die Seite lesen. Das darf nur, wer sie angelegt hat.")
		return
	case errors.As(err, &locked):
		s.redirect(w, r, "/seiten/"+slug, "Nicht verschoben: "+lockedMessage(a, locked))
		return
	case err != nil:
		s.pageError(w, r, err)
		return
	}
	msg := strings.TrimSpace(narrowedMessage(a, changed))
	if back == "liste" {
		s.redirect(w, r, "/seiten#seite-"+p.ID, msg)
		return
	}
	s.redirect(w, r, "/seiten/"+p.Slug, strings.TrimSpace("Verschoben. "+msg))
}
