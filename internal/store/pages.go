package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/tiliavir/allmend/internal/pages"
)

// Pages implements pages.Repository.
type Pages struct{ s *Store }

// Pages returns the page repository.
func (s *Store) Pages() *Pages { return &Pages{s} }

const pageCols = `id, slug, parent_id, position, title, body, version, owner_id, read_all, write_all, created_at, updated_at, updated_by`

func (r *Pages) scan(ctx context.Context, row interface{ Scan(...any) error }) (pages.Page, error) {
	var p pages.Page
	var ra, wa int
	var c, u string
	var parent sql.NullString
	if err := row.Scan(&p.ID, &p.Slug, &parent, &p.Position, &p.Title, &p.Body, &p.Version, &p.OwnerID, &ra, &wa, &c, &u, &p.UpdatedBy); err != nil {
		return p, err
	}
	p.ReadAll, p.WriteAll, p.ParentID = ra == 1, wa == 1, parent.String
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

// List returns all pages that are not deleted, by parent and position.
func (r *Pages) List(ctx context.Context) ([]pages.Page, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+pageCols+` FROM pages WHERE deleted_at IS NULL ORDER BY parent_id, position, title`)
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

// livePage reports whether id is a page that is not deleted.
func livePage(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pages WHERE id = ? AND deleted_at IS NULL`, id).Scan(&n)
	return n == 1, err
}

// nextPosition is the position after the last child of parent ("" = top level).
func nextPosition(ctx context.Context, tx *sql.Tx, parent string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position) + 1, 0) FROM pages WHERE parent_id IS ? AND deleted_at IS NULL`, nullStr(parent)).Scan(&n)
	return n, err
}

// Insert creates a page with its first revision, last among its siblings.
func (r *Pages) Insert(ctx context.Context, p pages.Page) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if p.ParentID != "" {
		ok, err := livePage(ctx, tx, p.ParentID)
		if err != nil {
			return fmt.Errorf("store.Pages.Insert parent: %w", err)
		}
		if !ok {
			return pages.ErrPlace
		}
	}
	if p.Position, err = nextPosition(ctx, tx, p.ParentID); err != nil {
		return fmt.Errorf("store.Pages.Insert position: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pages (`+pageCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Slug, nullStr(p.ParentID), p.Position, p.Title, p.Body, p.Version, p.OwnerID, b2i(p.ReadAll), b2i(p.WriteAll), fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt), p.UpdatedBy); err != nil {
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

// Move places a page below parentID, right before beforeID ("" = last), and
// renumbers the siblings.
func (r *Pages) Move(ctx context.Context, pageID, parentID, beforeID string) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ok, err := livePage(ctx, tx, pageID)
	if err != nil {
		return fmt.Errorf("store.Pages.Move: %w", err)
	}
	if !ok {
		return pages.ErrNotFound
	}
	if parentID != "" {
		ok, err := livePage(ctx, tx, parentID)
		if err != nil {
			return fmt.Errorf("store.Pages.Move parent: %w", err)
		}
		// The new parent must not be the page itself or below it.
		var loop int
		if err := tx.QueryRowContext(ctx, `
WITH RECURSIVE up(id) AS (
	SELECT ?
	UNION SELECT p.parent_id FROM pages p JOIN up ON p.id = up.id WHERE p.parent_id IS NOT NULL
) SELECT COUNT(*) FROM up WHERE id = ?`, parentID, pageID).Scan(&loop); err != nil {
			return fmt.Errorf("store.Pages.Move loop: %w", err)
		}
		if !ok || loop > 0 {
			return pages.ErrPlace
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM pages WHERE parent_id IS ? AND deleted_at IS NULL AND id != ? ORDER BY position, title`, nullStr(parentID), pageID)
	if err != nil {
		return fmt.Errorf("store.Pages.Move siblings: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	at := len(ids)
	if beforeID != "" {
		if at = slices.Index(ids, beforeID); at < 0 {
			return pages.ErrPlace
		}
	}
	ids = slices.Insert(ids, at, pageID)
	if _, err := tx.ExecContext(ctx, `UPDATE pages SET parent_id = ? WHERE id = ?`, nullStr(parentID), pageID); err != nil {
		return fmt.Errorf("store.Pages.Move: %w", err)
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE pages SET position = ? WHERE id = ?`, i, id); err != nil {
			return fmt.Errorf("store.Pages.Move position: %w", err)
		}
	}
	return tx.Commit()
}

// Delete marks a page deleted; content and revisions stay. Its children move
// to its parent, after the parent's other children, in their order.
func (r *Pages) Delete(ctx context.Context, pageID, by string, at time.Time) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var parent sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT parent_id FROM pages WHERE id = ? AND deleted_at IS NULL`, pageID).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return pages.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store.Pages.Delete: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pages SET deleted_at=?, deleted_by=? WHERE id=?`, fmtTime(at), by, pageID); err != nil {
		return fmt.Errorf("store.Pages.Delete: %w", err)
	}
	next, err := nextPosition(ctx, tx, parent.String)
	if err != nil {
		return fmt.Errorf("store.Pages.Delete position: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pages SET parent_id = ?, position = ? + position WHERE parent_id = ?`, nullStr(parent.String), next, pageID); err != nil {
		return fmt.Errorf("store.Pages.Delete children: %w", err)
	}
	return tx.Commit()
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
