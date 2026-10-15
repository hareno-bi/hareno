# Hareno — capability matrix (draft for owner sign-off)

Drafted 2026-10-15. Status: **proposed**. Nothing here is decided until the owner signs it off; Phase 3 starts against the signed version (PROJECT-PLAN.md, Phase 3). After sign-off, scope changes go through this document (PROJECT-PLAN "Top risks": scope growing toward full Power BI parity).

How to read it. For each area of the product (ARCHITECTURE-PLAN §1: "all core BI areas matter"):

- **Launch**: what the first release does. Each line has a test that shows it works; Phase 3 is done when every launch test passes.
- **Later**: what is deliberately not at launch (the depth boundary). Listed so that nobody expects it, not because it is unwanted.
- **Today**: what Phase 2 already delivers.

Power BI names are given in brackets where they help.

## Decisions for the owner

Most lines below follow from earlier decisions. These need an answer before sign-off; each has a proposal.

| # | Question | Proposal |
|---|---|---|
| D1 | Which chart types are at launch? | The seven built today (column, bar, line, area, stacked column, pie/donut, scatter) plus KPI card, combo (column + line, two axes), table and matrix. Gauge, waterfall, map and others later. |
| D2 | Which export formats? | CSV (today) and Excel (XLSX) of a tile; PDF of a whole dashboard. Paginated (print-layout) reports later. |
| D3 | Scheduled delivery (a dashboard emailed every Monday)? | Yes at launch: PDF or Excel by email to people who may see the data, each getting their own rows. |
| D4 | Drill-down and drill-through? | Drill-down through a hierarchy (year → quarter → month; region → customer) at launch. Drill-through to another page later. |
| D5 | Pages in a dashboard? | Yes, several pages per dashboard, with filters shared across pages. |
| D6 | Can a viewer export underlying detail rows, or only what the tile shows? | Only what the tile shows (summarized) at launch, under the existing `exporter` permission. Detail export later, as its own permission (ARCHITECTURE-PLAN §9). |
| D7 | Locale, currency and calendar | English UI; one currency symbol per dataset (set by the dataset admin); Hong Kong time zone and the April fiscal year as the dev mart has them, set per dataset. |
| D8 | Column-level security (hiding salary from some people) | At launch: a field can be restricted to a permission; people without it cannot select, filter or export it. |

## 1. Data sources and connections

| | Capability | Test | Today |
|---|---|---|---|
| Launch | PostgreSQL connection with a read-only credential, registered by an administrator | A connection is added in the app; queries run as the read-only role; a write is refused (known answer Q19) | Configured in a file; read-only role enforced |
| Launch | Credentials stored encrypted, never shown again | The stored secret is unreadable without the key; the API never returns it | In the server's config file, not yet in the app |
| Launch | Freshness watermark per connection | Dashboards show "data as of" from the watermark (Q18) | Done |
| Later | Databricks, Snowflake, other databases (milestone 3, on evidence); joining across sources | | |

## 2. Semantic modeling (datasets) [Power BI: semantic model]

| | Capability | Test | Today |
|---|---|---|---|
| Launch | Dataset builder: pick tables, confirm joins and their kind, define measures (PROJECT-PLAN Phase 3, "Datasets made and named in Hareno") | A dataset admin builds the sales dataset in the app; it gives the known answers Q01–Q16 | Datasets are files written by a developer |
| Launch | Field names and number formats edited in the app | A rename shows on every dashboard without a restart; the change is audited | Names and formats in the file (`display:`) |
| Launch | Measures: sum, count, distinct count, average, min, max, ratio, arithmetic on measures | Totals recompute ratios (Q10, Q16); sums of nothing are empty, not 0 | Done |
| Launch | Safety: a measure that would double count is refused, not computed | Shipping by product category is refused (Q04) | Done |
| Launch | Time: business time zone, months, fiscal calendar | Q12 and Q13 through the dataset | Time zone done; fiscal calendar not yet |
| Launch | Calculated fields: simple expressions on a row (e.g. `quantity * price`), with a defined small language | Each function in the language has a worked example that passes | Only in the dataset file |
| Launch | Hierarchies for drill-down (D4) | Year → quarter → month drills with correct totals at each level | Not started |
| Launch | Period comparisons: previous period, running total | Q14 through the dataset | Not started |
| Launch | Dataset revisions: a change shows which dashboards it affects before it is saved | Renaming or removing a measure lists the tiles that use it | Not started |
| Later | Many-to-many relationships, several fact tables in one dataset, rank and moving averages, what-if parameters | | |

