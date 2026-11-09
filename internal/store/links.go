package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tiliavir/allmend/internal/links"
)

// Links implements links.Repository.
type Links struct{ s *Store }

// Links returns the link repository.
func (s *Store) Links() *Links { return &Links{s} }

const linkCols = `id, name, url, description, icon, public, position, created_at, updated_at`

func scanLink(row interface{ Scan(...any) error }) (links.Link, error) {
	var l links.Link
	var pub int
	var c, u string
	if err := row.Scan(&l.ID, &l.Name, &l.URL, &l.Description, &l.Icon, &pub, &l.Position, &c, &u); err != nil {
		return l, err
	}
	l.Public = pub == 1
	var err error
	if l.CreatedAt, err = parseTime(c); err != nil {
		return l, err
	}
	l.UpdatedAt, err = parseTime(u)
	return l, err
}

// List returns all links ordered by position.
func (r *Links) List(ctx context.Context) ([]links.Link, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+linkCols+` FROM links ORDER BY position, name`)
	if err != nil {
		return nil, fmt.Errorf("store.Links.List: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []links.Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, fmt.Errorf("store.Links.List scan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Get loads one link.
func (r *Links) Get(ctx context.Context, id string) (links.Link, error) {
	l, err := scanLink(r.s.db.QueryRowContext(ctx, `SELECT `+linkCols+` FROM links WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return l, links.ErrNotFound
	}
	if err != nil {
		return l, fmt.Errorf("store.Links.Get: %w", err)
	}
	return l, nil
}

// Insert adds a link.
func (r *Links) Insert(ctx context.Context, l links.Link) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO links (`+linkCols+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		l.ID, l.Name, l.URL, l.Description, l.Icon, b2i(l.Public), l.Position, fmtTime(l.CreatedAt), fmtTime(l.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store.Links.Insert: %w", err)
	}
	return nil
}

// Update writes all fields of a link.
func (r *Links) Update(ctx context.Context, l links.Link) error {
	res, err := r.s.db.ExecContext(ctx, `UPDATE links SET name=?, url=?, description=?, icon=?, public=?, position=?, updated_at=? WHERE id=?`,
		l.Name, l.URL, l.Description, l.Icon, b2i(l.Public), l.Position, fmtTime(l.UpdatedAt), l.ID)
	if err != nil {
		return fmt.Errorf("store.Links.Update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return links.ErrNotFound
	}
	return nil
}

// Delete removes a link.
func (r *Links) Delete(ctx context.Context, id string) error {
	res, err := r.s.db.ExecContext(ctx, `DELETE FROM links WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store.Links.Delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return links.ErrNotFound
	}
	return nil
}

// Reorder sets positions in one transaction.
func (r *Links) Reorder(ctx context.Context, ids []string) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE links SET position = ? WHERE id = ?`, i, id); err != nil {
			return fmt.Errorf("store.Links.Reorder: %w", err)
		}
	}
	return tx.Commit()
}
