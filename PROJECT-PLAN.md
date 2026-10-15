# Hareno — project plan

Drafted: 2026-10-12. Companion to [ARCHITECTURE-PLAN.md](ARCHITECTURE-PLAN.md); section references (§) point there.

Status: **Phase 1 gate passed 2026-10-13** ([PHASE1-GATE.md](PHASE1-GATE.md)): Malloy confirmed (PostgreSQL only, four conditions), Kiban kept (§10.4 conditions 1–15), refresh design adopted, matrix approach accepted; S3 chart look signed off by the owner 2026-10-14. **Phase 2 in progress.** Spike findings: `spikes/s1`–`s4/FINDINGS.md`.

Durations are indicative only, assuming a team of about four (two Go backend, one to two frontend, part-time platform). They are recalibrated at the end of Phase 1 and are not commitments.

## Principles

- Burn down the biggest risks first: SQL correctness, authorization, then presentation.
- One end-to-end vertical slice before broad feature work (§14).
- Every phase ends at a gate with written exit criteria; failing a gate changes the plan, not the criteria.
- Known-answer data is the test oracle from day one.
- Licence check every dependency before it enters the build (§1, §13).

## Phase 0 — Foundations (≈2 weeks)

| Item | Output |
|---|---|
| Repository layout | Go module (`api`, `worker` entry points), React/TS frontend, Malloy compile sidecar. Done; `deploy/` arrives with the Helm chart in Phase 4 |
| Development environment | Docker Compose: development data mart, application PostgreSQL, Keycloak + Kiban v0.1.2 seeded with two companies, SeaweedFS (optional until exports). Done except SeaweedFS: `dev/README.md` |
| Development data mart | Deterministic star-schema seed with hand-verified expected results and deliberate traps (§14). Done: `dev/mart/` |
| CI | Build, test, lint, dependency licence report that fails on non-allowlisted licences. Done: `.github/workflows/ci.yml` (also runs the dev mart known answers) |

**Exit:** `docker compose up` gives a working local stack; CI is green; the known-answer document exists for the seed data.

## Phase 1 — Risk spikes (≈3 weeks, run in parallel)

Throwaway-quality code is acceptable; the findings are the deliverable.

| Spike | Question | Pass criteria |
|---|---|---|
| S1 Semantic layer (§18.1) | Can Malloy compile Hareno queries correctly? | About 20 golden queries match expected results: fan-out, totals and subtotals, distinct counts across relationships, row-policy injection, period-over-period, nulls. MetricFlow run on the same set for comparison |
| S2 Kiban integration (§10.4) | Does Kiban hold up as Hareno's authorization? | Go SDK login and `BatchCan` working; check latency measured at representative refresh concurrency; bounded-scan catalog listing; position-based grant follows reassignment; company isolation holds across catalog, queries, cache and exports; disablement-to-access-loss interval measured |
| S3 Presentation | Does ECharts reach the visual bar? Does the matrix approach work? | Client signs off a themed ECharts sample set; TanStack matrix prototype with server subtotals, 10k+ virtualized rows, keyboard navigation |
| S4 Refresh | Does five-second polling stay bounded? | Polling + in-flight deduplication + cache prototype; source query rate measured against viewer count |

S1 early observations from the Phase 0 sidecar test (Malloy 0.0.435, Postgres): the fan-out query returned the known answers (shipping 42.75, revenue 587.75). Symmetric-aggregate sums are cast to `DOUBLE PRECISION` (precision risk for money); a sum over only nulls returns 0 via `COALESCE`, not NULL; results are wrapped with `row_to_json`. S1 must decide how each is handled.

**Gate:** owner review of spike findings. Confirm or replace Malloy; confirm Kiban conditions or record fallbacks; recalibrate Phase 2–4 durations.

## Phase 2 — Vertical slice, milestone 1 (≈7–9 weeks, recalibrated at the Phase 1 gate)

