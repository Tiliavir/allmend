# ADR-20261109: Login with Google (OIDC), local fallback

Status: Accepted

## Decision
- Login with Google via OpenID Connect, authorization code flow with PKCE and
  `state`/`nonce`. ID token signature (RS256) verified against Google's JWKS with
  the Go standard library; no OAuth library.
- Accounts are matched by verified e-mail. The first admin's e-mail comes from
  config; admins invite by e-mail and set module access. The invite becomes an
  active account on first Google login with that address.
- Local login as fallback: an admin can enable a password for any account
  (argon2id, `golang.org/x/crypto`). The admin sets a one-time password that
  must be changed at first login. Bootstrap/break-glass: a local password hash
  for the first admin may be set via env.
- Sessions are server-side (SQLite table, random 256-bit ID in an `HttpOnly`,
  `Secure`, `SameSite=Lax` cookie), 30 days sliding. Server-side so disabling an
  account or a password reset ends its sessions at once.

## Why
No passwords to manage for the family, while a Google outage or a changed
Google policy never locks anyone out.
