# ADR-20261115: Markmap mind maps on pages

Status: Proposed

## Context
Mermaid mindmaps lay out radially and get messy with many nodes; even the
tidy-tree layout did not look good enough. Markus wants mind maps that look
like [Markmap](https://markmap.js.org/): a left-to-right tree with
collapsible branches, real strikethrough, Markdown as the source. (stated)

## Decision
- A ```` ```markmap ```` block on a page is drawn as a mind map. The source
  is plain Markdown: headings nest by level, list items by indentation, any
  other block is a leaf (as Markmap does).
- The server turns the block into Markmap's node tree with goldmark (same
  safe mode as the page: raw HTML dropped, URLs filtered) and puts it on the
  block as `data-markmap`. So `markmap-lib` and its Markdown parser are not
  needed in the browser; only `markmap-view` draws.
- Vendored, pinned, gzipped, loaded only on pages with a markmap block:
  `d3` 7.9.0 (ISC, ~92 KB gz) and `markmap-view` 0.18.13 (MIT, ~12 KB gz).
  No autoloader: it would fetch from a CDN.
- Like Mermaid, Markmap writes a `<style>` into its SVG, so these pages get
  the same relaxed `style-src` (`cspDiagram`); `script-src` stays `'self'`.
- Defaults: groups keep their colour from level 2, three levels open,
  nodes at most 300 px wide. A block may override them in front matter in
  Markmap's own format (`markmap:` with indented keys), but only
  `initialExpandLevel` (-1 = all, or ≥ 1), `colorFreezeLevel` (≥ 0) and
  `maxWidth` (≥ 0), as integers. Anything else in the front matter (other
  keys, other values, a missing closing `---`) is rejected when the page is
  saved, with the reason in the form. The server checks the options and
  passes them as `data-markmap-options`. (stated: per-page setting via front
  matter, 2026-11-16)
- Without JavaScript (and in Finanzen/Velo notes, which load no scripts)
  the Markdown source stays visible, as for Mermaid.

## Why
The look he asked for, with the least code in the browser and no external
requests. Markdown lists are easier to write and read than Mermaid's
mindmap syntax (no ids, no shape brackets, strikethrough works).

## Consequences
- Two more vendored files to keep pinned and updated together (`d3` is a
  dependency of `markmap-view`).
- Page saves render the body once to validate markmap blocks.
