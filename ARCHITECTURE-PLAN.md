# Hareno — requirements and architecture record

Recorded: 2026-10-12. Revised 2026-10-12 with component evaluation decisions (section 18).

Status: Phase 0 (foundations) authorized by the project owner on 2026-10-12; see PROJECT-PLAN.md. Later phases require separate authorization.

This document preserves the requirements, recommendations, tradeoffs, and unresolved questions discussed so far. **Confirmed** means explicitly stated by the client-facing project owner. **Decided** means selected by the project owner after evaluation, subject to the named validation gate. **Proposed** means recommended during planning and recorded for review; it does not imply a final approved technology selection. Roadmap items are not delivered capabilities or promised dates.

## 1. Purpose and confirmed requirements

Confirmed product name: **Hareno**, pronounced “hah-reh-noh”. This is a coined brand name inspired by Japanese *hare* (“clear weather”), suggesting a clear outlook; it has no fixed dictionary meaning. Use **Hareno** for product branding and lowercase **hareno** for code identifiers and domain names. No domain has been purchased; registrar availability and pricing remain unconfirmed.

Build a client-owned, on-premises-hostable, web-based BI platform. The client's existing tool is Power BI, which provides the functional baseline for discussion.

- Web application only; no desktop application.
- No PBIX import or compatibility with any other BI tool. DAX compatibility is not required; calculation capabilities will be defined independently.
- All core BI areas matter: semantic modeling, report authoring, visualization, interactive analysis, reporting, governance, and operations. Delivery must not quietly shrink to a collection of developer-authored dashboards.
- Attractive, polished chart presentation is important.
- Dashboards should refresh every five seconds.
- Start with PostgreSQL, assumed to be an existing data mart. Databricks is an expected future source. Snowflake, other SQL databases, and column stores were also identified as future source categories.
- The delivery team is responsible for application maintenance.
- The client requires ownership of bespoke application source code. Third-party code retains its own ownership and license terms.
- Kubernetes is the intended deployment platform.
- Python, Go, and Rust were offered as backend candidates; choose for low maintenance and adequate performance.

- Licensing must be "really free": permissive open-source dependencies only, with no paid tier, source-available, BSL or ELv2 component required for any delivered capability.
- dbt and Databricks exist upstream in the client's data workflow. They are outside this project's scope.
- Data-mart refresh frequency, mart query performance (read replicas, rollups, indexing) and source-side freshness are the client's responsibility. This project provides its own development data mart for testing.

Five-second **refresh cadence** is confirmed. A maximum five-second **source-to-screen freshness guarantee** is not confirmed and must not be inferred.

## 2. Proposed architecture baseline

