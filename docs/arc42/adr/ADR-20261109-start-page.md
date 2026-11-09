# ADR-20261109: Start page and links

Status: Accepted

## Decision
- `/` is public. Without login it shows only links the admin marked public;
  after login it adds the other links, the modules the account may use, and
  short teasers (pages, open games).
- Links (name, URL, icon, description, public yes/no, order) are managed by
  admins in the UI and stored in the database, never in the repo.

## Why
Intranet start page for the home network (stated); real host names must not
end up in the repo.
