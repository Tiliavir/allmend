package migrations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/tiliavir/allmend/internal/db"
)

// v15 enforces ADR-20261121-page-rights-inheritance on existing pages: a
// subpage is never readable by more people than its parent. Subpages that
// are, are narrowed top down to their parent's readers ("everyone" becomes
// those readers; edit rights stay within them). If a page's owner may not
// read its parent, the migration aborts with the page ids: that needs a
// decision by hand (move the page or widen the parent). The narrowed page
// ids are logged, so `migrate --dry-run` shows them before a release.
//
// Self-contained on purpose: a released step must not change when the
// domain code does.
func v15(ctx context.Context, tx db.Tx, _ db.MigrationEnv) error {
	ps, err := v15Load(ctx, tx)
	if err != nil {
		return err
	}
	kids := map[string][]*v15Page{}
	byID := map[string]*v15Page{}
	var roots []*v15Page
	for _, p := range ps {
		byID[p.id] = p
	}
	for _, p := range ps {
		switch {
		case p.parent == "":
			roots = append(roots, p)
		case byID[p.parent] == nil:
			return fmt.Errorf("v15: page %s has no live parent %s", p.id, p.parent)
		default:
			kids[p.parent] = append(kids[p.parent], p)
		}
	}
	var changed, locked []string
	queue := roots
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		limit := parent.audience()
		for _, c := range kids[parent.id] {
			queue = append(queue, c)
			if limit == nil || c.within(limit) {
				continue
			}
			if !limit[c.owner] {
				locked = append(locked, c.id)
				continue
			}
			c.fit(limit)
			changed = append(changed, c.id)
		}
	}
	if len(locked) > 0 {
		slices.Sort(locked)
		return fmt.Errorf("v15: owners may not read the parent of pages %v; move them or widen the parent, then migrate again", locked)
	}
	for _, id := range changed {
		if err := byID[id].store(ctx, tx); err != nil {
			return err
		}
	}
	if len(changed) > 0 {
		slog.Info("v15: page rights narrowed to the parent", "pages", changed)
	}
	return nil
}

type v15Page struct {
	id, parent, owner string
	readAll, writeAll bool
	readers, writers  []string
}

// audience is who may read the page; nil means everyone.
func (p *v15Page) audience() map[string]bool {
	if p.readAll || p.writeAll {
		return nil
	}
	m := map[string]bool{p.owner: true}
	for _, id := range append(slices.Clone(p.readers), p.writers...) {
		m[id] = true
	}
	return m
}

func (p *v15Page) within(limit map[string]bool) bool {
	own := p.audience()
	if own == nil {
		return false
	}
	for id := range own {
		if !limit[id] {
			return false
		}
	}
	return true
}

func (p *v15Page) fit(limit map[string]bool) {
	var everyone []string
	for id := range limit {
		if id != p.owner {
			everyone = append(everyone, id)
		}
	}
	slices.Sort(everyone)
	keep := func(ids []string, all bool) []string {
		if all {
			return slices.Clone(everyone)
		}
		return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return !limit[id] })
	}
	readAll := p.readAll || p.writeAll
	p.writers = keep(p.writers, p.writeAll)
	p.readers = keep(p.readers, readAll)
	if p.writeAll {
		p.readers = nil
	}
	p.readAll, p.writeAll = false, false
}

func (p *v15Page) store(ctx context.Context, tx db.Tx) error {
	if _, err := tx.ExecContext(ctx, `UPDATE pages SET read_all = 0, write_all = 0 WHERE id = ?`, p.id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM page_access WHERE page_id = ?`, p.id); err != nil {
		return err
	}
	for kind, ids := range map[string][]string{"read": p.readers, "write": p.writers} {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT INTO page_access (page_id, account_id, kind) VALUES (?, ?, ?)`, p.id, id, kind); err != nil {
				return err
			}
		}
	}
	return nil
}

func v15Load(ctx context.Context, tx db.Tx) ([]*v15Page, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(parent_id, ''), owner_id, read_all, write_all FROM pages WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ps []*v15Page
	byID := map[string]*v15Page{}
	for rows.Next() {
		p := &v15Page{}
		if err := rows.Scan(&p.id, &p.parent, &p.owner, &p.readAll, &p.writeAll); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ps = append(ps, p)
		byID[p.id] = p
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT page_id, account_id, kind FROM page_access ORDER BY page_id, account_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var page, acc, kind string
		if err := rows.Scan(&page, &acc, &kind); err != nil {
			_ = rows.Close()
			return nil, err
		}
		p := byID[page]
		switch {
		case p == nil: // access rows of a deleted page stay as they are
		case kind == "read":
			p.readers = append(p.readers, acc)
		default:
			p.writers = append(p.writers, acc)
		}
	}
	return ps, errors.Join(rows.Err(), rows.Close())
}
