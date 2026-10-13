# Development environment

| Service | Start | Address |
|---|---|---|
| Dev data mart | `docker compose up -d` | `localhost:55433`, see [mart/README.md](mart/README.md) |
| App database | `docker compose up -d` | `localhost:55434`, db/user `hareno`, password `dev` |
| Result cache (Valkey 8.1) | `docker compose up -d` | `localhost:55435`, no auth, not persisted |
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

Reset: `docker compose -p hareno-kiban -f dev/kiban/docker-compose.yml down -v`, then `dev/kiban/up.sh`.

`docker-compose.yml` and `.env.example` are copied unmodified from Kiban v0.1.2 `deploy/quickstart/` (Apache-2.0; see `LICENSE-kiban` and `NOTICE-kiban`). To upgrade, copy them from the new tag; a new `.env.example` key needs `dev/kiban/.env` regenerated.
