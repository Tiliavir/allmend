# ADR-20261111: Schiffe versenken – Seeminen and drag-and-drop setup

Status: Proposed

Extends [Schiffe versenken with Server-Sent Events](ADR-20261109-battleship-sse.md)
(issue #5).

## Decision

- **Seeminen is a fifth variant** (10 × 10, alternate shots), not a setting
  that combines with the others. Each side places its fleet plus three sea
  mines (one cell each).
- Mines may not lie on a ship or on each other. When ships may not touch,
  mines may not touch anything either (otherwise "auto water" next to a sunk
  ship could defuse a mine).
- Every shot names the firing ship, which must be afloat. A shot on a mine
  sinks that ship; the mine is used up. The lost ship counts as sunk and is
  shown as sunk to the opponent (as if shot down), so nobody fires at a
  wreck. Losing the last ship on a mine loses the game.
- The ship selection is preselected: the ship used last if afloat, otherwise
  the smallest one (a mine then costs least).
- Setup with JS: tap a ship to turn it around its middle, drag ships and
  mines to move them. While editing, overlaps (and touching, if not allowed)
  are allowed and shown red; the landing zone of a drag is grey. A turned or
  moved ship is pushed back onto the board at the edge. The server checks
  the fleet on submit as before; without JS the list form does the same.
- Schema step 10 rebuilds `games`, `game_ships` and `game_shots` (SQLite
  changes CHECK constraints only by rebuild) and adds `game_mines`;
  `game_shots.ship` names the firing ship (NULL in older games).

## Why

Rules from the paper variant "Seeminen-Runde"; the details above are not
fixed there and were decided by the agent for review.
