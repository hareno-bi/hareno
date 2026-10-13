# Hareno — project plan

Drafted: 2026-10-12. Companion to [ARCHITECTURE-PLAN.md](ARCHITECTURE-PLAN.md); section references (§) point there.

Status: Phase 0 complete (SeaweedFS deferred to exports). Phase 1 spikes authorized 2026-10-12. S1 done: Malloy confirmed on four conditions, PostgreSQL only (`spikes/s1/FINDINGS.md`). S2 done: Kiban kept; a blocking cross-company grant finding is handled in Hareno, plus a decision cache (`spikes/s2/FINDINGS.md`). S4 done: polling bounded by distinct queries ÷ TTL with a Valkey cache, two-level deduplication and stale-while-revalidate; freshness is checked against source capacity at publish (`spikes/s4/FINDINGS.md`).

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

## Phase 2 — Vertical slice, milestone 1 (≈6–8 weeks)

The §14 workflow, thin but real and production-shaped:

1. Register the PostgreSQL connection with restricted credentials.
2. Define one dataset with relationships and measures (admin-authored model document; minimal UI).
3. Build a report with two linked charts and a matrix (minimal designer).
4. Publish an immutable revision; viewers see the published revision.
5. Five-second refresh with stale marking and preserved state.
6. Row and column policy enforced from Kiban org units/positions.
7. CSV export through the same policy pipeline.

**Exit:** the whole slice passes the known-answer suite; policies hold on view, preview and export paths; deployable to a development Kubernetes namespace.

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
| Whether consolidated cross-company reporting is required | Start of Phase 3 |
| Visual direction / sample chart sign-off | End of Phase 1 |
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
