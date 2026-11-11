package schiffe

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/id"
)

// Repository stores games.
type Repository interface {
	Games(ctx context.Context, player string) ([]Game, error) // newest first, without ships/shots
	Get(ctx context.Context, id string) (Game, error)
	Insert(ctx context.Context, g Game) error
	// Save writes the game's state, new ships and new shots if the stored
	// version is want.
	Save(ctx context.Context, g Game, want int) error
}

// Service implements the game use cases.
type Service struct {
	Repo     Repository
	Accounts interface {
		Get(ctx context.Context, id string) (account.Account, error)
	}
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Challenge creates a game of a against opponent.
func (s *Service) Challenge(ctx context.Context, a account.Account, opponent string, v Variant, touching, autoWater bool) (Game, error) {
	if !KnownVariant(v) {
		return Game{}, fmt.Errorf("%w: unbekannte Variante", ErrState)
	}
	o, err := s.Accounts.Get(ctx, opponent)
	if err != nil || o.ID == a.ID || o.Status != account.Active || !o.Can(account.Schiffe) {
		return Game{}, fmt.Errorf("%w: diese Person kann nicht mitspielen", ErrState)
	}
	now := s.now()
	g := Game{ID: id.New(), PlayerA: a.ID, PlayerB: o.ID, Variant: v, Touching: touching, AutoWater: autoWater,
		Status: Setup, Version: 1, CreatedAt: now, UpdatedAt: now}
	return g, s.Repo.Insert(ctx, g)
}

// Load returns a game a plays in.
func (s *Service) Load(ctx context.Context, a account.Account, gameID string) (Game, error) {
	g, err := s.Repo.Get(ctx, gameID)
	if err != nil {
		return g, err
	}
	if !g.IsPlayer(a.ID) {
		return Game{}, ErrNotFound
	}
	return g, nil
}

// Place stores a's fleet and mines.
func (s *Service) Place(ctx context.Context, a account.Account, gameID string, ships []Ship, mines [][2]int) (Game, error) {
	g, err := s.Load(ctx, a, gameID)
	if err != nil {
		return g, err
	}
	if g.FleetOf(a.ID) != nil {
		return g, fmt.Errorf("%w: deine Flotte steht schon", ErrState)
	}
	want := g.Version
	if err := g.Place(a.ID, ships, mines); err != nil {
		return g, err
	}
	g.Version, g.UpdatedAt = want+1, s.now()
	return g, s.Repo.Save(ctx, g, want)
}

// Fire shoots for a; ship is the firing ship (Mines variant) or NoShip.
func (s *Service) Fire(ctx context.Context, a account.Account, gameID string, cells [][2]int, ship int) (Game, []Shot, error) {
	g, err := s.Load(ctx, a, gameID)
	if err != nil {
		return g, nil, err
	}
	want := g.Version
	now := s.now()
	shots, err := g.Fire(a.ID, cells, ship, now)
	if err != nil {
		return g, nil, err
	}
	g.Version, g.UpdatedAt = want+1, now
	return g, shots, s.Repo.Save(ctx, g, want)
}

// Proposal is a random fleet with the variant's mines for the setup form.
func Proposal(v Variant, touching bool) ([]Ship, [][2]int) {
	return RandomSetup(rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())), v.Size(), v.Mines(), touching) //nolint:gosec // game randomness
}

// ParseMines reads the mine cells of the setup form; want is the variant's
// number of mines (none: the form must not send any).
func ParseMines(cells []string, want, n int) ([][2]int, error) {
	if len(cells) != want {
		return nil, fmt.Errorf("%w: genau %d Seeminen erwartet", ErrPlacement, want)
	}
	var out [][2]int
	for i, c := range cells {
		x, y, err := ParseCoord(strings.TrimSpace(c), n)
		if err != nil {
			return nil, fmt.Errorf("%w: Seemine %d: %v", ErrPlacement, i+1, err)
		}
		out = append(out, [2]int{x, y})
	}
	return out, nil
}

// ParseFleet reads the setup form: for each ship of Fleet, in order, a start
// cell ("B3") and a direction ("h" or "v").
func ParseFleet(starts, dirs []string, n int) ([]Ship, error) {
	if len(starts) != len(Fleet) || len(dirs) != len(Fleet) {
		return nil, fmt.Errorf("%w: unvollständige Flotte", ErrPlacement)
	}
	ships := make([]Ship, len(Fleet))
	for i, size := range Fleet {
		x, y, err := ParseCoord(strings.TrimSpace(starts[i]), n)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %d: %v", ErrPlacement, ShipNames[size], i+1, err)
		}
		var h bool
		switch dirs[i] {
		case "h":
			h = true
		case "v":
		default:
			return nil, fmt.Errorf("%w: Richtung muss waagerecht oder senkrecht sein", ErrPlacement)
		}
		ships[i] = Ship{Size: size, X: x, Y: y, Horizontal: h}
	}
	return ships, nil
}
