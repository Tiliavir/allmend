# ADR-20261109: Configuration via environment, dependency set

Status: Accepted

## Decision
- Configuration only via environment variables (`ALLMEND_*`): listen address,
  data dir, public base URL, Google client ID/secret, first admin e-mail,
  optional break-glass hash, Alpha Vantage key, Frankfurter URL. Everything
  else (accounts, access, links) lives in the database. `config.yaml` and
  `gopkg.in/yaml.v3` (archived upstream) are removed.
- Runtime dependencies: `modernc.org/sqlite`, `golang.org/x/crypto` (argon2id),
  `github.com/yuin/goldmark`. Test only: `github.com/tdewolff/parse/v2`
  (asset validation), `github.com/go-rod/rod` (browser tests).
- Every new dependency needs a new ADR.

## Why
Few, stable dependencies with large benefit (stated principle).
