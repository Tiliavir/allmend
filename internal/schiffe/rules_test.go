package schiffe

import (
	"errors"
	"math/rand/v2"
	"testing"
	"time"
)

func fleet() []Ship {
	// Non-touching classic fleet on 10×10.
	return []Ship{
		{5, 0, 0, true}, {4, 0, 2, true}, {4, 6, 2, true},
		{3, 0, 4, true}, {3, 4, 4, true}, {3, 8, 4, false},
		{2, 0, 6, true}, {2, 3, 6, true}, {2, 0, 8, true}, {2, 3, 8, true},
	}
}

func TestCheckFleet(t *testing.T) {
	if err := CheckFleet(fleet(), 10, false); err != nil {
		t.Fatal(err)
	}
	f := fleet()
	f[9] = Ship{2, 0, 9, true} // touches the submarine at row 8 (A9)
	if err := CheckFleet(f, 10, false); !errors.Is(err, ErrPlacement) {
		t.Errorf("touching accepted: %v", err)
	}
	if err := CheckFleet(f, 10, true); err != nil {
		t.Errorf("touching allowed but rejected: %v", err)
	}
	f = fleet()
	f[0] = Ship{5, 7, 0, true}
	if err := CheckFleet(f, 10, true); !errors.Is(err, ErrPlacement) {
		t.Error("out of board accepted")
	}
	if err := CheckFleet(fleet()[:9], 10, true); !errors.Is(err, ErrPlacement) {
		t.Error("incomplete fleet accepted")
	}
}

func TestRandomFleetValid(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 50 {
		for _, n := range []int{10, 12} {
			if err := CheckFleet(RandomFleet(r, n, i%2 == 0), n, i%2 == 0); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCoord(t *testing.T) {
	if Coord(1, 6) != "B7" {
		t.Error(Coord(1, 6))
	}
	if x, y, err := ParseCoord("l12", 12); err != nil || x != 11 || y != 11 {
		t.Error(x, y, err)
	}
	for _, bad := range []string{"K1", "A0", "A11", "1A", "", "AA1"} {
		if _, _, err := ParseCoord(bad, 10); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func newGame(v Variant) *Game {
	g := &Game{PlayerA: "a", PlayerB: "b", Variant: v, Status: Setup, AutoWater: true}
	_ = g.Place("a", fleet())
	_ = g.Place("b", fleet())
	return g
}

func TestClassicTurnsAndWin(t *testing.T) {
	g := newGame(Classic)
	g.AutoWater = false
	if g.Status != Playing || g.Turn != "a" {
		t.Fatal("not started")
	}
	if _, err := g.Fire("b", [][2]int{{0, 0}}, time.Now()); !errors.Is(err, ErrNotTurn) {
		t.Error("b fired out of turn")
	}
	s, _ := g.Fire("a", [][2]int{{0, 0}}, time.Now())
	if s[0].Result != Hit || g.Turn != "b" {
		t.Errorf("hit: %+v turn %s", s, g.Turn)
	}
	g.Fire("b", [][2]int{{9, 9}}, time.Now())
	if _, err := g.Fire("a", [][2]int{{0, 0}}, time.Now()); !errors.Is(err, ErrShot) {
		t.Error("double shot accepted")
	}
	// a sinks everything; b always misses somewhere fresh.
	next := 98 // J10 was shot above
	for _, sh := range fleet() {
		for _, c := range sh.Cells() {
			if c == [2]int{0, 0} {
				continue
			}
			if g.Status == Finished {
				break
			}
			if g.Turn == "b" {
				if _, err := g.Fire("b", [][2]int{{next % 10, next / 10}}, time.Now()); err != nil {
					t.Fatal(err)
				}
				next--
			}
			if _, err := g.Fire("a", [][2]int{c}, time.Now()); err != nil {
				t.Fatalf("fire %v: %v", c, err)
			}
		}
	}
	if g.Status != Finished || g.Winner != "a" {
		t.Fatalf("status %s winner %s", g.Status, g.Winner)
	}
}

func TestStreakAndAutoWater(t *testing.T) {
	g := newGame(Streak)
	g.Fire("a", [][2]int{{0, 6}}, time.Now())
	if g.Turn != "a" {
		t.Fatal("streak: hit must keep the turn")
	}
	s, _ := g.Fire("a", [][2]int{{1, 6}}, time.Now())
	if s[0].Result != Sunk {
		t.Fatalf("want sunk, got %s", s[0].Result)
	}
	if _, err := g.Fire("a", [][2]int{{2, 6}}, time.Now()); !errors.Is(err, ErrShot) {
		t.Error("cell next to sunk ship accepted with auto water")
	}
	g.Fire("a", [][2]int{{9, 9}}, time.Now())
	if g.Turn != "b" {
		t.Error("streak: miss must pass the turn")
	}
}

func TestSalvo(t *testing.T) {
	g := newGame(Salvo)
	if n := g.ShotsPerTurn("a"); n != 5 {
		t.Fatalf("salvo size %d", n)
	}
	if _, err := g.Fire("a", [][2]int{{9, 9}}, time.Now()); !errors.Is(err, ErrShot) {
		t.Error("single shot accepted in salvo")
	}
	if _, err := g.Fire("a", [][2]int{{9, 9}, {9, 8}, {9, 7}, {9, 6}, {9, 9}}, time.Now()); !errors.Is(err, ErrShot) {
		t.Error("duplicate cell in salvo accepted")
	}
	if _, err := g.Fire("a", [][2]int{{9, 9}, {9, 8}, {9, 7}, {9, 6}, {9, 0}}, time.Now()); err != nil || g.Turn != "b" {
		t.Fatalf("salvo: %v", err)
	}
	// b sinks a's battleship: a's next salvo has 4 shots.
	g.Fire("b", [][2]int{{0, 0}, {1, 0}, {2, 0}, {3, 0}, {4, 0}}, time.Now())
	if n := g.ShotsPerTurn("a"); n != 4 {
		t.Errorf("after losing the battleship: %d", n)
	}
}