| Area | Proposed decision | Status / qualification |
|---|---|---|
| Frontend | React and TypeScript | Proposed |
| Primary backend | Go | Proposed; one primary backend language |
| Backend structure | Modular application, separately runnable API and worker processes | Proposed; avoid premature microservices |
| Analytical execution | Governed SQL against the PostgreSQL data mart, executed by Go (pgx; Databricks SQL Go driver later) | Proposed; no initial ingestion or analytical storage engine |
| SQL compilation | Malloy compiler (MIT) in a stateless Node sidecar; Go generates Malloy source from the Hareno model | **Decided**, subject to the semantic-layer spike (§18.1); fallback dbt MetricFlow ≥ v0.209 |
| Application storage | Separate PostgreSQL database | Proposed; isolate application work from analytical source workloads |
| Semantic model | Versioned governed datasets, dimensions, measures, relationships, hierarchies, policies, stored in Hareno's own format | Proposed; the compiler is not the system of record |
| Report storage | Relational metadata plus validated versioned JSON documents | Proposed |
| Visualization | ECharts only at launch, shared visual styling | **Decided**; Nivo deferred until a specific chart needs it |
| Table / matrix | TanStack Table v9 + TanStack Virtual (MIT); server-computed totals | **Decided**; fallback Tabulator (MIT) |
| Refresh | Five-second polling with bounded execution and no overlapping equivalent queries | Proposed |
| Shared cache | Valkey or equivalent shared result cache | Proposed; exact dependency/version not selected |
| Identity / object authorization | Kiban (Keycloak identity, Kiban's own authorization engine), multi-company from the start | **Decided** to adopt, conditional on §10.4 and §18.3 |
| Row / column security | BI policy and query layer | Proposed responsibility boundary |
| Jobs | PostgreSQL-backed durable queue (River, MPL-2.0, unmodified), Go workers | **Decided** for now; replaceable (§18.6) |
| PDF rendering | Controlled headless-browser worker for dashboard exports | Proposed; pagination needs separate layout behavior |
| XLSX export | Server-side generation in Go with excelize (BSD-3-Clause) | Proposed |
| Artifacts | Local disk for prototype; S3 API for later environments | **Decided**; SeaweedFS (Apache-2.0) for development S3; MinIO rejected |
| Deployment | Kubernetes with a supported Helm installation path | Intended target; Kiban support remains a dependency |

No Superset, Lightdash or Cube adoption is proposed. Superset was initially considered for faster standard BI delivery, but the custom authoring experience, presentation, ownership, and maintenance requirements favor a custom application using established components. Lightdash and Cube were evaluated and rejected; see §18.

## 3. Backend and component boundaries

### 3.1 Language choice

Go is recommended for the API, query orchestration, authorization integration, scheduling, and workers. Most analytical computation belongs in the source database; backend language speed cannot repair slow SQL or incorrect aggregation semantics.

Python remains a possible future choice for a specific analytics capability that demonstrably needs its ecosystem. Rust is reserved for an evidenced CPU-intensive requirement. Neither is part of the initial baseline. Maintaining three backend languages would create unnecessary deployment, testing, and staffing overhead.

One bounded exception: the Malloy compiler is TypeScript and runs as a stateless Node sidecar. It holds no state, executes no queries and has no database credentials. It receives Malloy source plus cached schemas and returns SQL. Go remains the only application backend. The team already maintains TypeScript for the frontend.

Databricks provides an official Go SQL driver, so Go does not block that future connector.

### 3.2 Logical modules

- Connections: credentials references, discovery, capabilities, execution and cancellation.
- Semantic model: datasets, relationships, dimensions, measures and revisions.
- Query planner: validation, policy application, SQL compilation, budgets and result shaping.
- Reports: layouts, visuals, interactions, drafts, publishing and revisions.
- Authorization integration: object decisions and effective data-policy resolution.
- Scheduling and exports: durable jobs, execution identity, rendering and artifact delivery.
- Administration and audit: configuration, operational diagnostics and BI activity events.

Use one backend codebase initially. API and worker processes can scale independently without making each module a separately operated service. Browser rendering may require its own runtime/container; that does not justify another general-purpose backend implementation.

## 4. Data sources and execution

### 4.1 PostgreSQL first

- Treat the PostgreSQL source as a prepared data mart, not an operational OLTP database requiring a new ingestion platform.
- Use dedicated, restricted read credentials and approved schemas/views.
- Discover tables, views, columns and data types.
- Push filtering, aggregation, sorting and limits into the database.
- Enforce deadlines, cancellation, result-size limits, bounded pools and per-source concurrency.
- Prioritize interactive queries over bulk exports; define fair scheduling and starvation protection.
- Parameterize values and validate identifiers against the governed model.
- Keep application metadata, jobs and audit activity separate from customer analytical data.

Source privileges must enforce read restrictions; a superficial SQL-prefix check is not a sufficient trust boundary.

### 4.2 Future connectors

Isolate connection/authentication, schema discovery, SQL dialect, type mapping, capabilities, execution, pagination and cancellation. Databricks is the next concrete target; verify warehouse authentication, latency, cancellation and cost behavior when it is introduced.

Each dataset initially belongs to one connection. A report may eventually display independent charts from multiple datasets, but cross-source joins and federation are separate features. No Trino, replication pipeline, ClickHouse, or streaming platform is included without an evidenced requirement.

## 5. Semantic model

### 5.1 Definitions

| Entity | Meaning | Example |
|---|---|---|
| Dataset | Governed analytical subject with declared grain | One row per order line |
| Dimension | Field for grouping or filtering | Region, category |
| Measure | Reusable aggregation or derived metric | Revenue, distinct customers |
| Relationship | Approved join with declared cardinality | Order line to product |
| Hierarchy | Ordered drill path | Year, quarter, month |
| Data policy | Rules restricting rows and columns | Allowed regions; salary restriction |

Prefer curated mart views and straightforward star-shaped datasets: a fact table with many-to-one dimension relationships. Explicitly reject ambiguous joins, unsupported cardinalities and paths that can duplicate measures. Many-to-many and multi-fact calculations require a deliberate later specification, not silent SQL generation.

### 5.2 Calculation semantics

Initial proposed capabilities:

- Sum, count, distinct count, average, minimum and maximum.
- Typed calculated dimensions with a restricted expression language.
- Measures composed from other measures.
- Explicit null, decimal, time-zone, calendar and division-by-zero behavior.
- Totals evaluated at the total's grouping level.
- Running totals, rank, moving averages and period comparisons as defined calculation categories with ordering and filter rules.

Example: `Margin % = SUM(Profit) / SUM(Revenue)`. The grand total recalculates that expression; it does not average visible percentages. Joining order-level measures to order lines can duplicate values and must be handled or rejected by the model.

The planner accepts structured field/measure references, validates them, resolves relationships, applies policies, then compiles parameterized SQL. A report must not contain executable user-supplied application code.

Compilation pipeline (decided, subject to §18.1):

1. Go validates the structured query against the published model and the viewer's permitted query shapes.
2. Go resolves effective row/column policy and removes or rejects restricted fields.
3. Go generates Malloy source from the Hareno model, adding row policy as `where` refinements.
4. The Malloy sidecar compiles to dialect SQL without executing it.
5. Go executes the SQL with deadlines, cancellation and size limits, and shapes the result.

Malloy's symmetric aggregates provide fan-out-safe measures across one-to-many joins. Totals come from Malloy ungrouped aggregates or nested queries, or from additional queries per grouping level; they are never computed in the browser. Calculated dimensions use a small Hareno grammar validated in Go and emitted as Malloy expressions. Period comparisons are built on Malloy timeframes and filters.

Trusted dataset administrators may have a separate SQL-backed dataset workflow. Ordinary authors select governed fields and measures. Exact expression syntax, SQL-author privileges, time-intelligence scope and model UI remain to be specified.

## 6. Authoring, storage and lifecycle

### 6.1 Storage

Use relational tables for workspaces, identities/references, ownership, connections, reports, revision pointers, schedules, jobs and audit records. Use validated versioned JSON documents for dataset definitions and report layouts, visual settings, filters and interaction rules.

Reference stable dataset, field and measure IDs. Display-name changes must not break reports. Persist application-level chart configuration rather than unrestricted renderer-specific options.

### 6.2 Draft and publish

1. Authors edit autosaved drafts.
2. Optimistic revision checks prevent overwriting concurrent edits.
3. Publish validates model references, permissions, expressions and visual configuration.
4. Publish creates an immutable report revision.
5. Viewers consume the published revision while authors continue drafting.
6. Rollback selects a prior valid published revision.

Published reports reference known dataset revisions. Dataset changes produce an impact assessment and explicit report-upgrade path. Current security policies always apply; restoring an old report/model revision cannot restore revoked access. Version pinning does not preserve underlying source data, so exact historical data reproduction needs separate retention/snapshot provisions.

### 6.3 Designer

Propose a grid canvas with resizing, alignment, pages, property panels, field selection, chart configuration, filters, preview and publish. Smaller-screen viewing uses stacking/reflow. Start with edit-conflict detection rather than real-time collaborative editing.

Authors should be able to configure shared filters, chart selections, cross-filtering, drill-down and drill-through. These behaviors belong to the application, not the renderer alone. Cross-highlighting depth and interaction configuration need explicit acceptance criteria.

## 7. Visualization decision

### 7.1 Nivo versus ECharts

| Requirement | Nivo | ECharts |
|---|---|---|
| Bar, line, pie, scatter, hierarchy charts | Good coverage | Good coverage |
| Calendar heatmap, chord, bump, waffle, swarm | Dedicated catalog components | Coverage and approach vary |
| Line/column combinations, multiple axes | Custom composition; no dedicated combo component | Combined series and axes |
| Radial gauge | No dedicated gauge; radial bar is different | Dedicated gauge |
| Candlestick/OHLC | No dedicated component | Dedicated candlestick series |
| Waterfall | Custom composition | Documented stacked-bar composition; not a dedicated series |
| Integrated zoom and brush controls | Chart-specific/application work | Reusable components |
| Analytical table/matrix | Separate component | Separate component |

Decided: ECharts only at launch, styled toward the visual direction liked in Nivo. A second renderer doubles theming, event normalization and accessibility work. Add Nivo only when a specific requested chart has clear value that ECharts cannot reasonably provide.

Both can share fonts, palette, spacing, axes, gridlines, number/date formats, tooltips and motion guidance. Exact label placement, interactions and animation will still differ.

### 7.2 Integration rules

- Give each chart type one renderer; do not maintain every chart twice.
- Business users choose chart types, not rendering libraries.
- Normalize selection/drill events into the report interaction model.
- Share chart containers, formatting, loading/error states and theme tokens.
- Preserve selection, filters and zoom through refreshes.
- Support keyboard operation, readable contrast, reduced motion and accessible data alternatives; a library alone does not establish accessibility compliance.
- Build custom KPI cards or small extensions where appropriate; do not recreate complex interaction systems just to avoid an existing component.

Table/matrix decided: TanStack Table v9 with TanStack Virtual (MIT). It is headless; Hareno builds the matrix cells, sticky headers, conditional formatting and an ARIA grid keyboard model. Subtotals and grand totals are computed server-side (§5.2). Exports are generated server-side so they pass through the same policy pipeline. Fallback: Tabulator (MIT) in a thin React wrapper.

Rejected: AG Grid (excluded by project owner; pivoting and grouping are commercial), RevoGrid and MUI X (tree, grouping, pivot or aggregation in paid tiers), Handsontable and WebDataRocks (proprietary), Glide Data Grid (stalled releases; canvas rendering weak for accessibility), react-pivottable (not virtualized, barely maintained).

## 8. Refresh, consistency and caching

### 8.1 Browser and API behavior

- Poll visible dashboards every five seconds; pause hidden tabs.
- Preserve the user's current analytical state.
- Avoid overlapping executions of equivalent queries.
- Cancel obsolete work where possible and discard results from superseded filters.
- Retain the last successful result during transient failures and clearly mark it stale.
- Bound retries, concurrency, response sizes and query duration.
- A refresh may batch several visual queries in one browser request.

For ordinary dashboards, allow independent chart completion with visible timestamps. Provide an explicit consistent-snapshot mode for reports requiring reconciliation. Merely batching HTTP requests does not guarantee a shared database snapshot.

### 8.2 Shared results

Use a shared result cache for multi-replica deployments and coalesce equivalent in-flight work. Cache: Valkey (decided after S4; dev instance in `compose.yaml`).

S4 (2026-10-13) refresh rules (evidence: `spikes/s4/FINDINGS.md`):
- One `MGET` per report refresh.
- In-flight deduplication per replica (singleflight) and across replicas (a Valkey `SET NX` lock).
- Stale-while-revalidate below a hard age ceiling, with stale results marked in the UI.
- Change detection where the mart has a load watermark (below).
- A source-concurrency cap per connection.
- With these, source queries per second are bounded by distinct queries ÷ cache TTL, independent of viewer count: 5,000 viewers ran at 36.8 queries/s against a bound of 37.5. Without a cache, 100 viewers saturated the source.

**Refresh on change, not on a timer, where the mart provides a load watermark.** Hareno reads the watermark (for example `SELECT max(loaded_at)` from a load log) at most once per minimum refresh interval per connection, on a reserved connection, and shares it across replicas through Valkey. Cached results carry the watermark they were computed under; when it moves, charts refresh once (stale-while-revalidate) and stay cached until it moves again. Source load becomes distinct queries ÷ max(load interval, minimum interval). S4 rows 13–17, with a real loader process: loads every 60 s cost 2.5 queries/s instead of 36.8, with new data on screen in 6.5 s p95; a mart loading every 100 ms costs 30 queries/s at a 5 s interval (6.2 s p95), but saturates the source at a 1 s interval. This is the approach Looker (datagroups) and Power BI (automatic page refresh with change detection) take. The watermark is the client's to provide (§2) and must move in the same transaction as the data, or after it; without one, a connection uses timer-based refresh with the TTL above.

Overload control is the admin-set minimum refresh interval per connection (default 5 s; the watermark read interval, or the TTL for timer-based connections) plus the source-concurrency cap. Hareno does not estimate report cost at publish time; add that only if a client's reports outgrow these two controls. Row-policy scopes should be coarse: a scope per user removes result sharing.

Cache identity includes connection, dataset revision, query, filters, time context and effective data-access scope. Different users may share results only when their effective scopes are equivalent. Check current object access before returning a cache hit, and resolve current row/column policy before selecting the entry.

Define cache reuse by result age and the agreed freshness budget. Five-second polling plus a five-second cache lifetime can introduce more than five seconds of visible delay. Policy changes must alter the effective cache scope or invalidate affected results.

Query timestamp and source freshness are distinct. A trustworthy source-data freshness indicator requires an ingestion watermark from the mart. For failed authorization, do not continue issuing protected data from cache; previously downloaded data cannot be remotely recalled.

### 8.3 Load and source limits

Illustrative request count: 100 viewers × 10 charts ÷ 5 seconds = 200 chart requests/second before batching, sharing or deduplication. This is not a measured project workload or a promise of required capacity.

Kiban decisions are cached for a bounded TTL (§10.4 item 13), after S2 measured about 30 ms per single check and a ceiling near 100 checks/s on the dev host.

## 9. Security semantics

| Permission | Intended meaning |
|---|---|
| View report | Consume its published definition |
| Edit report | Modify a draft |
| Publish report | Release a report revision |
| Manage sharing | Change object grants |
| Build from dataset | Create analyses from governed fields |
| Export summarized data | Export permitted aggregate results |
| Export detail | Retrieve permitted underlying records |
| Manage connection | Configure source access |

Viewing a published report does not imply permission to author arbitrary queries over its dataset. Publishing binds approved datasets to the report's permitted query shapes. The backend must validate viewer requests against that definition while applying current data policies.

Restricted columns must be controlled in selection, filtering, sorting, calculations and drill-through, not only hidden visually. Administrative platform access should not automatically imply business-data access. Explicitly define elevated support access, if required.

All paths—interactive views, previews, exports, schedules and artifact downloads—must enforce the same policy semantics. Authorization failures deny protected access. A worker's privileges are not the recipient's privileges.

Proposed browser authentication: server-managed sessions with secure HTTP-only cookies, server-held tokens and CSRF protection. Confirm compatibility with Kiban; do not assume this is already integrated.

## 10. Kiban evaluation and team clarification

### 10.1 Version and evidence

The original evaluation targeted **v0.1.1** (published 2026-10-09). **v0.1.2** was released 2026-10-12 and is the current evaluation baseline.

Evaluation to date consists of documentation, release and source review plus the team's response supplied by the project owner. No runtime verification, security audit or benchmark has occurred.

Architecture correction: Kiban is **not** built on OpenFGA. It implements its own Zanzibar-subset authorization engine stored in Kiban's PostgreSQL. OpenFGA (v1.12.0) is used only in Kiban's test harness as a differential oracle (65 hand-written cases plus 5000 generated checks); Kiban's own limitations document states this is not a proof of equivalence. Keycloak provides identity. Leaving Kiban later therefore requires exporting tuples from Kiban tables and translating its model JSON; the only existing translator is in the test harness.

Kiban's model: one tenant per deployment; companies; a typed org-unit tree per company; members (per company); positions on org units with dated assignments; groups within a company. Grants to `position#holder` follow the job. Kiban rejects members, positions and anchors that cross companies, but v0.1.2 accepts a grant whose subject is another company's position (S2; §10.4 item 12). Apps integrate through a Keycloak service client, a registered manifest of object types and relations, and the Go SDK (`Can`/`BatchCan`, `Grant`/`Revoke`, member/position/group lookups).

### 10.2 Responsibility boundary

Kiban: identity through Keycloak, companies, members, positions/groups, and application object authorization. BI application: query/data restrictions, authorized use of results, report-specific activities and artifacts.

Run the BI application beside Kiban with its own database and deployment, using its app integration rather than an in-tree sample module. Kiban's Apache-2.0 licensing and dependency notices do not transfer upstream ownership to the client.

### 10.3 Team response record

The following reflects the team's supplied response, not independently verified future functionality:

| Topic | Team's clarification |
|---|---|
| Inheritance | Parent tuples and derived relations supported; combined requirements use two checks or a derived relation |
| Denies / conditions | No denies, inheritance exceptions, conditional or expiring grants, by design |
| Data scopes | Object decisions plus member/position/group facts; BI owns row/column policies |
| Decision consistency | Grant or membership writes visible to next check; no decision cache |
| Separate databases | Grant first, application row second, reconciliation sweep; write-up planned for 0.1.5, issue #53 |
| Limited inheritance | Pattern documentation planned for 0.1.5, issue #54 |
| Authorized listing | Planned 0.3, issue #43; interim membership filtering and page batch checks; no counts including invisible objects |
| Offline subject checks | Token exchange with acting-client audit planned 0.2, issue #51; today bearer-only; do not retain user tokens |
| Change feed / audit API | Planned 0.3, issue #47; audits cover mutations and denials, not every allow |
| Benchmarks | None published; load harness planned 0.2, issue #32 |
| Passkey challenge | Planned 0.3, issue #46; use Keycloak OTP policy for production today |
| Kubernetes / Helm | Supported path planned 0.3, issue #45; Compose supported today |
| Enterprise identity | Entra ID / AD brokered by Keycloak but not tested by team; upstream disablement/group changes at next login |
| SCIM | Not on roadmap |
| Redundancy | Gateway stateless and replicable; PostgreSQL / Keycloak HA is operator responsibility |
| License | Apache-2.0 plus dependency notices |

### 10.4 Integration conditions

1. **Scheduled delivery:** settle owner, execution subject and recipient policies. Validate a supported offline mechanism with bounded delegation and current account status before enabling recipient-specific distribution.
2. **Catalog listing:** membership filtering alone is insufficient. Batch checks after fetching candidates can create sparse pages; scan with bounded cursors until full/exhausted and avoid unauthorized metadata/count leakage. Validate scale and future migration to a lookup API.
3. **Identity revocation:** immediate Kiban grant consistency is distinct from upstream disablement at next login. Set and verify a maximum disablement-to-access-loss interval; token refresh is not assumed to reauthenticate upstream.
4. **Combined permissions:** demonstrate conjunction semantics and define report-view versus dataset-build access. Do not assume alternative grant paths implement AND.
5. **Cross-database lifecycle:** authorize creation before grants; use server-generated non-reused IDs, idempotent retries, explicit pending operations, deletion cleanup and reconciliation. Grant-first can leave orphan tuples.
6. **Capacity:** test current decisions under expected refresh concurrency; do not introduce stale authorization caching without a defined revocation model.
7. **Deployment:** either wait for supported Kiban Kubernetes delivery and validate it, or explicitly budget ownership of its Kubernetes operation.

8. **Company modeling (decided: multi-company from the start):** Hareno assumes multiple Kiban companies within one deployment. Every Hareno object (workspace, connection, dataset, report, schedule, job, artifact, cache entry, audit event) belongs to exactly one company, and every request runs in one active company context. Kiban rejects cross-company facts, so consolidated reporting across companies is not representable as a single shared object; if it becomes a requirement, it needs a deliberate design (for example a dedicated group-level company whose datasets read consolidated mart views). A deployment with one company remains a valid special case.
9. **Member directory exposure:** every company member can read the member directory, including email addresses. The client must accept this or it must be addressed before launch.
10. **Hosting:** Kiban must be served at the root of its own hostname.
11. **Positions on org units (found 2026-10-12, v0.1.2):** the admin position-create route rejects `orgUnitId` and places every position at the company root, although the published API contract lists `orgUnitId` as required; the gateway exposes no position update route. Upstream: Kiban #71, fixed in 0.1.3 (create accepts `orgUnitId`; a position update route is added). Until then, Hareno keeps its own mapping from positions or groups to data-policy scopes (for example a region) instead of deriving scope from a position's org unit.
12. **Cross-company grant subjects (found by S2, 2026-10-13, v0.1.2; blocking until handled):** the grant route accepts a position from company B as subject on a company-A object, and the check then allows a member of both companies who holds that position. This contradicts Kiban's own security documentation. Hareno validates every grant subject's company before writing. Upstream: Kiban #72, fix at write and check time in 0.1.3. Hareno keeps its own validation after that.
13. **Decision cache (S2):** a single `Can` costs about 30 ms and saturated near 100 checks/s on the dev host. Hareno caches decisions per (company, subject, feature, object, relation) in process: allows for 30 s, denials for 5 s, with hard expiry and no stale serving. It uses `BatchCan` for multi-object checks. Exports, schedules and write paths bypass the cache. This is the revocation model required by item 6: grant revocation, member deactivation and Keycloak disablement take effect within 30 s on interactive views, subject to client acceptance. Design: `spikes/s2/FINDINGS.md`, *Decision cache*.
14. **Decision on every data path (S2):** Kiban's decision denies a Keycloak-disabled user or a deactivated member on the next check, while the access token keeps verifying for up to 300 s. Every data-returning request therefore needs a fresh or cached decision; token verification alone never authorizes. Kiban's own read routes, such as `me/companies`, still serve a disabled user's token until it expires (Kiban #74: documented in 0.1.3; a subject-state check on every route is planned for 0.2). Decision cost: Kiban #76, with a cached or event-driven Keycloak state, a latency budget and a load harness planned for 0.2. Hareno's decision cache stays regardless. The Go SDK requires Go 1.26.9.
15. **Object lifecycle in Kiban (S2):**
   - Revoking an anchor while other tuples remain is accepted. Those tuples can then no longer be revoked, and re-anchoring the same id revives them.
   - Deletion therefore revokes relations first and the anchor last, and object ids are never reused (with item 5).
   - Upstream: Kiban #73. In 0.1.3, revoke no longer requires the anchor for the owning app, and the deletion order and id-reuse hazard are documented. Hareno keeps relations-first deletion and fresh UUIDs regardless.
   - Positions have one holder and no atomic handover: end then assign leaves the position empty in between. Hareno ends, then assigns, and retries the assign on failure.
   - Upstream: Kiban #75 (the 409 names the current holder, 0.1.3) and #77 (atomic handover with an `expectedAssignmentId` guard, 0.2).

Roadmap versions are not contractual release dates. Kiban adoption stays conditional on launch acceptance criteria.

## 11. Reporting and artifacts

Reporting was agreed in principle. Exact formats and layout requirements remain to be specified.

- Execute jobs durably in workers with deadlines, retry policy, cancellation and resource limits.
- Use the shared semantic/policy pipeline.
- Store report/model revisions, parameters, execution subject, timestamps and available source watermark with artifacts.
- Use controlled browser rendering for dashboard PDFs; paginated reports require explicit page-layout rules.
- Define summarized versus detailed export rights and allowed formats, including CSV and whether XLSX is required.
- Authorize recipients and artifact downloads; use retention, deletion and access expiry rules.
- Avoid promising exactly-once external delivery without an explicit deduplication/retry design.
- Pinned definitions support explaining how a report was produced, but cannot recreate historical source data without snapshots/retention.
- Artifact storage: local disk during prototyping; S3 API from shared environments onward, against SeaweedFS in development and the client's S3-compatible store in production. MinIO is not used: its community repository is archived and official binaries/images stopped after 2025-10.

## 12. Kubernetes and operations

Proposed components: frontend, Go API replicas, query/report workers, browser renderer, application PostgreSQL, shared cache, artifact storage, and Kiban/Keycloak dependencies.

Provide a reproducible versioned installation, Helm configuration, health/readiness checks, resource limits, network policy, secrets integration and explicit database migrations. Define replica-wide source pool budgets; adding pods must not overwhelm the mart.

Kubernetes does not itself supply database HA, correct backups, restore coordination, source performance or upgrade compatibility. Define:

- Client-owned versus team-owned infrastructure responsibilities.
- Supported Kubernetes versions, storage and ingress assumptions.
- Connected versus air-gapped installation and private registry requirements.
- Backup coverage, encryption, retention and restoration exercises.
- Recovery time and recovery point objectives.
- Upgrade order, compatibility windows and rollback limitations after migrations.
- Kiban, Keycloak and application lifecycle coordination.
- Monitoring for refresh lag, queue delay, source latency, errors, authorization failures, cache age and artifact delivery.
- Credential rotation and controlled remote support access.

## 13. Ownership, maintenance and dependency policy

Separate the initial product build, continuing product development and maintenance SLA. Client ownership applies to agreed bespoke source/designs; identify pre-existing team code and third-party dependencies explicitly.

Handover should include source, tests, build tooling, deployment configuration, migrations, dependency notices, operating documentation and recovery procedures. Agree supported connectors/versions, patch responsibilities, incident response, maintenance windows and infrastructure boundaries.

Maintain reproducible dependency versions and a usable build path when public registries are unavailable. Commercial grid or other component licensing must be checked before selection; source availability is not the same as ownership.

Forking an existing BI product cannot satisfy full ownership: even MIT-licensed upstream code remains the upstream authors' copyright, and the client would own only its modifications. Hareno is therefore a clean-room build. Third-party products may be studied for ideas and behavior, but their source is not copied. Source-available directories (for example Lightdash's `ee/`) must not be opened as reference.

