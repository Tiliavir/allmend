# Allmend

The family workbench: start page with links to the home network's services,
plus modules for cycling (Velo), finances, commutes, places, shared expenses,
shared pages and a game of battleships. Self-hosted, one small container, SQLite.

[Demo](https://tiliavir.github.io/allmend/) with made-up data.

## Run

```sh
docker run -d -p 8080:8080 -v allmend:/app/data \
  -e ALLMEND_ADMIN_EMAIL=you@example.org \
  -e ALLMEND_BASE_URL=https://allmend.example.org \
  -e ALLMEND_GOOGLE_CLIENT_ID=… -e ALLMEND_GOOGLE_CLIENT_SECRET=… \
  ghcr.io/tiliavir/allmend
```

Configuration, Google login setup and upgrades: [deployment](docs/arc42/07-deployment.md).

## Develop

Go 1.26, `make check` (lint, tests, W3C validation; needs Java for the
validator). Architecture and decisions: [docs/arc42](docs/arc42/).