## 3. Report authoring (designer)

| | Capability | Test | Today |
|---|---|---|---|
| Launch | Grid canvas: place, resize and align tiles | A tile resized and moved stays so after publish | Tiles half or full width, moved up and down |
| Launch | Pages (D5) | A three-page dashboard publishes; filters carry across pages | One page |
| Launch | Tiles from a dataset, and SQL tiles for trusted users | Both kinds on one dashboard | Done |
| Launch | Draft, publish, unchanged published revisions, roll back to an earlier revision | Viewers see revision 3 while the author edits; rolling back to 2 works | Draft and publish done; rollback not yet |
| Launch | Autosave and edit-conflict warning | Two editors: the second save is refused with a clear message, nothing is lost | Conflict check done; no autosave |
| Launch | Phone viewing: tiles stack | A dashboard reads at phone width without sideways scrolling | Done for today's tiles |
| Later | Real-time co-editing, templates, themes per dashboard | | |

## 4. Visualization

| | Capability | Test | Today |
|---|---|---|---|
| Launch | Chart catalog (D1) in the signed-off look, light and dark | Each type renders the sample data in both themes; screenshots signed off | Seven chart types, signed-off look |
| Launch | Number formats from the dataset on axes, tooltips and cells | Margin shows as 61.8% everywhere | Done |
| Launch | Table and matrix: subtotals, grand total, up to 3 row levels and 1 column level, sticky headers | Q15 matrix through the dataset | Done |
| Launch | Conditional formatting on table and matrix cells (colour by value) | A rule colours negative profit red | Not started |
| Launch | Accessibility: keyboard use, contrast, reduced motion, a table alternative for every chart | Accessibility review passes (Phase 4) | Partly |
| Later | Maps, gauge, waterfall, custom visuals, a second chart library | | |

## 5. Interactive analysis

| | Capability | Test | Today |
|---|---|---|---|
| Launch | Filter bar and click-to-filter across tiles | A click on North filters the other tiles | Done |
| Launch | Filters and selection kept through the 5-second refresh | A refresh does not reset filters | Done |
| Launch | Drill-down (D4) | Clicking 2026 shows its quarters | Not started |
| Launch | Date range filter | "Last 3 months" narrows every tile with a date | Not started |
| Later | Drill-through to another page, cross-highlighting, bookmarks, natural-language questions | | |

## 6. Reporting and export

| | Capability | Test | Today |
|---|---|---|---|
| Launch | CSV export of a tile, the downloader's own rows only | Exit suite: export as a North-only viewer holds North only | Done |
| Launch | Excel export (D2) | Same test as CSV, in XLSX | Not started |
| Launch | PDF of a dashboard (D2) | The PDF matches the screen for a North-only viewer | Not started |
| Launch | Scheduled email delivery (D3) | A weekly schedule sends each recipient their own rows; a recipient who lost access gets nothing | Not started |
| Later | Paginated print-layout reports, detail export (D6), delivery to chat tools | | |

## 7. Governance and security

| | Capability | Test | Today |
|---|---|---|---|
| Launch | Sign-in through Kiban/Keycloak; company boundaries | A user of another company sees nothing | Done |
| Launch | Row policy from Kiban on view, preview, export and schedules | Exit suite (Q17) plus the schedule test | Done except schedules |
| Launch | Column policy (D8) | A restricted field cannot be selected, filtered or exported without the permission | Not started |
| Launch | Permissions: viewer, editor, SQL user, dataset user, dataset admin, exporter, data sharer, connection admin | Each permission's allow and deny cases have a test | All but dataset admin and connection admin |
| Launch | Sharing with people, and with their own data or the sharer's | Done today; tests in place | Done |
| Launch | Catalog: a list of dashboards and datasets a person may see | The list never shows something the person cannot open | Dashboards listed |
| Launch | Audit log: who viewed, exported, shared, published, changed a dataset | Each action writes an audit record an administrator can search | Logged to the server log, not searchable |
| Later | Workspaces, certification of datasets, data lineage, usage analytics | | |

## 8. Operations

| | Capability | Test | Today |
|---|---|---|---|
| Phase 3 | Development Kubernetes namespace (moved from P2-11) | The exit suite passes there | Runs on one server |
| Phase 4 | Helm install, upgrades, backup and restore, monitoring, load test | PROJECT-PLAN Phase 4 exit | |
| Phase 4 | Workload targets (users, response times, availability) | Agreed with the client before Phase 4 (ARCHITECTURE-PLAN §15) | |