The owner intends to commercialise Hareno later (recorded 2026-10-13). The licence policy keeps that open: every dependency is permissive (MIT, Apache-2.0, BSD, ISC) or MPL-2.0 used unmodified, and nothing is AGPL, GPL, SSPL, BSL or source-available. That is why MinIO and Lightdash's `ee/` were excluded. Obligations that remain: ship the Apache-2.0 NOTICE files (Kiban, Keycloak, ECharts and others), and publish any change made to an MPL-2.0 file (River). Licences are not the open question; the contract is. Client ownership of the bespoke source must be reconciled with the owner's right to commercialise it, for example through a licence-back or by defining which parts are pre-existing team code.

Dependencies with early or volatile versioning (Malloy 0.0.x, Kiban 0.1.x) are pinned and their source is vendored or mirrored so the client can build and patch them if upstream stalls.

## 14. Delivery and validation approach

All core areas remain in the product scope. Build a complete vertical workflow first, then expand depth:

Connect PostgreSQL → define dataset/measure → author linked charts and matrix → publish under policies → refresh every five seconds → export and schedule under supported authorization.

Proposed milestones:

1. Establish model/query correctness and an end-to-end representative report workflow.
2. Complete business-user authoring, publishing, interaction, governance and reporting acceptance criteria.
3. Expand calculations, chart coverage, connector support and scale according to agreed requirements.

