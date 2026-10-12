# S1 semantic-layer spike: findings

Date: 2026-10-12. Gate: ARCHITECTURE-PLAN.md §18.1. Engines: Malloy 0.0.435 (through Hareno's compile sidecar) and dbt MetricFlow 0.213.0 (CLI, dbt-core 1.12.5). Target: the dev mart (PostgreSQL 18), queries executed as `hareno_reader`. Expected values: the hand-computed known answers in `dev/mart/known-answers.sql`. Raw output: [results.txt](results.txt).

Scope is PostgreSQL only, the sole source for now. Other sources (Databricks first) rerun these cases when their connector is built.

Owner decision 2026-10-12: Malloy confirmed on the four conditions below.

## Recommendation

**Confirm Malloy, with four conditions.** Every condition is work Hareno does in Go around the compiler; none needs a fork, except the precision fix, which is one upstream line:

1. **Hareno's planner rejects grain-unsafe queries before compiling (C06).** Malloy answers "shipping by product category" with a plausible but meaningless number. The planner was always meant to validate model paths (§5.1); this spike makes the rule concrete. An order-grain measure grouped by a dimension below its grain is rejected unless an allocation rule exists.
2. **The generator emits guarded division and null-preserving sums (C17W, C20W).** Both workarounds are proven:
   - `pick a / b when b > 0 else null`
   - `pick sum when non-null count > 0 else null`
3. **Fix the money-precision cast (C21).** On Postgres, Malloy's fan-out-safe sum ends with `CAST(… AS DOUBLE PRECISION)` (`dialect/postgres/postgres.js:286`), although the comment beside it notes that NUMERIC keeps precision.
   - Plan: propose the one-line change upstream (cast to NUMERIC) and carry it as a vendored patch until it is released, guarded by C21.
   - Until then, sums through a fan-out join lose precision once totals exceed about 15–16 significant digits. With two decimal places that starts in the tens of trillions; it is reachable for currencies such as IDR, VND and KRW.
4. **Go renders time buckets in the business time zone.** Malloy returns bucket starts as UTC instants (e.g. HKT January = `2025-12-31T16:00Z`). The values are correct; only the labelling is Hareno's job.

Keep MetricFlow as the documented fallback; nothing here argues for switching. It passed the same number of cases, but only after changes to the upstream data model. It also carries constraints that conflict with Hareno's design: a dynamic per-viewer row policy, one-query matrices, a client-prepared mart with no write access, and refresh latency.

## Scorecard

| Case | What it tests | Malloy | MetricFlow |
|---|---|---|---|
| C01–C03 | totals, counts, group by | pass | pass (C03 after denormalising region) |
| C04 | fan-out trap (order shipping with line revenue) | pass (symmetric aggregates) | pass (per-measure subqueries) |
| C05 | order measure by a dimension on the one side | pass | pass |
| C06 | order measure by a line dimension, must be **rejected** | **answers anyway** (63.50 over 42.75 of shipping) | **rejects** ("fan-out join support is pending") |
| C07–C11 | null measures, null groups, distinct counts, ratio totals, null discounts | pass | pass (C09 after a line-level distinct measure) |
| C12–C13 | time zone and fiscal calendar | pass | pass |
| C14 | month-over-month, first month NULL | pass | pass with `--end-time`; otherwise an extra trailing period |
| C15 | running total | pass | pass after `period_agg: last`; default shows start-of-period values |
| C16 | matrix with row, column and grand totals | pass in **one query** (`all()`) | pass with **four queries** (no rollup) |
| C17 | ratio by customer incl. a customer without orders | **division by zero** error; C17W workaround passes | pass after a line-level order count; the customer without orders **cannot appear** |
| C18–C19 | row policy (North only) | pass (a `where` refinement on the source) | pass, but a single filter cannot span two semantic models: query split per model |
| C20 | sum over only nulls is NULL | **returns 0**; C20W workaround passes | pass |
| C21 | precision of a large money sum through fan-out | **fails**: `1.2345678901234568e+15` | not testable here; emits plain `SUM` over NUMERIC |

Out of the box, before modelling or query workarounds: Malloy 17/21, MetricFlow 12/21.

## Malloy observations

- The model lives in Hareno. Nothing changed in the mart; the model refers to tables as they are.
- Symmetric aggregates use an MD5-keyed `SUM(DISTINCT …)`; values are rounded to 9 decimal places inside.
- The query time zone also applies inside joins, so the fiscal-calendar join by business date was right (C13).
- Ad-hoc SQL sources work through `compiler_needs.sql_schemas` (C21).
- Results are wrapped as `row_to_json`; Go parses JSON rows and must keep numbers as exact decimals.
- Compile cost through the sidecar was about 41 ms per query, including schema round trips. That is small next to the 5-second refresh, and schemas are cacheable.

## MetricFlow observations

Modelling changes were needed. In practice these belong in the client's dbt project, which is out of Hareno's scope, or in views Hareno would have to be allowed to create:

- Every measure needs a time dimension in its own semantic model, so `ordered_at` was copied onto lines.
- Measure expressions cannot reach joined columns, so `unit_cost` and `customer_id` were copied onto lines.
- Joins stop at two hops from the measure, so `region_name` was copied onto customers.
- A ratio of measures from different models gets no shared dimensions, so a line-level order count was added.

Defects and traps:

- **Reserved-word entity names produce invalid SQL.** An entity named `order` emits unquoted `order = 106`.
- **Filter syntax differs from group-by syntax.** `--where` accepts only `entity__dimension` plus an `entity_path` argument; `--group-by` accepts full paths.
- **Cumulative metrics default to start-of-period values** at coarser grains.
- **Offset metrics extend one period past the data.**
- **Totals need one query per level.**
- **Members without facts never appear**, because queries start from measures.

Strengths:

- It rejects fan-out-ambiguous queries natively.
- It keeps exact NUMERIC.
- SQL null semantics hold without workarounds.
- Governance is multi-vendor.
- It aligns with the client's existing dbt.

Latency: the CLI took about 4.6 s per query, almost all Python start-up. Production would use MetricFlow's long-running sidecar, which was not measured.

## Open items carried forward

- Further sources: rerun C01–C21 through the source's Malloy dialect when its connector is built (Databricks: Milestone 3). That includes checking how its symmetric aggregate handles precision.
- Upstream Malloy issue/PR for the NUMERIC cast; decide on the vendored-patch mechanism.
- The planner's grain-validation rule (condition 1) belongs in the semantic-model specification (§16 item 4).

## Running the spike

```sh
docker compose up -d mart                 # from the repo root
cd spikes/s1 && uv sync
(cd metricflow && DBT_PROFILES_DIR=$PWD uv run dbt run && DBT_PROFILES_DIR=$PWD uv run dbt parse)
uv run python run.py                      # both engines; add -k C04 --sql to see SQL
```

The MetricFlow views are built in schema `mart_mf` with the dev mart admin login. `hareno_reader` gets SELECT on them.
