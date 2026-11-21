# ADR-20261121: Subpages never readable by more than their parent

Status: Accepted

Extends [Pages (mini wiki)](ADR-20261109-pages.md) and changes
[Page tree](ADR-20261109-page-tree.md).

## Context
With rights per page (#41: also when creating), a subpage could be readable
by people who may not read its parent. The tree then has to show it out of
place (top level) to keep the parent's title hidden, so people see different
trees for the same pages.

## Decision
- **Rule:** whoever may read a subpage may read its parent (stated). Only
  read rights are bound; who may edit is free within the readers (stated).
  The owner always counts as a reader of their page.
- **Create:** a subpage starts as wide as the parent allows (everyone the
  parent's readers are, may read and edit); the form offers only the
  parent's readers and no "everyone". A wider request is refused
  (`ErrWider`, form shown again).
- **Narrowing a page** narrows its subpages to it, down the whole branch, in
  the same transaction; the confirmation names them (titles only if the
  viewer may read them) (stated). "Everyone" becomes the parent's readers;
  edit rights stay within them.
- **Moving** below a page with fewer readers narrows the moved branch the
  same way (stated). Since that changes rights, it needs the right to manage
  the moved page (owner or admin); anyone who may edit can still move pages
  whose rights already fit (`ErrManage`).
- **Owners are never locked out:** a narrowing or move after which an owner
  could no longer read their own page is refused as a whole (`LockedError`);
  move the page or give the owner read rights first (stated).
- **Existing data** (migration 15): subpages readable by more than their
  parent are narrowed the same way, top down; page ids are logged, so
  `migrate --dry-run` shows them. An owner who may not read the parent of
  their page aborts the migration with the page ids (stated).
- Deleting a page hands its subpages to its parent, which already allows at
  least as many readers: nothing to adjust.
- The tree keeps its top-level fallback only as a guard against rows that
  break the rule; it never reveals a hidden title.

## Consequences
- Everyone sees the same tree shape, minus the branches they may not read.
- No database constraint backs the rule (it spans rows in two tables); the
  service checks it inside the write transaction's inputs and the migration
  establishes it. A concurrent rights change can slip through in theory; the
  tree fallback keeps that harmless.
