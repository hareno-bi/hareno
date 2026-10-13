# Phase 1 gate review

Prepared: 2026-10-13, for the owner's review. Gate (PROJECT-PLAN.md, Phase 1): confirm or replace Malloy; confirm Kiban conditions or record fallbacks; recalibrate Phase 2–4 durations. Evidence: `spikes/s1`–`s4/FINDINGS.md`.

## Recommendation

**Pass the gate and start Phase 2, with S3's chart sign-off carried as an open client input.**
- All four spikes answered their questions.
- Three spikes met every pass criterion. S3 met its matrix criteria; its chart criterion is the client's sign-off, which Phase 2 does not need (Phase 2 uses the default style). It must land before Phase 3's visualisation work.

## Spike results

| Spike | Result | Decision asked |
|---|---|---|
| S1 Semantic layer | Malloy passed the golden queries on PostgreSQL. It gave one plausible wrong answer (grain), and has precision, null and time-zone gaps; Hareno closes each around the compiler. MetricFlow passed the same count only after model changes, and conflicts with Hareno's design. | **Confirm Malloy**, PostgreSQL only, with four conditions (below). Other sources rerun the golden set when their connector is built. |
| S2 Kiban | The SDK, `BatchCan`, listing, isolation, reassignment and revocation were all exercised. One blocking leak was found (a cross-company grant subject), plus costs and lifecycle gaps. The Kiban team confirmed every issue: #71 fixed in 0.1.3; #72–#75 confirmed; #76 and #77 planned for 0.2. | **Keep Kiban** with §10.4 conditions 1–15. Hareno handles the leak itself until 0.1.3. |
| S3 Presentation | Matrix: 11,934 rows with database subtotals, 33 rows in the DOM, keyboard treegrid, 60 fps at normal scroll speed. Charts: a themed sample set with six full styles (five designed with Kimi CLI). | **Accept the matrix approach.** Chart sign-off: client. |
| S4 Refresh | Five-second polling is bounded by distinct queries ÷ refresh interval, whatever the viewer count (5,000 viewers: 36.8 q/s). Refreshing on a load watermark cut load 15× (2.5 q/s); a continuously loading mart needs a minimum interval. | **Adopt the refresh design:** Valkey cache, two-level deduplication, stale-while-revalidate, change detection, admin minimum refresh interval (default 5 s), connection cap. |

## Conditions carried into Phase 2

These become Phase 2 backlog items, each with a test.

**Malloy (S1)**
1. The planner rejects grain-unsafe queries (an order-grain measure grouped below its grain) unless an allocation rule exists.
2. The generator emits guarded division and null-preserving sums.
3. Money keeps NUMERIC precision: an upstream one-line fix, vendored as a patch until it is released, guarded by test C21.
4. Go labels time buckets in the business time zone.

**Kiban (S2)**
5. Hareno validates every grant's subject against the object's company (until Kiban 0.1.3 fixes #72).
6. A decision cache: 30 s for allows, 5 s for denials, bypassed for exports, schedules and writes. `BatchCan` wherever more than one object is checked.
7. Every data-returning request passes a decision; a verified token alone is never enough.
8. Object lifecycle: server-generated ids that are never reused; relations removed before the anchor.

**Refresh (S4)**
9. Valkey result cache with the cross-replica lock; stale results marked in the UI.
10. Change detection on the mart's load watermark where one exists, otherwise a timer; an admin minimum interval per connection.

**Presentation (S3)**
11. The style system (tokens for the dashboard, ECharts theme built from the same map) carries into the product. Fonts are self-hosted rather than loaded from Google Fonts.
12. Matrix rows come from Malloy. S1 proved row, column and grand totals in one Malloy query (C16, `all()`); Phase 2 maps that result into matrix rows. **First task of Phase 2**, since S3 used hand-written SQL.

## Recalibrated durations

The spikes were run by an AI agent, not by the planned team. They measure technical risk, not team speed, so team-speed estimates stay unvalidated until Phase 2's first milestone.

| Phase | Was | Now | Why |
|---|---|---|---|
| 2 Vertical slice | 6–8 weeks | **7–9 weeks** | Adds: conditions 1–12 (about 1.5 weeks). Saves: the dev loader, S4 refresh code and the S3 style and matrix code to build on (about 0.5 week). |
| 3 Product depth | 16–24 weeks | **16–24 weeks** | Unchanged until the capability matrix is signed off. The style system shortens the visualisation workstream a little; the report designer is still the largest unknown. |
| 4 Operability | 6–8 weeks | **6–8 weeks** | Unchanged. It depends on Kiban 0.2 (#76, #77) and on Kiban's Kubernetes path. |

Re-estimate again at the end of Phase 2's first milestone, using the team's measured pace.

## Open items

**Client** (each item: what is needed, and by when):
- **Chart sign-off** on the S3 sample set (http://139.99.122.192:55450), plus brand colours and fonts if they want their own style. Needed before Phase 3.
- **Revocation:** accept that removing someone's access takes effect within 30 seconds. Needed for Phase 2 design.
- **Freshness:** accept that data reaches the screen about 6–9 s after the mart loads it, at a 5 s minimum interval. Needed for Phase 2 design.
- **Load watermark:** the mart records when each load finished, in the same transaction as the data. Needed in Phase 2 for change detection; without it, Hareno uses timer-based refresh.
- **Currency and number formats.** Needed in Phase 2.
- **Member directory exposure** (§10.4 item 9). Already open.

**Kiban:**
- **0.1.3** (fixes #71 and #72): rerun `spikes/s2` and the isolation harness.
- **0.2** (#76, #77): rerun before Phase 4.

**Owner:**
- How the Kiban team receives the isolation harness: a zip, or a pushed repository.

## Owner decisions requested

1. Pass the Phase 1 gate (yes / no / conditions).
2. Confirm Malloy (PostgreSQL only) with conditions 1–4.
3. Confirm Kiban with conditions 5–8 and §10.4.
4. Adopt the refresh design (conditions 9–10).
5. Accept the recalibrated durations.
