# ADR-20261109: Page tree

Status: Accepted

Extends [Pages (mini wiki)](ADR-20261109-pages.md).

## Decision
- A page has an optional parent and a position among its siblings
  (`parent_id`, `position`; migration 0009). Existing pages stay on the top
  level, sorted by title.
- A reader sees a page below its parent only if they may read the parent;
  otherwise it shows at the top level, so a hidden title never leaks through
  the tree or the breadcrumbs.
- Moving needs edit rights on the moved page only. It changes neither content
  nor version nor revisions. A move into the page itself or its subtree, or to
  a missing page or sibling, is refused (`ErrPlace`); a `CHECK` also forbids a
  page being its own parent.
- Deleting a page hides it as before; its subpages move up to its parent.
- Ordering works without JavaScript on a "Verschieben" page that lists every
  valid place. The list adds drag and drop (`seiten-baum.js`, pointer events;
  press and hold on touch) that submits the same form. Server validation is
  the only authority.

## Why
Pages grow into groups (house, network, trips) that a flat list cannot show
(stated). Drag and drop was asked for (stated); the form keeps it usable
without JS and with the keyboard.
