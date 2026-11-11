# 7 Deployment

One container (`ghcr.io/tiliavir/allmend`) on the Synology NAS behind the
DSM reverse proxy (HTTPS), LAN only. Data in the volume `/app/data`:
`allmend.db` and `backup/`.

## Configuration

| Variable | Required | Meaning |
| --- | --- | --- |
| `ALLMEND_ADMIN_EMAIL` | yes | First admin; created at start if missing, always kept admin. |
| `ALLMEND_BASE_URL` | for Google | `https://host` as reached in the browser. |
| `ALLMEND_GOOGLE_CLIENT_ID`, `ALLMEND_GOOGLE_CLIENT_SECRET` | for Google | OAuth client (below). |
| `ALLMEND_ADMIN_PASSWORD_HASH` | without Google | Break-glass password for the first admin (`allmend hashpw`; the old `ADMIN_PW_HASH` works as is). |
| `ALLMEND_ADDR` | no | Listen address, default `:8080`. |
| `ALLMEND_DATA_DIR` | no | Default `data` (`/app/data` in the image). |
| `ALLMEND_BASE_CURRENCY` | no | Default currency for new amounts, default `EUR`. |
| `ALLMEND_ALPHAVANTAGE_KEY` | for prices | Alpha Vantage key (free tier: 25 calls a day). Without it only exchange rates are loaded. |
| `ALLMEND_FRANKFURTER_URL` | no | Exchange rates, default `https://api.frankfurter.dev/v1`; a self-hosted Frankfurter works too. |
| `ALLMEND_SECURE_COOKIE` | no | `false` only for plain-HTTP development. |

Unknown `ALLMEND_*` variables stop the start.

## Google login

1. Google Cloud console → new project → *OAuth consent screen*: External,
   publishing status *Testing*, add the family's Google accounts as test users.
2. *Credentials* → OAuth client ID → Web application. Authorized redirect URI:
   `<ALLMEND_BASE_URL>/anmelden/google/zurueck`.
3. Set client ID and secret as environment variables.

## Upgrade

1. Copy the data volume (or use a Hyper Backup snapshot).
2. Rehearse: `allmend migrate --dry-run` with the new image against the copy;
   compare the record counts per table.
3. Start the new image. It writes `backup/allmend-<time>-pre-v<N>.db`, then
   migrates in one transaction. On any error it stops without changes.
4. Rollback = old image + the backup file. A binary never opens a newer database.

## First switch from almanac

Up to 0.3.0 the migration steps imported an old `almanach.db` from the data
volume once. 0.4.0 removed that import (#8): switch with 0.3.0 first, then
upgrade. The old file is no longer read and can be archived.
