# ADR-20261115: Markmap mind maps on pages

Status: Proposed

## Context
Mermaid mindmaps lay out radially and get messy with many nodes; even the
tidy-tree layout did not look good enough. Markus wants mind maps that look
like [Markmap](https://markmap.js.org/): Markdown as the source, collapsible
branches, and Markmap's own options (theme, layout, foldLevel, …) and magic
comments, as documented. (stated)

A first version (0.11–0.12) used the legacy `markmap-view` 0.18 with a
server-built tree and a hand-checked subset of options; Markmap 1.x had
replaced it with new packages and option names (`foldLevel` instead of
`initialExpandLevel`, `theme`, `layout`).

## Decision
- A ```` ```markmap ```` block on a page is left as text by the server
  (like Mermaid) and drawn in the browser by Markmap 1.x:
  `@markmap/parse` reads the Markdown, the front matter and the magic
  comments, `@markmap/view` draws. Markmap behaves as its docs say.
- Markmap 1.x ships only ES modules with npm dependencies, so
  `tools/markmap` bundles the two packages with esbuild (`make
  vendor-markmap`) into `ui/static/vendor/markmap-<version>*.js` (gzipped,
  version in every name, cached immutable) plus one license file for
  everything bundled. esbuild is a build tool only. The bundle is ~130 KB
  gzipped; code highlighting (~54 KB) loads only when a map shows code.
- `ui/static/seiten.js` imports the bundle only on pages with a markmap
  block. The colour scheme follows Allmend (light/dark) unless the block sets
  `colorScheme`.
- Options are not checked on save. Markmap ignores invalid or unknown
  options and reports them as warnings; they are listed under the map. A
  person writes and immediately sees the page, so the warning is the
  feedback. "Never shit in, shit out" is about technical interfaces (APIs,
  imports, the database), not this display setting. (stated)
- Like Mermaid, Markmap adds `<style>` elements, so these pages get the same
  relaxed `style-src` (`cspDiagram`); `script-src` stays `'self'`. No
  requests to other hosts.
- Without JavaScript (and in Finanzen/Velo notes, which load no scripts)
  the Markdown source stays visible, as for Mermaid.

## Why
The look and options he asked for, exactly as documented by Markmap, with
almost no code of ours to keep in step with it, no external requests, and a
smaller page than the 0.18 version (d3 + markmap-view, ~104 KB).

## Consequences
- Updating Markmap: change the versions in `tools/markmap/package.json`, run
  `make vendor-markmap`, adjust the import path in `seiten.js` (a test
  checks that it exists).
- Raw HTML in a block is dropped by Markmap, not by goldmark; Markmap's
  sanitizer decides what reaches the SVG.
