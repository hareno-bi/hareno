# Hareno — capability matrix (draft for owner sign-off)

Drafted 2026-10-15. Status: **proposed**. Nothing here is decided until the owner signs it off; Phase 3 starts against the signed version (PROJECT-PLAN.md, Phase 3). After sign-off, scope changes go through this document (PROJECT-PLAN "Top risks": scope growing toward full Power BI parity).

How to read it. For each area of the product (ARCHITECTURE-PLAN §1: "all core BI areas matter"):

- **Launch**: what the first release does. Each line has a test that shows it works; Phase 3 is done when every launch test passes.
- **Later**: what is deliberately not at launch (the depth boundary). Listed so that nobody expects it, not because it is unwanted.
- **Today**: what Phase 2 already delivers.

Power BI names are given in brackets where they help.

## Decisions for the owner

Answers given 2026-10-15 are recorded; open items still need an answer.

| # | Question | Answer or proposal | Status |
|---|---|---|---|
| D1 | Which chart types are at launch? | Proposal after the comparison in Appendix A: today's seven, plus stacked bar, 100% stacked, stacked area, combo (column + line, two axes), bubble, KPI card, gauge, funnel, treemap and heat map; table and matrix. All in ECharts; no Nivo at launch. | Open |
| D2 | Which export formats? | CSV (today) and PDF first. Excel later. | Decided |
| D3 | Scheduled delivery? | Yes: by email, as CSV, PDF or a picture (PNG) of the dashboard. Each recipient gets their own rows. | Decided |
| D4 | Drill-down? | Drill-down in place, inside the same chart, as Power BI does it (click 2026, the chart shows its quarters; a button goes back up). Drill-through (jumping to another page) is later. | Decided |
| D5 | Pages, and phone use? | Several pages per dashboard. Dashboards scroll down: a page may be longer than the screen, and on a phone every page becomes one column of tiles in reading order, each tile the screen's width. | Decided |
| D6 | Export of detail rows? | Not needed for now. Downloads hold what the tile shows. | Decided |
| D7 | Languages, currency and calendar | Languages: English, Japanese and Traditional Chinese. Currency and calendar follow the data (see Appendix B). | Languages decided; Appendix B to confirm |
| D8 | Column-level security | Explained in Appendix C. | Open |

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
| Launch | Time: business time zone; fiscal periods from the mart's date table (Appendix B) | Q12 and Q13 through the dataset | Time zone done; date table not yet joined |
| Launch | Calculated fields: simple expressions on a row (e.g. `quantity * price`), with a defined small language | Each function in the language has a worked example that passes | Only in the dataset file |
| Launch | Hierarchies for drill-down (D4) | Year → quarter → month drills with correct totals at each level | Not started |
| Launch | Period comparisons: previous period, running total | Q14 through the dataset | Not started |
| Launch | Dataset revisions: a change shows which dashboards it affects before it is saved | Renaming or removing a measure lists the tiles that use it | Not started |
| Later | Many-to-many relationships, several fact tables in one dataset, rank and moving averages, what-if parameters | | |

## 3. Report authoring (designer)

| | Capability | Test | Today |
|---|---|---|---|
| Launch | Grid canvas: place, resize and align tiles | A tile resized and moved stays so after publish | Tiles half or full width, moved up and down |
| Launch | Pages, and scrolling dashboards for phones (D5) | A three-page dashboard publishes; filters carry across pages; at phone width each page is one scrolling column in reading order | One page; tiles stack on phones |
| Launch | Tiles from a dataset, and SQL tiles for trusted users | Both kinds on one dashboard | Done |
| Launch | Draft, publish, unchanged published revisions, roll back to an earlier revision | Viewers see revision 3 while the author edits; rolling back to 2 works | Draft and publish done; rollback not yet |
| Launch | Autosave and edit-conflict warning | Two editors: the second save is refused with a clear message, nothing is lost | Conflict check done; no autosave |
| Launch | Languages: English, Japanese, Traditional Chinese (D7) | Every screen, message and number/date format switches with the user's language; dataset field names can be given per language | English only |
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
| Launch | Drill-down in place (D4) | Clicking 2026 shows its quarters in the same chart; going back up restores the year view | Not started |
| Launch | Date range filter | "Last 3 months" narrows every tile with a date | Not started |
| Later | Drill-through to another page, cross-highlighting, bookmarks, natural-language questions | | |

## 6. Reporting and export

| | Capability | Test | Today |
|---|---|---|---|
| Launch | CSV export of a tile, the downloader's own rows only | Exit suite: export as a North-only viewer holds North only | Done |
| Launch | PDF of a dashboard (D2) | The PDF matches the screen for a North-only viewer | Not started |
| Launch | Scheduled email delivery as CSV, PDF or PNG (D3) | A weekly schedule sends each recipient their own rows; a recipient who lost access gets nothing | Not started |
| Later | Excel export, paginated print-layout reports, detail export (D6), delivery to chat tools | | |

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

## Appendix A. Chart types: Hareno, Power BI, Tableau, ECharts, Nivo (D1)

Built in only: Power BI's marketplace "custom visuals" and Tableau's workarounds are not counted. ✓ = has it, – = does not. ECharts is the decided chart library (ARCHITECTURE-PLAN §7.1); Nivo is added only for a chart ECharts cannot reasonably draw.

