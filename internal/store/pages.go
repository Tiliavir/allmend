package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tiliavir/allmend/internal/pages"
)

// Pages implements pages.Repository.
type Pages struct{ s *Store }

// Pages returns the page repository.
func (s *Store) Pages() *Pages { return &Pages{s} }

const pageCols = `id, slug, title, body, version, owner_id, read_all, write_all, created_at, updated_at, updated_by`

func (r *Pages) scan(ctx context.Context, row interface{ Scan(...any) error }) (pages.Page, error) {
	var p pages.Page
	var ra, wa int
	var c, u string
	if err := row.Scan(&p.ID, &p.Slug, &p.Title, &p.Body, &p.Version, &p.OwnerID, &ra, &wa, &c, &u, &p.UpdatedBy); err != nil {
		return p, err
	}
	p.ReadAll, p.WriteAll = ra == 1, wa == 1
	var err error
	if p.CreatedAt, err = parseTime(c); err != nil {
		return p, err
	}
	if p.UpdatedAt, err = parseTime(u); err != nil {
		return p, err
	}
	return p, nil
}

func (r *Pages) access(ctx context.Context, ps []pages.Page) error {
	idx := map[string]int{}
	for i := range ps {
		idx[ps[i].ID] = i
	}
	rows, err := r.s.db.QueryContext(ctx, `SELECT a.page_id, a.account_id, a.kind FROM page_access a JOIN pages p ON p.id = a.page_id WHERE p.deleted_at IS NULL ORDER BY a.account_id`)
	if err != nil {
		return fmt.Errorf("store.Pages access: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var pid, aid, right string
		if err := rows.Scan(&pid, &aid, &right); err != nil {
			return err
		}
		i, ok := idx[pid]
		if !ok {
			continue
		}
		if right == "read" {
			ps[i].Readers = append(ps[i].Readers, aid)
		} else {
			ps[i].Writers = append(ps[i].Writers, aid)
		}
	}
	return rows.Err()
}

// List returns all pages that are not deleted, newest first.
func (r *Pages) List(ctx context.Context) ([]pages.Page, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+pageCols+` FROM pages WHERE deleted_at IS NULL ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("store.Pages.List: %w", err)
	}
	var out []pages.Page
	for rows.Next() {
		p, err := r.scan(ctx, rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store.Pages.List scan: %w", err)
		}
		out = append(out, p)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return out, r.access(ctx, out)
}

// BySlug loads one page that is not deleted.
func (r *Pages) BySlug(ctx context.Context, slug string) (pages.Page, error) {
	p, err := r.scan(ctx, r.s.db.QueryRowContext(ctx, `SELECT `+pageCols+` FROM pages WHERE slug = ? AND deleted_at IS NULL`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return p, pages.ErrNotFound
	}
	if err != nil {
		return p, fmt.Errorf("store.Pages.BySlug: %w", err)
	}
	ps := []pages.Page{p}
	err = r.access(ctx, ps)
	return ps[0], err
}

// SlugExists reports whether any page (also a deleted one) uses slug.
func (r *Pages) SlugExists(ctx context.Context, slug string) (bool, error) {
	var n int
	err := r.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pages WHERE slug = ?`, slug).Scan(&n)
	return n > 0, err
}

func revision(ctx context.Context, tx *sql.Tx, p pages.Page) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO page_revisions (page_id, version, title, body, author_id, created_at) VALUES (?,?,?,?,?,?)`,
		p.ID, p.Version, p.Title, p.Body, p.UpdatedBy, fmtTime(p.UpdatedAt))
	return err
}

func writeAccess(ctx context.Context, tx *sql.Tx, p pages.Page) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM page_access WHERE page_id = ?`, p.ID); err != nil {
		return err
	}
	for right, ids := range map[string][]string{"read": p.Readers, "write": p.Writers} {
		for _, a := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT INTO page_access (page_id, account_id, kind) VALUES (?,?,?)`, p.ID, a, right); err != nil {
				return err
			}
		}
	}
	return nil
}

// Insert creates a page with its first revision.
func (r *Pages) Insert(ctx context.Context, p pages.Page) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO pages (`+pageCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Slug, p.Title, p.Body, p.Version, p.OwnerID, b2i(p.ReadAll), b2i(p.WriteAll), fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt), p.UpdatedBy); err != nil {
		return fmt.Errorf("store.Pages.Insert: %w", err)
	}
	if err := writeAccess(ctx, tx, p); err != nil {
		return fmt.Errorf("store.Pages.Insert access: %w", err)
	}
	if err := revision(ctx, tx, p); err != nil {
		return fmt.Errorf("store.Pages.Insert revision: %w", err)
	}
	return tx.Commit()
}

// Update saves title and body if the stored version is want.
func (r *Pages) Update(ctx context.Context, p pages.Page, want int) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE pages SET title=?, body=?, version=?, updated_at=?, updated_by=? WHERE id=? AND version=? AND deleted_at IS NULL`,
		p.Title, p.Body, p.Version, fmtTime(p.UpdatedAt), p.UpdatedBy, p.ID, want)
	if err != nil {
		return fmt.Errorf("store.Pages.Update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return pages.ErrConflict
	}
	if err := revision(ctx, tx, p); err != nil {
		return fmt.Errorf("store.Pages.Update revision: %w", err)
	}
	return tx.Commit()
}

// SetRights replaces the access lists.
func (r *Pages) SetRights(ctx context.Context, p pages.Page) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE pages SET read_all=?, write_all=? WHERE id=?`, b2i(p.ReadAll), b2i(p.WriteAll), p.ID); err != nil {
		return fmt.Errorf("store.Pages.SetRights: %w", err)
	}
	if err := writeAccess(ctx, tx, p); err != nil {
		return fmt.Errorf("store.Pages.SetRights access: %w", err)
	}
	return tx.Commit()
}

// Delete marks a page deleted; content and revisions stay.
func (r *Pages) Delete(ctx context.Context, pageID, by string, at time.Time) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE pages SET deleted_at=?, deleted_by=? WHERE id=? AND deleted_at IS NULL`, fmtTime(at), by, pageID)
	if err != nil {
		return fmt.Errorf("store.Pages.Delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return pages.ErrNotFound
	}
	return nil
}

// Revisions lists a page's revisions, newest first.
func (r *Pages) Revisions(ctx context.Context, pageID string) ([]pages.Revision, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT version, title, body, author_id, created_at FROM page_revisions WHERE page_id = ? ORDER BY version DESC`, pageID)
	if err != nil {
		return nil, fmt.Errorf("store.Pages.Revisions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []pages.Revision
	for rows.Next() {
		var v pages.Revision
		var c string
		if err := rows.Scan(&v.Version, &v.Title, &v.Body, &v.AuthorID, &c); err != nil {
			return nil, err
		}
		if v.CreatedAt, err = parseTime(c); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
