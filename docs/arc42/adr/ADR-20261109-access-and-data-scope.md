# ADR-20261109: Access per module, data scope per module

Status: Accepted

## Decision
- Per account and module: access yes/no. Plus the account flag **admin**
  (manages accounts, access, links, start page).
- How data is shared is fixed per module in code, not configurable:

| Scope | Meaning | Modules |
| --- | --- | --- |
| private | only the owner sees and edits | Velo, Finanzen |
| private, readable | owner edits; others with access read | Arbeitswege, Orte |
| shared | everyone with access reads and edits | Seiten |
| versus | players of a game see their own view | Schiffe versenken |
| public | without login | start page (public part) |

- Every owned row carries `owner_id`. Repositories take the acting account and
  filter in SQL; handlers never filter.

## Why
Matches how the family uses each module (stated). Fixed scopes keep the access
model small and testable.
