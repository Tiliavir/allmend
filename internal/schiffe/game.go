package schiffe

import (
	"errors"
	"fmt"
	"time"
)

// Status of a game.
type Status string

// Game states.
const (
	Setup    Status = "aufstellung"
	Playing  Status = "laeuft"
	Finished Status = "beendet"
)

// Result of a shot.
type Result string

// Shot results.
const (
	Miss Result = "wasser"
	Hit  Result = "treffer"
	Sunk Result = "versenkt"
)

// Shot is one fired cell.
type Shot struct {
	Seq     int
	Shooter string // account id
	X, Y    int
	Result  Result
	At      time.Time
}

// Game is the complete state; only the server sees both fleets.
type Game struct {
	ID        string
	PlayerA   string // challenger
	PlayerB   string
	Variant   Variant
	Touching  bool // ships may touch
	AutoWater bool // cells around a sunk ship count as water
	Status    Status
	Turn      string // account id whose move it is
	Winner    string
	FleetA    []Ship // nil until placed
	FleetB    []Ship
	Shots     []Shot
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Errors.
var (
	ErrNotFound = errors.New("kein solches Spiel")
	ErrNotYours = errors.New("du spielst in diesem Spiel nicht mit")
	ErrNotTurn  = errors.New("du bist nicht am Zug")
	ErrState    = errors.New("das geht in diesem Spielstand nicht")
	ErrShot     = errors.New("ungültiger Schuss")
	ErrConflict = errors.New("das Spiel hat sich inzwischen geändert")
)

// Opponent returns the other player.
func (g *Game) Opponent(p string) string {
	if p == g.PlayerA {
		return g.PlayerB
	}
	return g.PlayerA
}

// IsPlayer reports whether p plays in g.
func (g *Game) IsPlayer(p string) bool { return p == g.PlayerA || p == g.PlayerB }

// FleetOf returns p's fleet.
func (g *Game) FleetOf(p string) []Ship {
	if p == g.PlayerA {
		return g.FleetA
	}
	return g.FleetB
}

// Place sets p's fleet during setup. The game starts once both have placed;
// the challenger (A) begins.
func (g *Game) Place(p string, ships []Ship) error {
	if !g.IsPlayer(p) {
		return ErrNotYours
	}
	if g.Status != Setup {
		return ErrState
	}
	if err := CheckFleet(ships, g.Variant.Size(), g.Touching); err != nil {
		return err
	}
	if p == g.PlayerA {
		g.FleetA = ships
	} else {
		g.FleetB = ships
	}
	if g.FleetA != nil && g.FleetB != nil {
		g.Status, g.Turn = Playing, g.PlayerA
	}
	return nil
}

// ShotsAt returns the shots fired at target (by the other player).
func (g *Game) ShotsAt(target string) []Shot {
	var out []Shot
	for _, s := range g.Shots {
		if s.Shooter != target {
			out = append(out, s)
		}
	}
	return out
}

func hitSet(shots []Shot) map[[2]int]bool {
	m := map[[2]int]bool{}
	for _, s := range shots {
		m[[2]int{s.X, s.Y}] = true
	}
	return m
}

// SunkShips returns target's ships that are completely hit.
func (g *Game) SunkShips(target string) []Ship {
	hits := hitSet(g.ShotsAt(target))
	var out []Ship
	for _, s := range g.FleetOf(target) {
		all := true
		for _, c := range s.Cells() {
			all = all && hits[c]
		}
		if all {
			out = append(out, s)
		}
	}
	return out
}

// Blocked returns cells next to target's sunk ships (water by AutoWater).
func (g *Game) Blocked(target string) map[[2]int]bool {
	m := map[[2]int]bool{}
	if !g.AutoWater {
		return m
	}
	n := g.Variant.Size()
	for _, s := range g.SunkShips(target) {
		for _, c := range s.Cells() {
			for dx := -1; dx <= 1; dx++ {
				for dy := -1; dy <= 1; dy++ {
					x, y := c[0]+dx, c[1]+dy
					if x >= 0 && y >= 0 && x < n && y < n && !s.Has(x, y) {
						m[[2]int{x, y}] = true
					}
				}
			}
		}
	}
	return m
}

// ShotsPerTurn for p in the current state.
func (g *Game) ShotsPerTurn(p string) int {
	if g.Variant != Salvo {
		return 1
	}
	sunk := g.SunkShips(p)
	largest := 0
	for _, s := range g.FleetOf(p) {
		if !containsShip(sunk, s) && s.Size > largest {
			largest = s.Size
		}
	}
	return largest
}

func containsShip(ss []Ship, s Ship) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// Fire lets p shoot at the given cells (exactly ShotsPerTurn of them).
func (g *Game) Fire(p string, cells [][2]int, now time.Time) ([]Shot, error) {
	if !g.IsPlayer(p) {
		return nil, ErrNotYours
	}
	if g.Status != Playing {
		return nil, ErrState
	}
	if g.Turn != p {
		return nil, ErrNotTurn
	}
	want := g.ShotsPerTurn(p)
	if len(cells) != want {
		return nil, fmt.Errorf("%w: genau %d Schuss erwartet", ErrShot, want)
	}
	target := g.Opponent(p)
	n := g.Variant.Size()
	done := hitSet(g.ShotsAt(target))
	blocked := g.Blocked(target)
	seen := map[[2]int]bool{}
	for _, c := range cells {
		switch {
		case c[0] < 0 || c[1] < 0 || c[0] >= n || c[1] >= n:
			return nil, fmt.Errorf("%w: außerhalb des Spielfelds", ErrShot)
		case done[c] || seen[c]:
			return nil, fmt.Errorf("%w: auf %s wurde schon geschossen", ErrShot, Coord(c[0], c[1]))
		case blocked[c]:
			return nil, fmt.Errorf("%w: %s liegt neben einem versenkten Schiff", ErrShot, Coord(c[0], c[1]))
		}
		seen[c] = true
	}
	var fired []Shot
	anyHit := false
	for _, c := range cells {
		res := Miss
		for _, s := range g.FleetOf(target) {
			if s.Has(c[0], c[1]) {
				res, anyHit = Hit, true
				break
			}
		}
		shot := Shot{Seq: len(g.Shots) + 1, Shooter: p, X: c[0], Y: c[1], Result: res, At: now}
		g.Shots = append(g.Shots, shot)
		if res == Hit {
			for _, s := range g.SunkShips(target) {
				if s.Has(c[0], c[1]) {
					g.Shots[len(g.Shots)-1].Result = Sunk
				}
			}
		}
		fired = append(fired, g.Shots[len(g.Shots)-1])
	}
	if len(g.SunkShips(target)) == len(g.FleetOf(target)) {
		g.Status, g.Winner, g.Turn = Finished, p, ""
		return fired, nil
	}
	if g.Variant != Streak || !anyHit {
		g.Turn = target
	}
	return fired, nil
}
