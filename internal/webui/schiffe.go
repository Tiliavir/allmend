package webui

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/schiffe"
)

// hub notifies open game pages (Server-Sent Events) about new moves.
type hub struct {
	mu   sync.Mutex
	subs map[string]map[chan int]struct{}
}

func (h *hub) subscribe(game string) chan int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[string]map[chan int]struct{}{}
	}
	if h.subs[game] == nil {
		h.subs[game] = map[chan int]struct{}{}
	}
	ch := make(chan int, 1)
	h.subs[game][ch] = struct{}{}
	return ch
}

func (h *hub) unsubscribe(game string, ch chan int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs[game], ch)
	if len(h.subs[game]) == 0 {
		delete(h.subs, game)
	}
}

func (h *hub) publish(game string, version int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[game] {
		select {
		case ch <- version:
		default: // a pending notification is enough
		}
	}
}

func (s *Server) schiffeRoutes(mux *http.ServeMux) {
	m := func(h func(http.ResponseWriter, *http.Request, account.Account)) http.Handler {
		return s.Module(account.Schiffe, h)
	}
	mux.Handle("GET /schiffe", m(s.schiffeList))
	mux.Handle("POST /schiffe", m(s.schiffeChallenge))
	mux.Handle("GET /schiffe/{id}", m(s.schiffeGame))
	mux.Handle("POST /schiffe/{id}/aufstellung", m(s.schiffePlace))
	mux.Handle("POST /schiffe/{id}/schuss", m(s.schiffeFire))
	mux.Handle("GET /schiffe/{id}/ereignisse", m(s.schiffeEvents))
}

type gameRow struct {
	schiffe.Game
	Opponent string
	MyTurn   bool
	Won      bool
	Variant  string
}

type schiffeList struct {
	Games     []gameRow
	Opponents []account.Account
	Variants  []schiffe.VariantInfo
}

func variantName(v schiffe.Variant) string {
	for _, i := range schiffe.Variants {
		if i.ID == v {
			return i.Name
		}
	}
	return string(v)
}

func (s *Server) schiffeList(w http.ResponseWriter, r *http.Request, a account.Account) {
	s.renderSchiffeList(w, r, a, http.StatusOK, "")
}

