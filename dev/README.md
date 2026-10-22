# Development environment

| Service | Start | Address |
|---|---|---|
| Dev data mart | `docker compose up -d` | `localhost:55433`, see [mart/README.md](mart/README.md) |
| App database | `docker compose up -d` | `localhost:55434`, db/user `hareno`, password `dev` |
| Result cache (Valkey 8.1) | `docker compose up -d` | `localhost:55435`, no auth, not persisted |
| Public demo (nginx): `web/dist` with the API, and Kiban's login pages | see [Public demo](#public-demo) | `0.0.0.0:55450` and `0.0.0.0:55451`, **public** |
| Embedding test host: a stand-in for a client's site | see [Embedding](#embedding) | `0.0.0.0:55452`, **public** |
| Kiban v0.1.2 | `dev/kiban/up.sh` | gateway `http://localhost:55440`, Keycloak `localhost:55441` |

`dev/mart/check.sh` runs the mart's known-answer checks.

## Kiban

`dev/kiban/up.sh` runs Kiban as its own compose project (`hareno-kiban`) so the rest of the stack works without it.

1. On the first run it generates `dev/kiban/.env` and `dev/kiban/secrets-in/superadmin-password`. Both are gitignored.
2. It starts the stack.
3. It runs `seed.sh`. Seeding happens once; the verification runs every time.

Seeded, for multi-company development (password for every dev user: `hareno-dev-password`):

| Company | Org units | Members | Position | Group |
|---|---|---|---|---|
| ALPHA Alpha Trading Ltd | NORTH, SOUTH, WEST (match the mart regions) | alice, bob, dave | NORTH_MGR held by alice | FINANCE: bob, dave |
| BETA Beta Logistics Ltd | HQ | carol, dave | FIN_LEAD held by carol | |

- dave belongs to both companies.
- The app `hareno` is registered and enabled.
- Its service client `hareno-backend` has its secret in `dev/kiban/hareno-backend-secret` (gitignored).
- The superadmin password is in `dev/kiban/secrets-in/superadmin-password`.

Positions sit at the company root: Kiban 0.1.2 cannot place them on org units (ARCHITECTURE-PLAN.md §10.4 item 11).

Browser login: `http://localhost:55440`.

Hareno's own grants are re-applied on every `seed.sh` run (grants are idempotent):
- The app's object types are `report`, `connection` (`sql_user` may use the SQL editor; `dataset_user` may build dashboards from datasets, without SQL; `data_sharer` may share dashboards showing their own data; `exporter` may download CSV) and `data_scope` (`viewer` holds a row scope such as `mart.region.North`, or `mart.region.*` for all).
- On ALPHA's `mart` connection, the North manager (alice) may use SQL with North only, may share her data and may download CSV. The Finance group (bob, dave) may use SQL with all regions, but not download. erin is in ALPHA with no data access of her own; she may download CSV, which gets her no rows. frank sees South only and builds dashboards from datasets, without SQL. BETA (carol) has no access.

## Hareno API

```sh
(cd compiler && npm ci && PORT=55436 node server.ts &)   # Malloy compile sidecar, for dataset tiles
go run ./cmd/api -addr 127.0.0.1:55453 -config dev/hareno.yaml
curl http://127.0.0.1:55453/api/sql -H "Authorization: Bearer $TOKEN" \
  -d '{"connection": "mart", "sql": "SELECT region_name FROM mart.dim_region"}'
```

`GET /api/connections` lists the connections the user may build dashboards on, and those they may query with SQL. `GET /api/datasets` lists the datasets they may build from (`dev/mart/datasets`, listed under `datasets:` in `dev/hareno.yaml`).

The sign-in page of the dev and demo stacks lists the test accounts and their access (`signin.demo_accounts`), and all the sample data (`signin.demo_data`, served at `GET /api/demo-data` without sign-in). Neither belongs in a production configuration.

Saved reports are dashboards: a title and tiles, each its own SQL query shown as a chart or a table. They are under `/api/reports` (`internal/sqlapi/reports.go` lists the routes). Their rules:
- **Changing a report** (save, publish) needs the report's `editor` relation in Kiban, plus `sql_user` or `dataset_user` on its connection. Adding or changing SQL needs `sql_user`.
- **Dataset tiles** are built by picking a dataset's fields and measures; Hareno writes the query each time the tile runs, and the dataset computes its filters and matrix totals (`internal/sqlapi/datasets.go`; ARCHITECTURE-PLAN.md §10.4 item 21).
- **Viewing the published revision** needs only `viewer` (or `editor`), and runs under the viewer's own row scope.
- **Sharing a viewer the sharer's data** needs `data_sharer` on the connection. The viewer then sees the sharer's rows (`internal/sqlapi/shares.go`; ARCHITECTURE-PLAN.md §10.4 item 17).
- **Downloading a tile as CSV** (`GET /api/reports/{id}/tiles/{n}/csv`) needs `exporter` on the connection, asked fresh from Kiban each time. The file holds the downloader's own rows, up to `export_rows` (`internal/sqlapi/export.go`; ARCHITECTURE-PLAN.md §10.4 item 18).
- **Filters** narrow every tile whose result has the filtered column; clicking a chart filters by what was clicked. Editors pick the filter bar's columns (`internal/sqlapi/filters.go`; ARCHITECTURE-PLAN.md §10.4 item 19).
- **Matrix tiles** group a tile's rows by up to three row columns and one column column, with subtotals and a grand total computed by the database under the viewer's row policy (`internal/sqlapi/matrix.go`; ARCHITECTURE-PLAN.md §10.4 item 20).
- **The creator** becomes the report's editor.

