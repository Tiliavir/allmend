package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/commute"
	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/links"
	"github.com/tiliavir/allmend/internal/migrations"
	"github.com/tiliavir/allmend/internal/pages"
	"github.com/tiliavir/allmend/internal/session"
	"github.com/tiliavir/allmend/internal/store"
)

func open(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "allmend.db")
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := db.Migrate(ctx, d, migrations.All(), db.Options{BackupDir: filepath.Join(dir, "backup")}); err != nil {
		t.Fatal(err)
	}
	return store.New(d), dir
}

func TestMigrateIdempotentWithBackup(t *testing.T) {
	t.Parallel()
	s, dir := open(t)
	ctx := context.Background()
	res, err := db.Migrate(ctx, s.DB(), migrations.All(), db.Options{BackupDir: filepath.Join(dir, "backup")})
	if err != nil || len(res.Applied) != 0 {
		t.Fatalf("second run: %v %v", res, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "backup"))
	if len(entries) != 1 {
		t.Fatalf("want 1 backup, got %d", len(entries))
	}
}

func TestMigrateRollsBackOnError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	ctx := context.Background()
	d, _ := db.Open(ctx, path)
	defer func() { _ = d.Close() }()
	ms := []db.Migration{
		{Version: 1, Name: "ok", Up: db.SQL(`CREATE TABLE a (x INTEGER) STRICT`)},
		{Version: 2, Name: "bad", Up: db.SQL(`CREATE TABLE b (`)},
	}
	if _, err := db.Migrate(ctx, d, ms, db.Options{BackupDir: dir}); err == nil {
		t.Fatal("want error")
	}
	if v, _ := db.CurrentVersion(ctx, d); v != 0 {
		t.Fatalf("version %d after failed run", v)
	}
	var n int
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'a'`).Scan(&n)
	if n != 0 {
		t.Fatal("table a survived rollback")
	}
}

func TestRefusesNewerDatabase(t *testing.T) {
	t.Parallel()
	s, dir := open(t)
	ctx := context.Background()
	_, _ = s.DB().ExecContext(ctx, `INSERT INTO schema_migrations VALUES (999, 'future', '2030-01-01T00:00:00Z')`)
	if _, err := db.Migrate(ctx, s.DB(), migrations.All(), db.Options{BackupDir: dir}); err == nil {
		t.Fatal("older binary must refuse a newer database")
	}
}

func TestAccounts(t *testing.T) {
	t.Parallel()
	s, _ := open(t)
	ctx := context.Background()
	r := s.Accounts()
	now := time.Now().UTC()
	admin := account.Account{ID: "a1", Email: "admin@example.org", IsAdmin: true, Status: account.Active, Theme: account.ThemeAuto, Modules: []account.Module{account.Velo}, CreatedAt: now, UpdatedAt: now}
	if err := r.Insert(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := r.Insert(ctx, account.Account{ID: "a2", Email: "ADMIN@example.org", Status: account.Invited, Theme: account.ThemeAuto, CreatedAt: now, UpdatedAt: now}); !errors.Is(err, account.ErrEmailTaken) {
		t.Fatalf("duplicate e-mail: %v", err)
	}
	if err := r.Insert(ctx, account.Account{ID: "a3", Email: "x@example.org", Status: account.Active, Theme: account.ThemeAuto, Modules: []account.Module{"nope"}, CreatedAt: now, UpdatedAt: now}); !errors.Is(err, account.ErrUnknownMod) {
		t.Fatalf("unknown module: %v", err)
	}
	admin.IsAdmin = false
	if err := r.Update(ctx, admin); !errors.Is(err, account.ErrLastAdmin) {
		t.Fatalf("last admin: %v", err)
	}
	got, err := r.ByEmail(ctx, "Admin@Example.org")
	if err != nil || !got.IsAdmin || len(got.Modules) != 1 {
		t.Fatalf("by email: %+v %v", got, err)
	}
}

func TestSessionsAndFlows(t *testing.T) {
	t.Parallel()
	s, _ := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_ = s.Accounts().Insert(ctx, account.Account{ID: "a1", Email: "a@example.org", IsAdmin: true, Status: account.Active, Theme: account.ThemeAuto, CreatedAt: now, UpdatedAt: now})
	r := s.Sessions()
	if err := r.Create(ctx, "h1", "a1", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if acc, err := r.Lookup(ctx, "h1", now, now.Add(time.Hour)); err != nil || acc != "a1" {
		t.Fatalf("lookup: %q %v", acc, err)
	}
	if _, err := r.Lookup(ctx, "h1", now.Add(2*time.Hour), now.Add(3*time.Hour)); !errors.Is(err, session.ErrNoSession) {
		t.Fatalf("expired: %v", err)
	}
	f := session.Flow{State: "s", Nonce: "n", Verifier: "v", Created: now}
	_ = r.SaveFlow(ctx, f)
	if got, err := r.TakeFlow(ctx, "s"); err != nil || got.Nonce != "n" {
		t.Fatalf("take: %v", err)
	}
	if _, err := r.TakeFlow(ctx, "s"); !errors.Is(err, session.ErrNoSession) {
		t.Fatal("flow must be single use")
	}
}

func TestLinksOrder(t *testing.T) {
	t.Parallel()
	s, _ := open(t)
	ctx := context.Background()
	svc := links.Service{Repo: s.Links()}
	a, err := svc.Create(ctx, links.Link{Name: "A", URL: "https://a.example", Icon: "link"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := svc.Create(ctx, links.Link{Name: "B", URL: "https://b.example", Icon: "link", Public: true})
	if err := svc.Move(ctx, b.ID, -1); err != nil {
		t.Fatal(err)
	}
	all, _ := svc.Visible(ctx, true)
	if all[0].ID != b.ID || all[1].ID != a.ID {
		t.Fatal("move failed")
	}
	pub, _ := svc.Visible(ctx, false)
	if len(pub) != 1 || pub[0].ID != b.ID {
		t.Fatal("public filter failed")
	}
	if _, err := svc.Create(ctx, links.Link{Name: "X", URL: "javascript:alert(1)", Icon: "link"}); !errors.Is(err, links.ErrInvalid) {
		t.Fatal("javascript URL accepted")
	}
}

func TestPages(t *testing.T) {
	t.Parallel()
	s, _ := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := account.Account{ID: "o", Email: "o@example.org", IsAdmin: true, Status: account.Active, Theme: account.ThemeAuto, CreatedAt: now, UpdatedAt: now}
	other := account.Account{ID: "x", Email: "x@example.org", Status: account.Active, Theme: account.ThemeAuto, CreatedAt: now, UpdatedAt: now}
	_ = s.Accounts().Insert(ctx, owner)
	_ = s.Accounts().Insert(ctx, other)
	svc := pages.Service{Repo: s.Pages()}

	p, err := svc.Create(ctx, owner, "", "Einkaufsliste", "- [ ] Milch\n- [ ] Brot\n", pages.Public)
	if err != nil || p.Slug != "einkaufsliste" {
		t.Fatalf("create: %+v %v", p, err)
	}
	p2, _ := svc.Create(ctx, owner, "", "Einkaufsliste", "", pages.Public)
	if p2.Slug != "einkaufsliste-2" {
		t.Fatalf("slug collision: %q", p2.Slug)
	}
	// Toggle by line, then by text after a concurrent change.
	if p, err = svc.Toggle(ctx, other, p.Slug, 1, 0, "Milch"); err != nil || p.Body != "- [x] Milch\n- [ ] Brot\n" {
		t.Fatalf("toggle: %q %v", p.Body, err)
	}
	if p, err = svc.Toggle(ctx, other, p.Slug, 1, 0, "Brot"); err != nil || p.Body != "- [x] Milch\n- [x] Brot\n" {
		t.Fatalf("toggle by text: %q %v", p.Body, err)
	}
	// Stale save is rejected.
	if _, err := svc.Save(ctx, owner, p.Slug, 1, "Einkaufsliste", "überschrieben"); !errors.Is(err, pages.ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	// Private page: other loses access, page looks not found.
	if _, _, err := svc.SetRights(ctx, owner, p.Slug, pages.Rights{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, other, p.Slug); !errors.Is(err, pages.ErrNotFound) {
		t.Fatalf("private page visible: %v", err)
	}
	if _, _, err := svc.SetRights(ctx, owner, p.Slug, pages.Rights{Readers: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Toggle(ctx, other, p.Slug, 3, 0, "Milch"); !errors.Is(err, pages.ErrForbidden) {
		t.Fatalf("reader toggled: %v", err)
	}
	if err := svc.Delete(ctx, other, p.Slug); !errors.Is(err, pages.ErrForbidden) {
		t.Fatalf("reader deleted: %v", err)
	}
	_, revs, err := svc.Revisions(ctx, owner, p.Slug)
	if err != nil || len(revs) != 3 || revs[0].Version != 3 {
		t.Fatalf("revisions: %d %v", len(revs), err)
	}
	if err := svc.Delete(ctx, owner, p.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, owner, p.Slug); !errors.Is(err, pages.ErrNotFound) {
		t.Fatal("deleted page still visible")
	}
	var kept int
	_ = s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM page_revisions WHERE page_id = ?`, p.ID).Scan(&kept)
	if kept != 3 {
		t.Fatalf("revisions lost on delete: %d", kept)
	}
}