| Chart | Power BI | Tableau | Hareno today | ECharts | Nivo | Proposal |
|---|---|---|---|---|---|---|
| Column and bar | ✓ | ✓ | ✓ | ✓ | ✓ | Have |
| Stacked column | ✓ | ✓ | ✓ | ✓ | ✓ | Have |
| Stacked bar (horizontal) | ✓ | ✓ | – | ✓ | ✓ | Launch |
| 100% stacked column and bar | ✓ | ✓ | – | ✓ | – | Launch |
| Line | ✓ | ✓ | ✓ | ✓ | ✓ | Have |
| Area | ✓ | ✓ | ✓ | ✓ | ✓ | Have |
| Stacked area | ✓ | ✓ | – | ✓ | ✓ | Launch |
| Combo: column + line, two axes | ✓ | ✓ | – | ✓ | – | Launch |
| Pie and donut | ✓ | ✓ | ✓ | ✓ | ✓ | Have |
| Scatter | ✓ | ✓ | ✓ | ✓ | ✓ | Have |
| Bubble (scatter sized by a value) | ✓ | ✓ | – | ✓ | ✓ | Launch |
| Table | ✓ | ✓ | ✓ | (own) | – | Have |
| Matrix (pivot with subtotals) | ✓ | ✓ | ✓ | (own) | – | Have |
| KPI card (one big number, change vs last period) | ✓ | ✓ | – | (own) | – | Launch |
| Gauge | ✓ | – | – | ✓ | – | Launch |
| Funnel | ✓ | – | – | ✓ | ✓ | Launch |
| Treemap | ✓ | ✓ | – | ✓ | ✓ | Launch |
| Heat map (grid coloured by value) | via matrix colours | ✓ | – | ✓ | ✓ | Launch |
| Waterfall | ✓ | – | – | ✓ (stacked-bar method) | – | Later |
| Ribbon | ✓ | – | – | – | – | Later |
| Filled map and point map | ✓ | ✓ | – | ✓ (needs map shapes) | ✓ | Later: needs map boundary data with a free licence |
| Histogram | – | ✓ | – | ✓ | – | Later |
| Box plot | – | ✓ | – | ✓ | ✓ | Later |
| Gantt | – | ✓ | – | ✓ (custom series) | – | Later |
| Bullet graph | – | ✓ | – | ✓ (custom series) | ✓ | Later |
| Packed bubbles / circle packing | – | ✓ | – | – | ✓ | Later (Nivo) |
| Sunburst | – | – | – | ✓ | ✓ | Later |
| Sankey (flows) | – | – | – | ✓ | ✓ | Later |
| Radar | – | – | – | ✓ | ✓ | Later |
| Calendar heat map | – | – | – | ✓ | ✓ | Later |
| Candlestick (finance) | – | – | – | ✓ | – | Later |
| Chord, beeswarm | – | – | – | ✓ (ECharts 6) | ✓ | Later |
| Waffle, marimekko, bump | – | – | – | – | ✓ | Later (Nivo) |
| AI visuals (key influencers, decomposition tree, Q&A) | ✓ | partly | – | – | – | Not planned |

With the proposal, Hareno at launch has every built-in chart of Power BI and Tableau except waterfall, ribbon, maps, histogram, box plot, Gantt, bullet and packed bubbles, which come later, and the AI visuals. Everything at launch is ECharts. Of the later charts, only packed bubbles and waffle, marimekko and bump need Nivo.

## Appendix B. Currency and calendar (D7)

The owner's view is that these belong to the data preparation in the mart, not to Hareno. Agreed, with one exception, time zone:

- **Converting money** (US$ to HK$, at which rate, on which date) is data preparation: the mart stores the amounts to report. Hareno never converts.
- **Showing a currency symbol** is only a display format, like showing 0.618 as 61.8%: a dataset field's format can say "money, HK$". Number and date layout (1,234.50; 2026年10月15日) follow the viewer's language.
- **Fiscal year and quarter** come from the mart's date table (the dev mart has `dim_date` with fiscal years from April). Hareno joins to it like any other table and does not calculate fiscal periods.
- **Time zone** is the exception. The mart stores the moment an order happened. Which month it falls in depends on the time zone: order 105 is March in UTC but April in Hong Kong (known answer Q12). Hareno keeps one business time zone per dataset so months and days are grouped correctly. If the mart stores local business dates, the dataset uses those, and the time zone matters only for timestamps.

## Appendix C. Column-level security (D8)

Row security decides **which rows** a person sees: alice sees North's orders only. Column security decides **which fields** a person sees. Power BI calls it object-level security.

Example: an HR dataset has employee, department and salary. Managers may see salaries; everyone else may count employees by department but must not see salary.

Hiding the salary column on screen is not enough. Without column security, a person could still learn salaries by:

- filtering "salary above 100,000" and counting who is left;
- sorting by salary;
- averaging salary over a department of one person;
- downloading the CSV, or receiving a scheduled email.

Column security means the server refuses a restricted field everywhere: picking it, filtering, sorting, using it in a calculation, export and schedules. It is granted through a Kiban permission, for example "may see salary".

Options:

1. **At launch, as above.** It gives the most complete protection. Its cost is that every query path checks it, and every new feature must too. It needs about 2 to 3 weeks, plus tests on every path.
2. **Later. Until then, use two datasets.** A dataset admin makes "HR" with salary and "HR summary" without it, and grants only managers the first. This is simple and safe. Its costs are two datasets to maintain, and that a dashboard mixing both needs care.

Proposal: option 2 at launch. Add option 1 when a client has data that needs it.