func (s *Server) renderSchiffeList(w http.ResponseWriter, r *http.Request, a account.Account, status int, errMsg string) {
	games, err := s.Schiffe.Repo.Games(r.Context(), a.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names, all, err := s.names(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := schiffeList{Variants: schiffe.Variants}
	for _, g := range games {
		d.Games = append(d.Games, gameRow{Game: g, Opponent: names[g.Opponent(a.ID)], MyTurn: g.Turn == a.ID, Won: g.Winner == a.ID, Variant: variantName(g.Variant)})
	}
	for _, c := range all {
		if c.ID != a.ID && c.Status == account.Active && c.Can(account.Schiffe) {
			d.Opponents = append(d.Opponents, c)
		}
	}
	s.render(w, r, status, "schiffe/list", Page{Title: "Schiffe versenken", Module: account.Schiffe, Error: errMsg, Data: d})
}

func (s *Server) schiffeChallenge(w http.ResponseWriter, r *http.Request, a account.Account) {
	g, err := s.Schiffe.Challenge(r.Context(), a, r.PostFormValue("gegner"), schiffe.Variant(r.PostFormValue("variante")),
		r.PostFormValue("beruehren") == "1", r.PostFormValue("wasser") == "1")
	if errors.Is(err, schiffe.ErrState) {
		s.renderSchiffeList(w, r, a, http.StatusUnprocessableEntity, "Das Spiel konnte nicht angelegt werden: "+err.Error()+".")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.hub.publish(g.ID, g.Version)
	s.redirect(w, r, "/schiffe/"+g.ID, "Spiel angelegt. Stell deine Flotte auf.")
}

// ── board view ────────────────────────────────────────

type cell struct {
	Coord string
	Class string // empty, ship, hit, sunk, miss, blocked
	Label string
	Open  bool // may be shot now
}

type boardRow struct {
	Label string
	Cells []cell
}

type board struct {
	Cols []string
	Rows []boardRow
}

type gameView struct {
	Game      schiffe.Game
	Me        string
	Opponent  string
	Variant   schiffe.VariantInfo
	MyTurn    bool
	Placed    bool
	Shots     int // shots per turn
	Salvo     bool
	Own       board
	Target    board
	LastShots []string
	Won       bool
	// setup form
	Proposal []shipField
	Size     int
}

type shipField struct {
	Name, Start string
	Size        int
	Horizontal  bool
}

func makeBoard(g schiffe.Game, owner string, showShips, shootable bool) board {
	n := g.Variant.Size()
	b := board{}
	for x := range n {
		b.Cols = append(b.Cols, string(rune('A'+x)))
	}
	shots := map[[2]int]schiffe.Result{}
	for _, sh := range g.ShotsAt(owner) {
		shots[[2]int{sh.X, sh.Y}] = sh.Result
	}
	sunk := map[[2]int]bool{}
	for _, sh := range g.SunkShips(owner) {
		for _, c := range sh.Cells() {
			sunk[c] = true
		}
	}
	ships := map[[2]int]bool{}
	if showShips {
		for _, sh := range g.FleetOf(owner) {
			for _, c := range sh.Cells() {
				ships[c] = true
			}
		}
	}
	blocked := g.Blocked(owner)
	for y := range n {
		row := boardRow{Label: strconv.Itoa(y + 1)}
		for x := range n {
			c := [2]int{x, y}
			k := cell{Coord: schiffe.Coord(x, y), Class: "empty", Label: "offen"}
			res, shot := shots[c]
			switch {
			case sunk[c]:
				k.Class, k.Label = "sunk", "versenkt"
			case shot && res != schiffe.Miss:
				k.Class, k.Label = "hit", "Treffer"
			case shot:
				k.Class, k.Label = "miss", "Wasser"
			case blocked[c]:
				k.Class, k.Label = "blocked", "Wasser (neben versenktem Schiff)"
			case ships[c]:
				k.Class, k.Label = "ship", "Schiff"
			default:
				k.Open = shootable
			}
			row.Cells = append(row.Cells, k)
		}
		b.Rows = append(b.Rows, row)
	}
	return b
}

func (s *Server) schiffeGame(w http.ResponseWriter, r *http.Request, a account.Account) {
	g, err := s.Schiffe.Load(r.Context(), a, r.PathValue("id"))
	if errors.Is(err, schiffe.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderGame(w, r, a, g, http.StatusOK, "", nil)
}

func (s *Server) renderGame(w http.ResponseWriter, r *http.Request, a account.Account, g schiffe.Game, status int, errMsg string, proposal []schiffe.Ship) {
	names, _, err := s.names(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	opp := g.Opponent(a.ID)
	v := gameView{Game: g, Me: a.ID, Opponent: names[opp], MyTurn: g.Status == schiffe.Playing && g.Turn == a.ID,
		Placed: g.FleetOf(a.ID) != nil, Salvo: g.Variant == schiffe.Salvo, Won: g.Winner == a.ID, Size: g.Variant.Size()}
	for _, i := range schiffe.Variants {
		if i.ID == g.Variant {
			v.Variant = i
		}
	}
	if g.Status == schiffe.Setup && !v.Placed {
		if proposal == nil {
			proposal = schiffe.Proposal(g.Variant, g.Touching)
		}
		for _, sh := range proposal {
			v.Proposal = append(v.Proposal, shipField{Name: schiffe.ShipNames[sh.Size], Start: schiffe.Coord(sh.X, sh.Y), Size: sh.Size, Horizontal: sh.Horizontal})
		}
		v.Own = makeBoard(schiffe.Game{Variant: g.Variant, PlayerA: a.ID, FleetA: proposal}, a.ID, true, false)
	} else {
		v.Shots = g.ShotsPerTurn(a.ID)
		v.Own = makeBoard(g, a.ID, true, false)
		// After the game both fleets are revealed.
		v.Target = makeBoard(g, opp, g.Status == schiffe.Finished, v.MyTurn)
		for i := len(g.Shots) - 1; i >= 0 && len(v.LastShots) < 5; i-- {
			sh := g.Shots[i]
			who := "Du"
			if sh.Shooter != a.ID {
				who = names[sh.Shooter]
			}
			v.LastShots = append(v.LastShots, fmt.Sprintf("%s: %s – %s", who, schiffe.Coord(sh.X, sh.Y), map[schiffe.Result]string{schiffe.Miss: "Wasser", schiffe.Hit: "Treffer", schiffe.Sunk: "Treffer versenkt"}[sh.Result]))
		}
	}
	p := Page{Title: "Schiffe versenken", Module: account.Schiffe, Error: errMsg, Data: v,
		Scripts: []Script{{Src: "/static/schiffe.js", Module: true}}}
	s.render(w, r, status, "schiffe/game", p)
}

func (s *Server) schiffeGameErr(w http.ResponseWriter, r *http.Request, a account.Account, gameID string, err error, proposal []schiffe.Ship) {
	g, lerr := s.Schiffe.Load(r.Context(), a, gameID)
	if errors.Is(lerr, schiffe.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if lerr != nil {
		s.fail(w, r, lerr)
		return
	}
	switch {
	case errors.Is(err, schiffe.ErrPlacement), errors.Is(err, schiffe.ErrShot), errors.Is(err, schiffe.ErrNotTurn), errors.Is(err, schiffe.ErrState):
		s.renderGame(w, r, a, g, http.StatusUnprocessableEntity, err.Error()+".", proposal)
	case errors.Is(err, schiffe.ErrConflict):
		s.renderGame(w, r, a, g, http.StatusConflict, "Das Spiel hat sich gerade geändert – schau dir den neuen Stand an.", nil)
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) schiffePlace(w http.ResponseWriter, r *http.Request, a account.Account) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	g, err := s.Schiffe.Load(r.Context(), a, id)
	if errors.Is(err, schiffe.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ships, err := schiffe.ParseFleet(r.PostForm["start"], r.PostForm["richtung"], g.Variant.Size())
	if err == nil {
		g, err = s.Schiffe.Place(r.Context(), a, id, ships)
	}
	if err != nil {
		s.schiffeGameErr(w, r, a, id, err, ships)
		return
	}
	s.hub.publish(id, g.Version)
	s.redirect(w, r, "/schiffe/"+id, "Flotte aufgestellt.")
}

func (s *Server) schiffeFire(w http.ResponseWriter, r *http.Request, a account.Account) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		s.status(w, r, http.StatusBadRequest)
		return
	}
	g, err := s.Schiffe.Load(r.Context(), a, id)
	if errors.Is(err, schiffe.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var cells [][2]int
	for _, c := range r.PostForm["c"] {
		x, y, err := schiffe.ParseCoord(c, g.Variant.Size())
		if err != nil {
			s.schiffeGameErr(w, r, a, id, fmt.Errorf("%w: %v", schiffe.ErrShot, err), nil)
			return
		}
		cells = append(cells, [2]int{x, y})
	}
	g, shots, err := s.Schiffe.Fire(r.Context(), a, id, cells)
	if err != nil {
		s.schiffeGameErr(w, r, a, id, err, nil)
		return
	}
	s.hub.publish(id, g.Version)
	msg := ""
	for i, sh := range shots {
		if i > 0 {
			msg += ", "
		}
		msg += schiffe.Coord(sh.X, sh.Y) + ": " + map[schiffe.Result]string{schiffe.Miss: "Wasser", schiffe.Hit: "Treffer", schiffe.Sunk: "Treffer versenkt!"}[sh.Result]
	}
	if g.Status == schiffe.Finished {
		msg += " – gewonnen!"
	}
	s.redirect(w, r, "/schiffe/"+id, msg)
}

// schiffeEvents streams "update" events with the new game version.
func (s *Server) schiffeEvents(w http.ResponseWriter, r *http.Request, a account.Account) {
	id := r.PathValue("id")
	g, err := s.Schiffe.Load(r.Context(), a, id)
	if errors.Is(err, schiffe.ErrNotFound) {
		s.status(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		s.fail(w, r, errors.New("schiffe: response writer cannot flush"))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // DSM reverse proxy (nginx): do not buffer
	ch := s.hub.subscribe(id)
	defer s.hub.unsubscribe(id, ch)
	_, _ = fmt.Fprintf(w, "retry: 3000\nevent: update\ndata: %d\n\n", g.Version)
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case v := <-ch:
			if _, err := fmt.Fprintf(w, "event: update\ndata: %d\n\n", v); err != nil {
				slog.Warn("sse write", "err", err)
				return
			}
			fl.Flush()
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
