// Package schiffe is the game of battleships for two Allmend accounts. The
// server is the referee: it checks every placement and shot, and a player
// never receives the opponent's fleet.
package schiffe

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
)

// Variant of the game.
type Variant string

// Variants (rules from the classic paper game).
const (
	Classic Variant = "klassisch"     // 10×10, alternate shots
	Streak  Variant = "treffer-serie" // a hit lets the shooter fire again
	Salvo   Variant = "salve"         // shots per turn = size of own largest ship afloat
	BigSea  Variant = "grosses-meer"  // 12×12, alternate shots
)

// VariantInfo describes a variant for the UI.
type VariantInfo struct {
	ID          Variant
	Name, Rules string
}

// Variants in display order.
var Variants = []VariantInfo{
	{Classic, "Klassisch", "10 × 10 Felder, abwechselnd ein Schuss."},
	{Streak, "Treffer-Serie", "Wer trifft, schießt gleich nochmals."},
	{Salvo, "Salven-Modus", "Pro Zug so viele Schüsse, wie dein größtes Schiff noch Felder hat."},
	{BigSea, "Großes Meer", "12 × 12 Felder, abwechselnd ein Schuss."},
}

// KnownVariant reports whether v exists.
func KnownVariant(v Variant) bool {
	return slices.ContainsFunc(Variants, func(i VariantInfo) bool { return i.ID == v })
}

// Size is the board width and height.
func (v Variant) Size() int {
	if v == BigSea {
		return 12
	}
	return 10
}

// Fleet lists ship lengths: one battleship, two cruisers, three destroyers,
// four submarines.
var Fleet = []int{5, 4, 4, 3, 3, 3, 2, 2, 2, 2}

// ShipNames by length.
var ShipNames = map[int]string{5: "Schlachtschiff", 4: "Kreuzer", 3: "Zerstörer", 2: "U-Boot"}

// Ship is placed with its first cell at (X, Y), X = column, Y = row, 0-based.
type Ship struct {
	Size       int
	X, Y       int
	Horizontal bool
}

// Cells returns the cells a ship covers.
func (s Ship) Cells() [][2]int {
	out := make([][2]int, s.Size)
	for i := range s.Size {
		if s.Horizontal {
			out[i] = [2]int{s.X + i, s.Y}
		} else {
			out[i] = [2]int{s.X, s.Y + i}
		}
	}
	return out
}

// Has reports whether the ship covers (x, y).
func (s Ship) Has(x, y int) bool {
	if s.Horizontal {
		return y == s.Y && x >= s.X && x < s.X+s.Size
	}
	return x == s.X && y >= s.Y && y < s.Y+s.Size
}

// ErrPlacement explains why a fleet is not valid.
var ErrPlacement = errors.New("ungültige Aufstellung")

// CheckFleet validates a complete fleet for a board of size n. With touching
// false, ships may not touch, not even diagonally.
func CheckFleet(ships []Ship, n int, touching bool) error {
	want := slices.Clone(Fleet)
	got := make([]int, len(ships))
	for i, s := range ships {
		got[i] = s.Size
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		return fmt.Errorf("%w: die Flotte muss aus 1 Schlachtschiff (5), 2 Kreuzern (4), 3 Zerstörern (3) und 4 U-Booten (2) bestehen", ErrPlacement)
	}
	occ := map[[2]int]int{}
	for i, s := range ships {
		for _, c := range s.Cells() {
			if c[0] < 0 || c[1] < 0 || c[0] >= n || c[1] >= n {
				return fmt.Errorf("%w: %s %s ragt über den Rand", ErrPlacement, ShipNames[s.Size], Coord(s.X, s.Y))
			}
			if _, ok := occ[c]; ok {
				return fmt.Errorf("%w: Schiffe überlappen bei %s", ErrPlacement, Coord(c[0], c[1]))
			}
			occ[c] = i
		}
	}
	if !touching {
		for c, i := range occ {
			for dx := -1; dx <= 1; dx++ {
				for dy := -1; dy <= 1; dy++ {
					if j, ok := occ[[2]int{c[0] + dx, c[1] + dy}]; ok && j != i {
						return fmt.Errorf("%w: Schiffe berühren sich bei %s", ErrPlacement, Coord(c[0], c[1]))
					}
				}
			}
		}
	}
	return nil
}

// RandomFleet places the fleet at random; it restarts when stuck.
func RandomFleet(r *rand.Rand, n int, touching bool) []Ship {
	for {
		var ships []Ship
		ok := true
		for _, size := range Fleet {
			placed := false
			for range 200 {
				s := Ship{Size: size, Horizontal: r.IntN(2) == 0}
				if s.Horizontal {
					s.X, s.Y = r.IntN(n-size+1), r.IntN(n)
				} else {
					s.X, s.Y = r.IntN(n), r.IntN(n-size+1)
				}
				if checkPartial(append(slices.Clone(ships), s), n, touching) {
					ships = append(ships, s)
					placed = true
					break
				}
			}
			if !placed {
				ok = false
				break
			}
		}
		if ok {
			return ships
		}
	}
}

func checkPartial(ships []Ship, n int, touching bool) bool {
	occ := map[[2]int]int{}
	for i, s := range ships {
		for _, c := range s.Cells() {
			if c[0] < 0 || c[1] < 0 || c[0] >= n || c[1] >= n {
				return false
			}
			if _, ok := occ[c]; ok {
				return false
			}
			occ[c] = i
		}
	}
	if touching {
		return true
	}
	for c, i := range occ {
		for dx := -1; dx <= 1; dx++ {
			for dy := -1; dy <= 1; dy++ {
				if j, ok := occ[[2]int{c[0] + dx, c[1] + dy}]; ok && j != i {
					return false
				}
			}
		}
	}
	return true
}

// Coord names a cell like "B7" (column letter, row number).
func Coord(x, y int) string { return fmt.Sprintf("%c%d", 'A'+x, y+1) }

// ParseCoord reads "B7" for a board of size n.
func ParseCoord(s string, n int) (int, int, error) {
	if len(s) < 2 || len(s) > 3 {
		return 0, 0, fmt.Errorf("%q ist kein Feld", s)
	}
	c := s[0]
	if c >= 'a' && c <= 'z' {
		c -= 'a' - 'A'
	}
	x := int(c) - 'A'
	y := 0
	for _, d := range s[1:] {
		if d < '0' || d > '9' {
			return 0, 0, fmt.Errorf("%q ist kein Feld", s)
		}
		y = y*10 + int(d-'0')
	}
	y--
	if x < 0 || x >= n || y < 0 || y >= n {
		return 0, 0, fmt.Errorf("%q liegt nicht auf dem Spielfeld", s)
	}
	return x, y, nil
}
