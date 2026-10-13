# S3 presentation spike: findings

Date: 2026-10-13. Gate: PROJECT-PLAN.md Phase 1, S3. Code: [web/src/s3](../../web/src/s3). Data: [web/scripts/s3-snapshot.sql](../../web/scripts/s3-snapshot.sql). Browser checks: [check.mjs](check.mjs).

Demo: **http://139.99.122.192:55450** (chart samples), **http://139.99.122.192:55450/#/matrix** (matrix).

## Status

| Pass criterion | Result |
|---|---|
| Client signs off a themed ECharts sample set | **Waiting for the client.** The sample set is published at the demo URL. |
| TanStack matrix with server subtotals | Met: subtotals and grand total come from the database (`GROUP BY ROLLUP`), with margin recomputed at each level, not averaged |
| 10k+ virtualized rows | Met: 11,934 rows, 33 in the DOM at a time; 60 fps while scrolling at 6,000 px/s |
| Keyboard navigation | Met: ARIA treegrid model, checked in a browser (below) |

## Chart sample set

The sample set is modelled on **Northgate Air Cargo Terminal**, a fictional cargo handling facility. It uses ECharts 6.1 (Apache-2.0), tree-shaken to the six chart types used, and covers:
- **KPI cards** with change against the prior 12 months: tonnes handled, flights handled, handling revenue, and ULDs ready by cut-off.
- **Charts:**
  - line: tonnage by flow (export, import, transhipment);
  - gauge: warehouse occupancy;
  - stacked column and donut: tonnage by cargo type (general, e-commerce, perishables, pharma, dangerous goods);
  - combined bar and line on two axes: tonnage against build-up punctuality;
  - horizontal bar: top 10 airlines;
  - heatmap: truck arrivals at the landside docks by weekday and hour;
  - scatter, log scale: weight of 400 shipments against dwell time.
- **States:** loading, no data, query failed, access removed, and a "data as of … · refreshing" badge for a stale chart (S4).
- **Light and dark mode** (system, light or dark), **three palettes** (Hareno, Harbour, Signal), both remembered per browser, plus layout at phone width.

**Theming:** ECharts takes a theme object at chart creation: colours, fonts, axes, legend, tooltip and per-series defaults. Hareno builds that object from the page's CSS tokens ([theme.ts](../../web/src/s3/theme.ts)), so one stylesheet themes page and charts together. Switching mode or palette re-creates the charts with the new theme. A client brand is one more palette block in [s3.css](../../web/src/s3/s3.css) (`--series-1…6`, `--accent`, heatmap range, and optionally `--font`), with a light and a dark variant.

**Design decisions:**
- Amounts carry no currency symbol until one is agreed. Missing values show "–", never 0.
- The data is synthetic and every name is invented: 12 airlines, 11,921 flights over 24 months (schedule growing about 10% a year, peak season in Q4), split by flow and cargo type, with handling revenue per kg varying by cargo type, flow and airline. It is generated deterministically in the dev mart by the snapshot SQL. The page reads a JSON snapshot, so the public demo has no API, mart or Kiban behind it.

**Not included:**
- A map, such as a route map of origins and destinations. ECharts ships no map geometry, so a map needs boundary data with a licence the client can redistribute.

## Matrix prototype

Rows are airline → flight and columns are flow (export, import, transhipment) × (tonnes, yield per kg), plus a total. TanStack Table 9.2 builds the header groups and cells, and TanStack Virtual 3.14 renders only the visible rows. The server returns rows already in display order with subtotal rows, so the browser never adds numbers up. Collapsing an airline hides its flights and keeps the server's subtotal row. Yield is revenue ÷ kg recomputed from sums at each level, which is the subtotal trap a client-side average gets wrong.

**Browser checks** (`node check.mjs <url> <dir>`; Chrome, headless, on the shared 8-core dev host, load average 11–16):
- **Virtualization:** 33 of 11,934 rows in the DOM.
- **Keyboard:**
  - arrows move the active cell;
  - Home and End go to the ends of a row, and with Ctrl to the first and last row;
  - Page Up and Page Down move a screen;
  - Enter or Space toggles an airline;
  - Left and Right on an airline collapse and expand it;
  - Left on a flight goes to its airline.

  The active cell is announced through `aria-activedescendant`, and rows carry `aria-level` and `aria-expanded`.
- **Scrolling:** at 100 px per frame (6,000 px/s), frames average 16.6–17.2 ms with a p95 of 16.7–16.8 ms, which is 60 fps. A fling of 600 px per frame averages 22–27 ms (about 40 fps), with a worst frame of 50–117 ms across runs. That is without a GPU, on a loaded host.
- **Layout:** no page errors, and no horizontal page scroll in dark mode, at 390 px wide, or with the Harbour and Signal palettes.

**What this says about effort:** the matrix is about 200 lines once the server provides subtotal rows. The hard part is the query (Malloy must produce the same rollup; S1 covered nesting, not subtotal rows). Sorting within groups, column resizing, frozen header columns at phone width, cell copy and an export that matches what is shown are not built. These remain the main Phase 2–3 matrix work.

## Not covered

- Real client data and branding, and a map (above).
- Screen-reader testing with NVDA or VoiceOver. Only the ARIA structure is checked.
- Bundle size: 896 kB minified (294 kB gzip), mostly ECharts. Splitting the chart and matrix pages is a Phase 2 task.
- Matrix rows from Malloy instead of hand-written SQL.

## Running

```sh
web/scripts/s3-snapshot.sh                         # sample data from the dev mart into web/public/s3
(cd web && npm run build)
docker compose --profile demo up -d demo           # serves web/dist on 0.0.0.0:55450
(cd spikes/s3 && npm install && node check.mjs http://localhost:55450/ /tmp)
```