func TestPageTreeMigration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "allmend.db")
	ctx := context.Background()
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	opt := db.Options{BackupDir: filepath.Join(dir, "backup")}
	all := migrations.All()
	if _, err := db.Migrate(ctx, d, all[:8], opt); err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01T00:00:00Z"
	if _, err := d.ExecContext(ctx, `INSERT INTO accounts (id, email, is_admin, status, created_at, updated_at) VALUES ('o','o@example.org',1,'active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]string{{"p1", "Zeta"}, {"p2", "Alpha"}, {"p3", "Mitte"}} {
		if _, err := d.ExecContext(ctx, `INSERT INTO pages (id, slug, title, body, version, owner_id, read_all, write_all, created_at, updated_at, updated_by) VALUES (?,?,?,'',1,'o',1,1,?,?,'o')`, p[0], p[1], p[1], now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Migrate(ctx, d, all, opt); err != nil {
		t.Fatal(err)
	}
	rows, err := d.QueryContext(ctx, `SELECT title, position, parent_id FROM pages ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var title string
		var pos int
		var parent *string
		if err := rows.Scan(&title, &pos, &parent); err != nil || parent != nil {
			t.Fatalf("scan: %v %v", err, parent)
		}
		got = append(got, title+strings.Repeat("#", pos))
	}
	if strings.Join(got, ",") != "Alpha,Mitte#,Zeta##" {
		t.Fatalf("existing pages not ordered alphabetically on top level: %v", got)
	}
	if _, err := d.ExecContext(ctx, `UPDATE pages SET parent_id = id WHERE id = 'p1'`); err == nil {
		t.Fatal("a page may be its own parent")
	}
}