Published dashboards refresh every 5 seconds while the tab is visible. The API caches each tile's result per connection, row scope and SQL (`internal/sqlapi/cache.go`). A cached result is reused until the connection's `watermark` query (here, `mart.etl_watermark`) returns a new value, and that query runs at most once per `refresh` interval. To watch a dashboard update, run the loader (`dev/mart/README.md`). The public demo keeps one running: `go run ./dev/mart/loader -every 5s -cycle 6`, which adds one order line every 5 seconds for 30 seconds, then removes them one by one, so the mart moves every 5 seconds without growing. Stop it and run `-clean` before the known-answer checks or the integration tests on this machine.

The API keeps reports in the app database and applies its migrations at startup (`internal/appdb`). The first time it sees a configured connection, it records the connection's company and mode. If the configuration later gives a different company or mode, the API refuses to start, because both are fixed for a connection's life.

The endpoint needs the dev mart and dev Kiban. `$TOKEN` is a Kiban user's access token; `seed.sh`'s `login` function shows how to get one. Every statement is written to the audit log on stdout.

Reset: `docker compose -p hareno-kiban -f dev/kiban/docker-compose.yml down -v`, then `dev/kiban/up.sh`.

`docker-compose.yml` and `.env.example` are copied unmodified from Kiban v0.1.2 `deploy/quickstart/` (Apache-2.0; see `LICENSE-kiban` and `NOTICE-kiban`). To upgrade, copy them from the new tag; a new `.env.example` key needs `dev/kiban/.env` regenerated.

## Browser sign-in and the SQL editor

The browser signs in through Hareno's API (`internal/signin`): `/auth/login` sends it to Kiban's login page, and `/auth/callback` exchanges the code on the server and sets an httpOnly session cookie. The browser never holds a Kiban token. Sessions live in the API's memory, so restarting the API signs everyone out.

Local development, with the API on 55453:

```sh
cd web && npm run dev      # Vite proxies /api and /auth to 127.0.0.1:55453
```

Then open the SQL editor and sign in as alice, bob or carol.

## Public demo

For testing from another computer. Replace the address with the server's public one:

```sh
export HARENO_PUBLIC_HOST=139.99.122.192
HARENO_PUBLIC_ORIGIN=http://$HARENO_PUBLIC_HOST:55450 dev/kiban/up.sh   # registers the origin with Kiban
(cd web && npm run build)
go run ./cmd/api -addr 127.0.0.1:55453 -config dev/hareno.yaml &
docker compose --profile demo up -d demo
```

- `http://$HARENO_PUBLIC_HOST:55450` serves the frontend, with `/api` and `/auth` passed to the API.
- `:55451` serves Kiban's login pages only, not Kiban's API or admin console (`dev/demo/nginx.conf.template`).
- Kiban issues tokens for `http://localhost:55440`, so the proxy forwards with that host name and rewrites the replies.
- Keycloak marks its cookies Secure, which browsers drop on plain HTTP, so the proxy removes the flag.
- **Dev only.** There is no TLS, so passwords cross the network unencrypted. Use only the seeded dev accounts.

## Embedding

A published dashboard can be shown inside another site, such as a client's report portal: the site puts `https://<hareno>/#/embed/<dashboard id>` in an `<iframe>`.

- **Options** in the address, after `?`: `theme=light` or `dark`, `style=<style id>`, `title=0` to hide the title, `filter.<column>=<value>` to start filtered (for example `filter.region_name=North`), `dates=3m` or `dates=2026-02-01..2026-03-31` to start with a date range (`7d`, `30d`, `3m`, `12m`, `year`, `lastyear`), `page=2` to start on the second page. Row security applies as anywhere: a filter never shows rows the viewer may not see.
- **Sign-in.** Viewers sign in to Hareno as usual. Signed out, the frame first tries a silent sign-in (`/auth/login?silent=1`): someone already signed in to Kiban, or to Windows where Kiban uses Active Directory, sees the dashboard at once. Otherwise it shows a Sign in button, which opens Kiban's sign-in in a small window that closes itself.
- **Which sites may frame Hareno** is set where Hareno's pages are served, as `Content-Security-Policy: frame-ancestors 'self' <origins>`; browsers refuse every other site. Here `HARENO_EMBED_HOSTS` (space-separated origins) sets it, by default the test host.
- **Same site.** Hareno's session cookie is `SameSite=Lax`, so the embedding site and Hareno must share a registrable domain (e.g. `reports.example.com` and `hareno.example.com`); so must Kiban's login pages for the silent sign-in. Browsers block the cookies of a frame from another site. If they do, the frame says so and links to the dashboard in Hareno.
- **Height.** The frame posts `{ type: 'hareno:height', height }` to the host page whenever its content changes size; the host sets the frame's height, checking the message's origin first.
- **Test host:** `http://$HARENO_PUBLIC_HOST:55452/?d=<dashboard id>` (`dev/demo/embed-host/index.html`) shows a dashboard as a client's site would.