Milestones are sequencing, not permission to omit required core functions at launch.

Highest-value validation areas:

- Totals, joins, nulls, distinct counts, ratios, time filters and matrix subtotals.
- Object and data policies across queries, caches, previews, exports and schedules.
- Revocation, upstream disablement and worker delegation.
- Refresh concurrency, cancellation, slow sources and partial dashboard failure.
- Report/model revision compatibility and migration.
- Rendering consistency, keyboard use, reduced motion and export fidelity.
- Installation, upgrades, backup, restore and recovery.

Use known-answer data and SQL reference results. Existing report examples can inform behavior, but no interoperability or migration compatibility is required.

Development data mart: a Docker Compose PostgreSQL instance with a deterministic seeded star schema and hand-verified expected results. It deliberately includes a fan-out trap (order-header shipping cost beside order lines), nulls, decimals, time zones, a fiscal calendar, a region dimension for row-policy tests, and a `loaded_at` watermark table. The same dataset serves as the semantic-layer spike oracle and the long-term regression suite.

## 15. Outstanding inputs and decisions

### Workload and service targets

- Concurrent viewers/authors and charts per dashboard.
- Source sizes, growth, query complexity and typical returned points.
- Report/dataset/workspace counts and permission density.
- Initial-load and refresh latency targets, including percentiles.
- Maximum acceptable source-to-screen age and stale-display behavior.
- Availability, maintenance windows, RPO and RTO.

