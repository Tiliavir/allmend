package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tiliavir/allmend/internal/schiffe"
)

// Schiffe implements schiffe.Repository.
type Schiffe struct{ s *Store }

// Schiffe returns the game repository.
func (s *Store) Schiffe() *Schiffe { return &Schiffe{s} }

const gameCols = `id, player_a, player_b, variant, touching, auto_water, status, COALESCE(turn, ''), COALESCE(winner, ''), version, created_at, updated_at`

func scanGame(row interface{ Scan(...any) error }) (schiffe.Game, error) {
	var g schiffe.Game
	var v, st, c, u string
	var touch, water int
	if err := row.Scan(&g.ID, &g.PlayerA, &g.PlayerB, &v, &touch, &water, &st, &g.Turn, &g.Winner, &g.Version, &c, &u); err != nil {
		return g, err
	}
	g.Variant, g.Status, g.Touching, g.AutoWater = schiffe.Variant(v), schiffe.Status(st), touch == 1, water == 1
	return g, times(&g.CreatedAt, &g.UpdatedAt, c, u)
}

// Games lists the games of a player, newest first (without ships and shots).
func (r *Schiffe) Games(ctx context.Context, player string) ([]schiffe.Game, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+gameCols+` FROM games WHERE player_a = ? OR player_b = ? ORDER BY updated_at DESC`, player, player)
	if err != nil {
		return nil, fmt.Errorf("store.Schiffe.Games: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []schiffe.Game
	for rows.Next() {
		g, err := scanGame(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Get loads a game with fleets and shots.
func (r *Schiffe) Get(ctx context.Context, id string) (schiffe.Game, error) {
	g, err := scanGame(r.s.db.QueryRowContext(ctx, `SELECT `+gameCols+` FROM games WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return g, schiffe.ErrNotFound
	}
	if err != nil {
		return g, fmt.Errorf("store.Schiffe.Get: %w", err)
	}
	ships, err := r.s.db.QueryContext(ctx, `SELECT player_id, size, x, y, horizontal FROM game_ships WHERE game_id = ? ORDER BY player_id, idx`, id)
	if err != nil {
		return g, err
	}
	for ships.Next() {
		var p string
		var sh schiffe.Ship
		var h int
		if err := ships.Scan(&p, &sh.Size, &sh.X, &sh.Y, &h); err != nil {
			_ = ships.Close()
			return g, err
		}
		sh.Horizontal = h == 1
		if p == g.PlayerA {
			g.FleetA = append(g.FleetA, sh)
		} else {
			g.FleetB = append(g.FleetB, sh)
		}
	}
	if err := errors.Join(ships.Err(), ships.Close()); err != nil {
		return g, err
	}
	mines, err := r.s.db.QueryContext(ctx, `SELECT player_id, x, y FROM game_mines WHERE game_id = ? ORDER BY player_id, idx`, id)
	if err != nil {
		return g, err
	}
	for mines.Next() {
		var p string
		var m [2]int
		if err := mines.Scan(&p, &m[0], &m[1]); err != nil {
			_ = mines.Close()
			return g, err
		}
		if p == g.PlayerA {
			g.MinesA = append(g.MinesA, m)
		} else {
			g.MinesB = append(g.MinesB, m)
		}
	}
	if err := errors.Join(mines.Err(), mines.Close()); err != nil {
		return g, err
	}
	shots, err := r.s.db.QueryContext(ctx, `SELECT seq, shooter, x, y, ship, result, at FROM game_shots WHERE game_id = ? ORDER BY seq`, id)
	if err != nil {
		return g, err
	}
	defer func() { _ = shots.Close() }()
	for shots.Next() {
		var s schiffe.Shot
		var res, at string
		var ship sql.NullInt64
		if err := shots.Scan(&s.Seq, &s.Shooter, &s.X, &s.Y, &ship, &res, &at); err != nil {
			return g, err
		}
		s.Ship = schiffe.NoShip
		if ship.Valid {
			s.Ship = int(ship.Int64)
		}
		s.Result = schiffe.Result(res)
		if s.At, err = parseTime(at); err != nil {
			return g, err
		}
		g.Shots = append(g.Shots, s)
	}
	return g, shots.Err()
}

// Insert creates a game.
func (r *Schiffe) Insert(ctx context.Context, g schiffe.Game) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO games (id, player_a, player_b, variant, touching, auto_water, status, turn, winner, version, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		g.ID, g.PlayerA, g.PlayerB, string(g.Variant), b2i(g.Touching), b2i(g.AutoWater), string(g.Status), nullStr(g.Turn), nullStr(g.Winner), g.Version, fmtTime(g.CreatedAt), fmtTime(g.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store.Schiffe.Insert: %w", err)
	}
	return nil
}

// Save writes state, missing fleets and new shots if the version is want.
func (r *Schiffe) Save(ctx context.Context, g schiffe.Game, want int) error {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE games SET status=?, turn=?, winner=?, version=?, updated_at=? WHERE id=? AND version=?`,
		string(g.Status), nullStr(g.Turn), nullStr(g.Winner), g.Version, fmtTime(g.UpdatedAt), g.ID, want)
	if err := affected(res, err, schiffe.ErrConflict); err != nil {
		return err
	}
	for _, p := range []string{g.PlayerA, g.PlayerB} {
		fleet := g.FleetOf(p)
		if fleet == nil {
			continue
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_ships WHERE game_id = ? AND player_id = ?`, g.ID, p).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue // fleets never change once placed
		}
		for i, sh := range fleet {
			if _, err := tx.ExecContext(ctx, `INSERT INTO game_ships (game_id, player_id, idx, size, x, y, horizontal) VALUES (?,?,?,?,?,?,?)`,
				g.ID, p, i, sh.Size, sh.X, sh.Y, b2i(sh.Horizontal)); err != nil {
				return fmt.Errorf("store.Schiffe.Save ship: %w", err)
			}
		}
		for i, m := range g.MinesOf(p) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO game_mines (game_id, player_id, idx, x, y) VALUES (?,?,?,?,?)`,
				g.ID, p, i, m[0], m[1]); err != nil {
				return fmt.Errorf("store.Schiffe.Save mine: %w", err)
			}
		}
	}
	var last int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM game_shots WHERE game_id = ?`, g.ID).Scan(&last); err != nil {
		return err
	}
	for _, s := range g.Shots {
		if s.Seq <= last {
			continue
		}
		var ship any
		if s.Ship != schiffe.NoShip {
			ship = s.Ship
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_shots (game_id, seq, shooter, x, y, ship, result, at) VALUES (?,?,?,?,?,?,?,?)`,
			g.ID, s.Seq, s.Shooter, s.X, s.Y, ship, string(s.Result), fmtTime(s.At)); err != nil {
			return fmt.Errorf("store.Schiffe.Save shot: %w", err)
		}
	}
	return tx.Commit()
}
