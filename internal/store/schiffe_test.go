package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiliavir/allmend/internal/db"
	"github.com/tiliavir/allmend/internal/migrations"
	"github.com/tiliavir/allmend/internal/schiffe"
	"github.com/tiliavir/allmend/internal/store"
)

// TestSeaMinesMigration: games stored before v10 keep their ships and shots;
// their shots name no ship.
func TestSeaMinesMigration(t *testing.T) {
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
	if _, err := db.Migrate(ctx, d, all[:9], opt); err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01T00:00:00Z"
	for _, stmt := range []string{
		`INSERT INTO accounts (id, email, status, created_at, updated_at) VALUES ('a','a@example.org','active','` + now + `','` + now + `')`,
		`INSERT INTO accounts (id, email, status, created_at, updated_at) VALUES ('b','b@example.org','active','` + now + `','` + now + `')`,
		`INSERT INTO games (id, player_a, player_b, variant, touching, auto_water, status, turn, winner, version, created_at, updated_at) VALUES ('g','a','b','salve',0,1,'laeuft','b',NULL,4,'` + now + `','` + now + `')`,
		`INSERT INTO game_ships (game_id, player_id, idx, size, x, y, horizontal) VALUES ('g','a',0,5,0,0,1), ('g','b',0,5,0,2,0)`,
		`INSERT INTO game_shots (game_id, seq, shooter, x, y, result, at) VALUES ('g',1,'a',0,2,'treffer','` + now + `'), ('g',2,'a',9,9,'wasser','` + now + `')`,
	} {
		if _, err := d.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := db.Migrate(ctx, d, all, opt); err != nil {
		t.Fatal(err)
	}
	g, err := store.New(d).Schiffe().Get(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if g.Variant != schiffe.Salvo || g.Version != 4 || g.Turn != "b" || len(g.FleetA) != 1 || len(g.FleetB) != 1 || g.MinesA != nil {
		t.Fatalf("game after migration: %+v", g)
	}
	if len(g.Shots) != 2 || g.Shots[0].Result != schiffe.Hit || g.Shots[0].Ship != schiffe.NoShip {
		t.Fatalf("shots after migration: %+v", g.Shots)
	}
	var schema string
	if err := d.QueryRowContext(ctx, `SELECT group_concat(sql, ' ') FROM sqlite_master WHERE name IN ('game_ships', 'game_shots', 'game_mines')`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(schema, "_v10") || strings.Count(strings.ReplaceAll(schema, `"games"`, "games"), "REFERENCES games(id)") != 3 {
		t.Fatalf("references not pointing to games: %s", schema)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO game_ships (game_id, player_id, idx, size, x, y, horizontal) VALUES ('nope','a',1,2,0,0,1)`); err == nil {
		t.Fatal("foreign key to games not enforced after the rebuild")
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO game_shots (game_id, seq, shooter, x, y, result, at) VALUES ('g',3,'b',1,1,'mine','`+now+`')`); err == nil {
		t.Fatal("a mine shot without a ship was stored")
	}
}

func TestSchiffeMinesRoundTrip(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"a", "b"} {
		if _, err := s.DB().ExecContext(ctx, `INSERT INTO accounts (id, email, status, created_at, updated_at) VALUES (?,?, 'active', ?, ?)`,
			id, id+"@example.org", now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	repo := s.Schiffe()
	g := schiffe.Game{ID: "g", PlayerA: "a", PlayerB: "b", Variant: schiffe.Mines, Status: schiffe.Setup, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := repo.Insert(ctx, g); err != nil {
		t.Fatal(err)
	}
	fleet := []schiffe.Ship{
		{Size: 5, X: 0, Y: 0, Horizontal: true}, {Size: 4, X: 0, Y: 2, Horizontal: true}, {Size: 4, X: 6, Y: 2, Horizontal: true},
		{Size: 3, X: 0, Y: 4, Horizontal: true}, {Size: 3, X: 4, Y: 4, Horizontal: true}, {Size: 3, X: 8, Y: 4},
		{Size: 2, X: 0, Y: 6, Horizontal: true}, {Size: 2, X: 3, Y: 6, Horizontal: true}, {Size: 2, X: 0, Y: 8, Horizontal: true}, {Size: 2, X: 3, Y: 8, Horizontal: true},
	}
	mines := [][2]int{{7, 9}, {9, 9}, {9, 0}}
	for _, p := range []string{"a", "b"} {
		if err := g.Place(p, fleet, mines); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.Fire("a", [][2]int{{9, 9}}, 2, now); err != nil {
		t.Fatal(err)
	}
	g.Version = 2
	if err := repo.Save(ctx, g, 1); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MinesA) != 3 || got.MinesB[2] != [2]int{9, 0} || got.Shots[0].Result != schiffe.Mine || got.Shots[0].Ship != 2 || got.Afloat("a", 2) {
		t.Fatalf("round trip: %+v", got)
	}
	if err := repo.Save(ctx, g, 1); !errors.Is(err, schiffe.ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
}