The §14 workflow, thin but real and production-shaped:

1. Register the PostgreSQL connection with restricted credentials.
2. Define one dataset with relationships and measures (admin-authored model document; minimal UI).
3. Build a report with two linked charts and a matrix (minimal designer).
4. Publish an immutable revision; viewers see the published revision.
5. Five-second refresh with stale marking and preserved state.
6. Row and column policy enforced from Kiban org units/positions.
7. CSV export through the same policy pipeline.

**Exit:** the whole slice passes the known-answer suite; policies hold on view, preview and export paths; deployable to a development Kubernetes namespace.

### Phase 2 tasks

In order; each lands as its own PR with its test. "Cond" refers to the conditions in PHASE1-GATE.md. Re-estimate the remaining tasks at checkpoint A, using the team's measured pace.

| # | Task | Covers | Done when |
|---|---|---|---|
| P2-01 | Matrix rows from Malloy: compile a matrix query with `all()` totals through the sidecar, run it on the mart, and shape the result into matrix rows in Go | cond 12 | Known answer Q15 (region × category, subtotals, grand total) matches through Go |
| P2-02 | Go query pipeline: the compile loop with the sidecar (`compiler_needs` answered from the mart's schema), execution with the restricted reader credential, typed results | step 1 | Golden queries C01–C21 from S1 run through Go in CI against the dev mart |
| P2-03 | Model document v0 and planner: an admin-authored dataset (relationships, measures) generating the Malloy source; grain check; guarded division and null-preserving sums; vendored NUMERIC patch; time-zone labels | step 2, cond 1–4 | Golden suite passes, including the C06 rejection and C21 precision |
| **A** | **Checkpoint: query path proven end to end. Re-estimate.** | | |
| P2-04 | Application database: migrations for connections (encrypted credentials), datasets, reports and immutable revisions | steps 1, 4 | Migrations apply and roll forward in CI; publish creates a revision that never changes |
| P2-05 | Kiban module: sign-in, decision cache (30 s allow / 5 s deny, bypass list), `BatchCan`, grant-subject validation, object lifecycle | cond 5–8 | The S2 harness scenarios pass as Go tests against dev Kiban, including the cross-company leak being refused |
| P2-06 | Row and column policy from Kiban org units and positions, compiled as Malloy `where` refinements, or enforced by PostgreSQL row-level security if the client chooses the SQL editor (§5.3); cache scope follows the effective policy | step 6 | North-only viewer sees only North (Q17) on view, preview and export |
| P2-07 | Refresh service: Valkey cache, two-level deduplication, stale-while-revalidate, watermark change detection, minimum interval per connection, stale markers | step 5, cond 9–10 | S4 scenarios rerun against the real service stay within their bounds |
| P2-08 | Report viewer: two linked charts and the matrix, polling with preserved state, the style system with self-hosted fonts | steps 4–5, cond 11 | A viewer sees a published report refresh in place with filters kept. Filters and linked charts done 2026-10-14, the matrix tile 2026-10-15, on SQL dashboards (ARCHITECTURE-PLAN §10.4 items 19–20) |
| **B** | **Checkpoint: a published report refreshes for a policy-restricted viewer.** | | |
| P2-09 | Minimal designer: add, remove and place charts on a grid, pick fields and measures from the dataset, link two charts by a shared filter | step 3 | A report built in the designer publishes and views correctly. Dataset tiles done 2026-10-15 (ARCHITECTURE-PLAN §10.4 item 21): picked fields and measures, mixed with SQL tiles on the grid, filters and linked charts shared |
| P2-10 | CSV export through the same policy pipeline, with a fresh decision (no cache) | step 7 | Export of a restricted viewer's report contains only their rows. Done 2026-10-14: per-tile CSV, `exporter` per data source (ARCHITECTURE-PLAN §10.4 item 18) |
| P2-11 | Development Kubernetes namespace: images, minimal manifests (Helm comes in Phase 4) | exit | The slice runs in the namespace against the dev mart and dev Kiban |
| P2-12 | Exit suite: known answers end to end; policy tests on view, preview and export | exit | All pass in CI and in the namespace |

## Phase 3 — Product depth, milestone 2 (≈16–24 weeks)

Parallel workstreams, each against the capability matrix (§16 item 1), which must be signed off before this phase starts.

| Workstream | Scope |
|---|---|
| Semantic modeling | Model editor UI, calculated dimensions grammar, hierarchies, time intelligence, model revisions and impact assessment |
| Report designer | Grid canvas, pages, property panels, chart configuration, filters, preview, autosave drafts, conflict detection, reflow for small screens |
| Interaction | Shared filters, cross-filtering, drill-down, drill-through, selection preserved across refresh |
| Visualization | Agreed ECharts catalog, shared theme tokens, KPI cards, accessibility alternatives |
| Matrix | Hierarchical rows/columns, subtotals, conditional formatting, ARIA grid |
| Governance | Sharing via Kiban, catalog listing, permission semantics (§9), audit events, administration |
| Reporting | Durable jobs, schedules, PDF rendering worker, XLSX via excelize, artifact storage, retention |

**Exit:** capability matrix acceptance criteria met for launch scope.

## Phase 4 — Operability and hardening (≈6–8 weeks, overlapping Phase 3)

- Helm chart, explicit migrations, health/readiness, resource limits, network policy, secrets.
- Kiban on Kubernetes: adopt its supported path if released, otherwise the budgeted self-operated option (§10.4 item 7).
- Backup and restore drill; upgrade and rollback procedure.
- Monitoring for refresh lag, queue delay, source latency, authorization failures, cache age, artifact delivery.
- Load test against agreed workload targets (§15).
- Security review and accessibility review.
- Handover documentation (§13).

**Exit:** install, upgrade, restore and load tests pass in a client-like environment.

## Phase 5 — Pilot and launch (≈4 weeks)

Client UAT on real mart data with a pilot user group, defect burn-down, launch decision.

## After launch — milestone 3

Databricks connector, broader calculations and chart coverage, scale work, Snowflake as required. Each item is admitted by an evidenced requirement (§4.2).

## Client inputs and when they are needed

| Input | Needed by |
|---|---|
| Query authoring options, including a SQL editor for trusted users (ARCHITECTURE-PLAN §5.3); option C moves row policy into the database | Before P2-06 |
| Whether consolidated cross-company reporting is required | Deferred by the owner (2026-10-14): not in scope until raised again |
| Visual direction / sample chart sign-off | Done: chart look signed off 2026-10-14 |
| Capability matrix and launch depth sign-off | Start of Phase 3 |
| Initial chart catalog | Start of Phase 3 |
| Export formats and paginated-report requirements | Start of Phase 3 |
| Workload, latency, availability, RPO/RTO targets | Start of Phase 4 |
| Kubernetes environment, ingress, storage, registry, air-gap status | Start of Phase 4 |
| Databricks access and authentication | Milestone 3 |

## Top risks

| Risk | Mitigation |
|---|---|
| Scope grows toward full Power BI parity | Signed capability matrix with depth boundaries; changes go through it |
| Malloy fails the spike or upstream stalls | Hareno owns the model format; MetricFlow fallback; Malloy vendored |
| Kiban gaps (listing, Kubernetes, maturity) | Single integration module; bounded-scan listing; budget self-operation |
| Mart performance under five-second refresh | Client responsibility (§1); Hareno measures and reports source latency |
| Matrix and designer effort underestimated | Prototyped in S3; recalibrate after Phase 1 |
| Team capacity | Recalibrate durations at the Phase 1 gate |

## Immediate next steps

1. Owner review of this plan and the revised architecture record.
2. Authorize Phase 0 and the Phase 1 spikes.
