# ADR-20261109: Schiffe versenken with Server-Sent Events

Status: Accepted

## Decision
- Two accounts play against each other, turn-based; a game survives restarts
  (state in SQLite, moves as an append-only log).
- The server is the referee: it validates every shot and never sends the
  opponent's fleet to the client.
- Live updates via Server-Sent Events (stdlib, `EventSource`); plus an
  "Aktualisieren" button. Response header `X-Accel-Buffering: no` for the DSM
  reverse proxy.
- Variants at start: Klassisch, Treffer-Serie, Salven-Modus, Großes Meer
  (12×12). Settings per game: ships may touch yes/no; mark cells around a sunk
  ship as water yes/no. Seeminen later (#5).

## Why
Polling every few seconds feels slow when players sit side by side (stated);
SSE is a web standard and needs no dependency.

## Implementation notes

- Fleet: 1 × 5, 2 × 4, 3 × 3, 4 × 2 (10 ships) on 10 × 10, or 12 × 12 for
  Großes Meer. Placement: a random proposal the player may edit (start cell
  and direction per ship, plain form) or re-roll; the server checks it.
- The challenger starts. Tables `games`, `game_ships`, `game_shots`; every
  move is a versioned update (no lost moves when both act at once).
- SSE sends the game version; the page reloads only if it changed. An
  in-process hub notifies open pages (single instance on the NAS).