### Product depth

- Initial chart catalog.
- Table/matrix feature acceptance criteria (component selected; §7.2).
- Expression language, time intelligence and advanced relationship support.
- Fiscal calendars, locale, decimal/currency and time-zone rules.
- Exact paginated-report requirements and export formats.
- Sharing/publishing lifecycle and cross-highlighting semantics.
- SQL authoring permissions and administrator data access.

### Platform dependencies

- Kiban launch gates, release availability and Kubernetes ownership.
- Whether consolidated cross-company reporting is required, and its design if so (§10.4 item 8).
- Identity provider, session/revocation expectations and provisioning process.
- Artifact storage implementation and secrets provider.
- Databricks adoption timing, network route and authentication.
- Air-gap requirements, installed platform services and support access.

## 16. Architecture deliverables still to produce

This is the consolidated planning record, not the completed detailed design. Further planning should produce:

1. Capability matrix with launch acceptance criteria and depth boundaries.
2. Formal architecture decision records with alternatives and consequences.
3. System/deployment diagrams and query, refresh, publish and scheduled-delivery flows.
4. Semantic model and expression specification with worked examples.
5. Security model and failure-behavior specification.
6. Sizing assumptions, validation plan and phased delivery/staffing estimates.

## 17. Reference material

References consulted during discussion; library documentation may change. Pin implementation decisions to selected versions during later validation.

