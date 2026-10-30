# Development data mart

A small PostgreSQL star schema with a deterministic seed, used as the test oracle for Hareno's query compilation (ARCHITECTURE-PLAN.md §14, §18.1). Every expected result was worked out by hand; `known-answers.sql` holds reference SQL that must reproduce them.

```sh
dev/mart/check.sh        # start the mart and run all known-answer checks
docker compose down      # stop; the seed is recreated on next start
```

Connection: `localhost:55433`, database `mart`, schema `mart`.
- `hareno_reader` / `hareno_reader`: the read-only credential Hareno uses.
- `postgres` / `dev`: admin.
- `hareno_gate` / `hareno_gate`: the SQL editor's login. Per query it sets the viewer's scope and switches to `hareno_sql`, which row-level security restricts to that scope ([rls.sql](rls.sql), applied at startup after `init.sql`).

Both are dev-only passwords.

## ESG report demo data

[`esg.sql`](esg.sql) (run at startup after `rls.sql`) adds schema `esg`: invented sustainability data for a science park of four buildings over three financial years (FY2023/24, the base year, to FY2025/26), generated deterministically. It covers utilities and the emissions they cause, waste, greenhouse gases by scope and source, staff, leadership, safety, tenants, community events, procurement, governance measures, targets and a GRI content index. The datasets `datasets/esg_*.yaml` model it; the ESG report demo dashboard is built on them. No figure in it describes a real organisation. It is separate from the sales mart, so the known-answer checks are untouched.

## Loader

[`loader/`](loader/main.go) stands in for a client's load process, for testing refresh against data that changes:

```sh
go run ./dev/mart/loader -every 100ms    # 5 orders per load; Ctrl-C prints the load count
go run ./dev/mart/loader -every 5s -cycle 6   # the public demo: one line added every 5 s for 30 s, then removed, each minute
go run ./dev/mart/loader -clean          # remove loaded orders and restore the seed watermark
```

Each load inserts orders (ids from 1,000,000) with their lines and moves `etl_watermark` in the same transaction. The known-answer checks fail while loaded orders exist, so run `-clean` first. `docker compose down` also resets the mart. The S4 harness starts and cleans the loader itself.

## Model

`fact_order_line` (line grain) → `fact_order` (order grain) → `dim_customer` → `dim_region`; lines → `dim_product`; `dim_date` holds the fiscal calendar; `etl_watermark` holds load times.

Test conventions, not client facts:
- Business time zone: `Asia/Hong_Kong`.
- Fiscal year starts 1 April and is labelled by the year it ends in.

## Deliberate traps

| Trap | Where | Checks |
|---|---|---|
| Fan-out: order-grain shipping joined to lines | `fact_order.shipping_cost` | Q04, Q05 |
| Null measure / null dimension group | shipping on order 106, Cobalt's segment | Q06, Q07 |
| Distinct counts are not additive | customers across categories | Q09 |
| Ratio totals recompute, not average | margin %, average order value | Q10, Q16 |
| Null discount means none | `fact_order_line.discount` | Q11 |
| Time zone moves an order across month and fiscal year | order 105 | Q12, Q13 |
| No prior period gives NULL, not 0 | January month-over-month | Q14 |
| Matrix subtotals and grand total | region × category | Q15 |
| Empty group / division by zero | Ember has no orders | Q16 |
| Cancelled orders excluded | order 107 | Q02 |
| Row policy | North-only viewer | Q17 |
| Read-only credential | `hareno_reader` | Q19 |

Shipping by product category has no correct answer without an allocation rule; Hareno's model must reject that query.
