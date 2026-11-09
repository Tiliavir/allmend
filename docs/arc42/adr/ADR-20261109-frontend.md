# ADR-20261109: Frontend: HTML and CSS first

Status: Accepted

## Decision
- Server-rendered `html/template`. Plain forms with POST/redirect/GET; no htmx,
  no Alpine.js.
- JavaScript only where HTML cannot do it (chart filters, map, Mermaid, live
  game updates, ticking checklist items): small native ES modules, no bundler.
- Plain CSS (nesting, custom properties, `prefers-color-scheme`); Sass removed.
- Vendored, pinned, loaded only on pages that need them: Chart.js, Leaflet,
  Mermaid. No external requests at runtime; system font stack.
- Strict CSP (`script-src 'self'`, `style-src 'self'`, no inline styles).
- UI German only.
- Visual direction D of the prototype canvas: layout and calm of "Allmend",
  module colours of "Dorfplatz" (each module has its own colour).

## Why
Small payloads and fast pages on any device; fewer dependencies; explicit
behaviour without a client framework (stated principles).