func TestPageTree(t *testing.T) {
	t.Parallel()
	s, _ := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := account.Account{ID: "o", Email: "o@example.org", IsAdmin: true, Status: account.Active, Theme: account.ThemeAuto, CreatedAt: now, UpdatedAt: now}
	other := account.Account{ID: "x", Email: "x@example.org", Status: account.Active, Theme: account.ThemeAuto, CreatedAt: now, UpdatedAt: now}
	_ = s.Accounts().Insert(ctx, owner)
	_ = s.Accounts().Insert(ctx, other)
	svc := pages.Service{Repo: s.Pages()}
	mk := func(parent, title string) pages.Page {
		t.Helper()
		p, err := svc.Create(ctx, owner, parent, title, "", pages.Public)
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return p
	}
	order := func(ns []pages.Node) string {
		var out []string
		var walk func(ns []pages.Node, depth int)
		walk = func(ns []pages.Node, depth int) {
			for _, n := range ns {
				out = append(out, strings.Repeat("-", depth)+n.Page.Title)
				walk(n.Children, depth+1)
			}
		}
		walk(ns, 0)
		return strings.Join(out, ",")
	}
	tree := func() string {
		t.Helper()
		ns, err := svc.Tree(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		return order(ns)
	}

	a, b, c := mk("", "A"), mk("", "B"), mk("", "C")
	a1, a2 := mk(a.ID, "A1"), mk(a.ID, "A2")
	if got := tree(); got != "A,-A1,-A2,B,C" {
		t.Fatalf("tree: %s", got)
	}
	// Reorder: C before A.
	if _, _, err := svc.Move(ctx, owner, c.Slug, "", a.ID); err != nil {
		t.Fatal(err)
	}
	if got := tree(); got != "C,A,-A1,-A2,B" {
		t.Fatalf("reorder: %s", got)
	}
	// Reparent: B below A, before A1.
	if _, _, err := svc.Move(ctx, owner, b.Slug, a.ID, a1.ID); err != nil {
		t.Fatal(err)
	}
	if got := tree(); got != "C,A,-B,-A1,-A2" {
		t.Fatalf("reparent: %s", got)
	}
	// Loops and bad targets are rejected, nothing changes.
	for name, args := range map[string][3]string{
		"onto itself":      {a.Slug, a.ID, ""},
		"below descendant": {a.Slug, a2.ID, ""},
		"unknown parent":   {a.Slug, "ffffffffffffffffffffffffffffffff", ""},
		"before non-child": {a1.Slug, a.ID, c.ID},
		"before itself":    {a1.Slug, a.ID, a1.ID},
	} {
		if _, _, err := svc.Move(ctx, owner, args[0], args[1], args[2]); !errors.Is(err, pages.ErrPlace) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := tree(); got != "C,A,-B,-A1,-A2" {
		t.Fatalf("after rejected moves: %s", got)
	}
	// Only people who may edit can move.
	if _, _, err := svc.SetRights(ctx, owner, a1.Slug, pages.Rights{ReadAll: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Move(ctx, other, a1.Slug, "", ""); !errors.Is(err, pages.ErrForbidden) {
		t.Fatalf("reader moved: %v", err)
	}
	// Deleting a page hands its children to its parent, in order, last.
	if err := svc.Delete(ctx, owner, a.Slug); err != nil {
		t.Fatal(err)
	}
	if got := tree(); got != "C,B,A1,A2" {
		t.Fatalf("delete: %s", got)
	}
	// Narrowing a page narrows its subpages: the reader sees neither.
	priv := mk("", "Privat")
	kid := mk(priv.ID, "Kind")
	_, changed, err := svc.SetRights(ctx, owner, priv.Slug, pages.Rights{})
	if err != nil || len(changed) != 1 || changed[0].ID != kid.ID {
		t.Fatalf("narrow parent: %v %v", changed, err)
	}
	ns, err := svc.Tree(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if got := order(ns); strings.Contains(got, "Kind") || strings.Contains(got, "Privat") {
		t.Fatalf("hidden parent: %s", got)
	}
	// Should stored rights ever break the rule, the page shows at the top
	// level and the hidden parent stays hidden.
	kid.ReadAll = true
	if err := s.Pages().SetRights(ctx, kid); err != nil {
		t.Fatal(err)
	}
	if ns, err = svc.Tree(ctx, other); err != nil {
		t.Fatal(err)
	}
	if got := order(ns); !strings.Contains(got, "Kind") || strings.Contains(got, "Privat") || strings.Contains(got, "-Kind") {
		t.Fatalf("fallback: %s", got)
	}
	up, err := svc.Ancestors(ctx, other, kid)
	if err != nil || len(up) != 0 {
		t.Fatalf("ancestors leak: %v %v", up, err)
	}
	if _, err := svc.Create(ctx, other, priv.ID, "Frech", "", pages.Public); !errors.Is(err, pages.ErrPlace) {
		t.Fatalf("created below invisible page: %v", err)
	}
}

// ADR-20261121-page-rights-inheritance: a subpage is never readable by more
// people than its parent.
func TestPageRightsInheritance(t *testing.T) {
	t.Parallel()
	s, _ := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	acc := func(id string, admin bool) account.Account {
		a := account.Account{ID: id, Email: id + "@example.org", IsAdmin: admin, Status: account.Active, Theme: account.ThemeAuto, CreatedAt: now, UpdatedAt: now}
		if err := s.Accounts().Insert(ctx, a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	anna, ben, mia := acc("anna", true), acc("ben", false), acc("mia", false)
	svc := pages.Service{Repo: s.Pages()}
	get := func(slug string) pages.Page {
		t.Helper()
		p, err := s.Pages().BySlug(ctx, slug)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	audience := func(p pages.Page) string {
		x := p.Audience()
		if x.All {
			return "all"
		}
		var ids []string
		for id := range x.IDs {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		return strings.Join(ids, ",")
	}

	// Geschenke: anna and ben read, ben edits.
	gifts, err := svc.Create(ctx, anna, "", "Geschenke", "", pages.Rights{Readers: []string{ben.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// A new subpage starts as wide as the parent allows, and no wider.
	if r := gifts.ChildRights(ben.ID); r.ReadAll || r.WriteAll || !slices.Equal(r.Writers, []string{anna.ID}) {
		t.Fatalf("child rights: %+v", r)
	}
	if _, err := svc.Create(ctx, ben, gifts.ID, "Offen", "", pages.Public); !errors.Is(err, pages.ErrWider) {
		t.Fatalf("public below private: %v", err)
	}
	if _, err := svc.Create(ctx, ben, gifts.ID, "Mia", "", pages.Rights{Readers: []string{mia.ID}}); !errors.Is(err, pages.ErrWider) {
		t.Fatalf("extra reader below private: %v", err)
	}
	wish, err := svc.Create(ctx, ben, gifts.ID, "Wunschliste", "", gifts.ChildRights(ben.ID))
	if err != nil {
		t.Fatal(err)
	}
	ideas, err := svc.Create(ctx, anna, wish.ID, "Ideen", "", wish.ChildRights(anna.ID))
	if err != nil || audience(ideas) != "anna,ben" {
		t.Fatalf("grandchild: %v %s", err, audience(ideas))
	}
	// The page itself may not get wider than its parent.
	if _, _, err := svc.SetRights(ctx, ben, wish.Slug, pages.Public); !errors.Is(err, pages.ErrWider) {
		t.Fatalf("widen subpage: %v", err)
	}

	// Narrowing Geschenke to anna alone would lock ben out of his own
	// Wunschliste: refused, nothing changes.
	_, _, err = svc.SetRights(ctx, anna, gifts.Slug, pages.Rights{})
	var locked *pages.LockedError
	if !errors.As(err, &locked) || len(locked.Pages) != 1 || locked.Pages[0].ID != wish.ID {
		t.Fatalf("lock out owner: %v", err)
	}
	if audience(get(gifts.Slug)) != "anna,ben" {
		t.Fatal("refused narrowing stored")
	}

	// Widening the parent leaves subpages as they are; narrowing it again
	// narrows them, down the whole branch, in one go.
	if _, ch, err := svc.SetRights(ctx, anna, gifts.Slug, pages.Rights{Readers: []string{ben.ID, mia.ID}}); err != nil || len(ch) != 0 {
		t.Fatalf("widen parent: %v %v", ch, err)
	}
	if _, _, err := svc.SetRights(ctx, ben, wish.Slug, pages.Rights{Readers: []string{anna.ID, mia.ID}, Writers: []string{anna.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.SetRights(ctx, anna, ideas.Slug, pages.Rights{Writers: []string{ben.ID, mia.ID}}); err != nil {
		t.Fatal(err)
	}
	_, ch, err := svc.SetRights(ctx, anna, gifts.Slug, pages.Rights{Readers: []string{ben.ID}})
	if err != nil || len(ch) != 2 {
		t.Fatalf("narrow branch: %v %v", ch, err)
	}
	if w := get(wish.Slug); audience(w) != "anna,ben" || !slices.Equal(w.Writers, []string{anna.ID}) {
		t.Fatalf("wish narrowed: %+v", w)
	}
	if i := get(ideas.Slug); audience(i) != "anna,ben" || !slices.Equal(i.Writers, []string{ben.ID}) {
		t.Fatalf("ideas narrowed: %+v", i)
	}

	// Moving below a narrower page narrows the moved branch. Only the
	// owner may; anyone who may edit can still move within the same rights.
	open1, err := svc.Create(ctx, mia, "", "Offen", "", pages.Public)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, ben, open1.ID, "Offen Kind", "", pages.Public); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Move(ctx, mia, open1.Slug, gifts.ID, ""); !errors.Is(err, pages.ErrPlace) {
		t.Fatalf("mia moved below a page she cannot read: %v", err)
	}
	if _, _, err := svc.Move(ctx, ben, open1.Slug, gifts.ID, ""); !errors.As(err, &locked) {
		t.Fatalf("move locks mia out of her page: %v", err)
	}
	if _, _, err := svc.SetRights(ctx, anna, gifts.Slug, pages.Rights{Readers: []string{ben.ID, mia.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Move(ctx, ben, open1.Slug, gifts.ID, ""); !errors.Is(err, pages.ErrManage) {
		t.Fatalf("writer narrowed by moving: %v", err)
	}
	moved, ch, err := svc.Move(ctx, mia, open1.Slug, gifts.ID, "")
	if err != nil || len(ch) != 2 || moved.ParentID != gifts.ID {
		t.Fatalf("move narrows: %v %v", ch, err)
	}
	for _, slug := range []string{"offen", "offen-kind"} {
		if got := audience(get(slug)); got != "anna,ben,mia" {
			t.Errorf("%s after move: %s", slug, got)
		}
	}
	// A move that would lock an owner out is refused.
	solo, err := svc.Create(ctx, anna, "", "Nur Anna", "", pages.Rights{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Move(ctx, anna, open1.Slug, solo.ID, ""); !errors.As(err, &locked) {
		t.Fatalf("move locks out owners: %v", err)
	}
	if get(open1.Slug).ParentID != gifts.ID {
		t.Fatal("refused move stored")
	}
}

func TestCommuteConflict(t *testing.T) {
	t.Parallel()
	s, _ := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	a := account.Account{ID: "a", Email: "a@example.org", IsAdmin: true, Status: account.Active, Theme: account.ThemeAuto, CreatedAt: now, UpdatedAt: now}
	_ = s.Accounts().Insert(ctx, a)
	svc := commute.Service{Repo: s.Commute()}
	y, err := svc.Create(ctx, a, commute.Year{Year: 2026, CarDistKm: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, a, commute.Year{Year: 2026}); !errors.Is(err, commute.ErrExists) {
		t.Fatalf("duplicate year: %v", err)
	}
	y.Months[0].Workdays = 20
	if _, err := svc.Save(ctx, a, y, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Save(ctx, a, y, 1); !errors.Is(err, commute.ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	d, _ := svc.Draft(ctx, a, 2027)
	if d.CarDistKm != 5 {
		t.Errorf("draft prefill: %+v", d)
	}
}

const ts0 = `'2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z'`