- [Nivo component catalog](https://nivo.rocks/components/)
- [Nivo theming](https://nivo.rocks/guides/theming/)
- [Nivo license](https://github.com/plouc/nivo/blob/master/LICENSE.md)
- [ECharts features](https://echarts.apache.org/en/feature.html)
- [ECharts styling](https://echarts.apache.org/handbook/en/concepts/style/)
- [ECharts dynamic updates](https://echarts.apache.org/handbook/en/how-to/data/dynamic-data/)
- [ECharts waterfall composition](https://echarts.apache.org/handbook/en/how-to/chart-types/bar/waterfall/)
- [ECharts license](https://github.com/apache/echarts/blob/master/LICENSE)
- [Databricks Go SQL driver](https://docs.databricks.com/aws/en/dev-tools/go-sql-driver)
- [Databricks Statement Execution API](https://docs.databricks.com/aws/en/dev-tools/sql-execution-tutorial)
- [PostgreSQL transaction isolation](https://www.postgresql.org/docs/current/transaction-iso.html)
- [Kiban v0.1.1 release](https://github.com/rosschiu/kiban/releases/tag/v0.1.1)
- [Kiban v0.1.1 limitations](https://github.com/rosschiu/kiban/blob/v0.1.1/docs/limitations.md)
- [Kiban integration](https://rosschiu.github.io/kiban/integrate/)
- [Kiban licensing](https://github.com/rosschiu/kiban/blob/v0.1.1/LICENSING.md)
- [Power BI visualization overview, for requirement comparison only](https://learn.microsoft.com/en-us/power-bi/visuals/power-bi-visualizations-overview)
- [Malloy](https://github.com/malloydata/malloy)
- [dbt MetricFlow open-sourcing announcement](https://www.getdbt.com/blog/dbt-labs-affirms-commitment-to-open-semantic-interchange-by-open-sourcing-metricflow)
- [Cube license](https://github.com/cube-js/cube/blob/master/LICENSE)
- [Cube Core production guide (Cube Store replication)](https://github.com/cube-js/cube/blob/master/docs-mintlify/cube-core/running-in-production.mdx)
- [Cube data access policies](https://docs.cube.dev/docs/data-modeling/data-access-policies)
- [Lightdash pricing](https://www.lightdash.com/pricing)
- [sqlglot](https://github.com/tobymao/sqlglot)
- [TanStack Table](https://github.com/TanStack/table)
- [SeaweedFS](https://github.com/seaweedfs/seaweedfs)
- [River](https://github.com/riverqueue/river)
- [excelize](https://github.com/qax-os/excelize)
- [Kiban repository (v0.1.2 baseline)](https://github.com/rosschiu/kiban)

Kiban roadmap details in section 10 were supplied directly in the conversation. They have not been independently checked against issue status as part of writing this record.

## 18. Component evaluation decisions (2026-10-12)

Evidence was gathered from repository LICENSE files, source and documentation on 2026-10-12. Each decision lists its validation gate. Items marked unverified in the evaluation remain to be checked during the spike.

### 18.1 Semantic layer / SQL compiler — Malloy (decided, gated)

| Option | License | Verdict | Reason |
|---|---|---|---|
| **Malloy** | MIT | **Selected** | Symmetric aggregates give fan-out-safe measures by design; stateless compile API (`compileModel`/`compileQuery`) produces SQL without executing; Postgres, Databricks and Snowflake dialects |
| dbt MetricFlow ≥ v0.209 | Apache-2.0 (earlier versions AGPL then BSL) | **Fallback** | Multi-vendor governance; strong time intelligence; native sidecar binary with NDJSON IPC, no Python runtime. Weaker BI fit: fan-out avoided by restricting joins, no totals, no row security |
| Cube Core | Apache-2.0 (all self-hosted components) | Rejected | Licence is acceptable. Rejected on shape: a full query server owning execution, caching, queueing and security, which duplicates Hareno's Go layer and policies. Open-source Cube Store has no replication (one node down is a full outage), requires S3/GCS/Azure-class object storage, and has no official Helm chart. Vendor investment is focused on Cloud and AI |
| sqlglot | MIT | Not the core | Parser/transpiler only; join graph, fan-out and measure grain would all be bespoke. Reserve for raw-SQL validation if needed |
| Go-native parsers | Various | Rejected | Single-dialect parsing only; no semantic layer |

Malloy risks: 0.0.x versioning, the structured query interface is marked work-in-progress, a small team, and governance not independently confirmed. Mitigations: Hareno owns its model format and generates Malloy source, so the compiler is replaceable; pin and vendor Malloy; maintain a golden-SQL regression suite.

**Gate:** a two-week spike compiling about 20 known-answer queries (fan-out, totals and subtotals, distinct counts across relationships, row policy injection, period-over-period, nulls) through Malloy and MetricFlow against the development mart. Malloy is confirmed if it passes; otherwise MetricFlow is re-evaluated against the failures.

**S1 result (2026-10-12, PostgreSQL): Malloy confirmed by the owner.** Malloy 17/21 out of the box, 19/21 counting the two query-generation workarounds below. MetricFlow 19/21, but only after upstream model denormalisation (12/21 before). Malloy is confirmed on four conditions:

1. Hareno's planner rejects grain-unsafe queries before compiling: a sum or average grouped by a dimension reached through a one-to-many join below the measure's grain, unless an allocation rule exists. Counts, distinct counts, min and max are allowed: split that way they keep a membership meaning and are not additive, and every total is recomputed (P2-03, `internal/model`).
2. The generator emits guarded division and null-preserving sums.
3. The Postgres fan-out-safe sum's `DOUBLE PRECISION` cast is fixed upstream, carried meanwhile as a vendored patch.
4. Time buckets are rendered in business time by Go.

PostgreSQL is the only source until a further source is admitted. The C01–C21 cases are rerun through that source's Malloy dialect when its connector is built (Databricks: Milestone 3). Evidence: `spikes/s1/FINDINGS.md`.

### 18.2 Existing BI codebases — reference only

Lightdash: root MIT, but `packages/backend/src/ee/` is under the Lightdash Source Available License since 2025-01-27 (production use requires a paid subscription; Lightdash retains rights in modifications). 31 core non-test files import from `ee/`. SSO/SCIM, scheduled delivery, embedding and custom roles are paid. TypeScript backend, tile-based dashboards, around 12,600 commits per year. Not forked; ideas only (dbt YAML `meta` metric convention, fan-out detection behavior, SQL-side pivot/subtotals). No bespoke-ownership-compatible BI fork exists among the evaluated options; Hareno remains a clean-room build.

### 18.3 Kiban — adopt, multi-company

Adopted because company, org-unit and position structure is likely to be needed: position-based sharing that follows the job, org units as row-policy scopes, and an org tree and audit log Hareno would otherwise build. Conditions: §10.4 items 1–15. S2 (2026-10-13) confirmed the SDK integration, `BatchCan`, bounded-scan listing, position reassignment and immediate member/identity deactivation, and found items 12–15. Kiban's team confirmed all of them on 2026-10-13: #72, #73, #74 and #75 are fixed or documented in 0.1.3; #76 and #77 are planned for 0.2. Hareno's workarounds stand until each release is verified. Evidence: `spikes/s2/FINDINGS.md`. All Kiban calls go through Hareno's authorization integration module; BI row/column policies live in Hareno's database and reference Kiban org units, positions and groups by ID. Decoupling later is feasible but not free (§10.1 correction).

### 18.4 Table/matrix — TanStack Table v9 (decided)

See §7.2.

### 18.5 Artifact and development object storage — decided

See §11. SeaweedFS is selected for development S3 (single container, Apache-2.0, active). versitygw (Apache-2.0) is an alternative minimal folder-backed endpoint. rustfs is watched but recently reached 1.0. LocalStack is unusable (free tier non-commercial, token required). Garage (AGPL-3.0) and Ceph RGW (heavy) are not selected.

### 18.6 Job queue — River (decided for now)

River is used unmodified. It is MPL-2.0, a file-level copyleft: unmodified use carries notice obligations only; any modification to River's own files must be published under MPL-2.0, so River is not patched in-tree. Job enqueueing and handlers stay inside Hareno's scheduling module so River can be replaced later (for example by a minimal `FOR UPDATE SKIP LOCKED` queue) without touching other modules.
