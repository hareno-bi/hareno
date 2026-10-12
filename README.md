# Hareno

Client-owned, web-based BI platform. Planning and decisions: [ARCHITECTURE-PLAN.md](ARCHITECTURE-PLAN.md), [PROJECT-PLAN.md](PROJECT-PLAN.md).

| Path | What |
|---|---|
| `cmd/api` | Go HTTP API (`go run ./cmd/api`, serves `/healthz` on `:8080`) |
| `cmd/worker` | Go background worker |
| `compiler/` | Malloy compile sidecar: stateless, no database access (`npm start`, `npm test`) |
| `web/` | React + TypeScript frontend (`npm run dev`) |
| `dev/` | Local development stack: data mart, app database, Kiban. See [dev/README.md](dev/README.md) |

Requires Go 1.25+, Node 24+ (runs the sidecar's TypeScript directly), Docker with Compose.
