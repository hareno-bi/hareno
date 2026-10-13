# S3 presentation spike: findings

Date: 2026-10-13. Gate: PROJECT-PLAN.md Phase 1, S3. Code: [web/src/s3](../../web/src/s3). Data: [web/scripts/s3-snapshot.sql](../../web/scripts/s3-snapshot.sql). Browser checks: [check.mjs](check.mjs).

Demo: **http://139.99.122.192:55450** (chart samples), **http://139.99.122.192:55450/#/matrix** (matrix).

## Status

| Pass criterion | Result |
|---|---|
| Client signs off a themed ECharts sample set | **Waiting for the client.** The sample set is published at the demo URL. |
| TanStack matrix with server subtotals | Met: subtotals and grand total come from the database (`GROUP BY ROLLUP`), with margin recomputed at each level, not averaged |
| 10k+ virtualized rows | Met: 12,004 rows, 33 in the DOM at a time; 60 fps while scrolling at 6,000 px/s |
| Keyboard navigation | Met: ARIA treegrid model, checked in a browser (below) |

## Chart sample set

The sample set uses ECharts 6.1 (Apache-2.0), tree-shaken to the five chart types used, and covers:
- **KPI cards** with change against the prior 12 months.
- **Charts:** line, donut, stacked column, horizontal bar, a combined bar and line chart on two axes, a scatter of 400 customers, and a heatmap.
- **States:** loading, no data, query failed, access removed, and a "data as of … · refreshing" badge for a stale chart (S4).
- **Light and dark mode** (system, light or dark, remembered per browser), plus layout at phone width.

**Design decisions:**
- Colours live in CSS tokens, and the ECharts theme is built from them, so one stylesheet changes both page and charts. The client's brand colours replace `--series-*` and `--accent` in [s3.css](../../web/src/s3/s3.css).
- Amounts carry no currency symbol until one is agreed. Missing values show "–", never 0.
- The data is synthetic: 12,000 customers and 96,000 sales lines over 24 months, generated deterministically in the dev mart by the snapshot SQL. The page reads a JSON snapshot, so the public demo has no API, mart or Kiban behind it.

**Not included:**
- A map. ECharts ships no map geometry, so a map needs boundary data with a licence the client can redistribute. Add it when the client names the regions.

## Matrix prototype

Rows are region → customer and columns are category × (revenue, margin), plus a total. TanStack Table 9.2 builds the header groups and cells, and TanStack Virtual 3.14 renders only the visible rows. The server returns rows already in display order with subtotal rows, so the browser never adds numbers up. Collapsing a region hides its customers and keeps the server's subtotal row.

**Browser checks** (`node check.mjs <url> <dir>`; Chrome, headless, on the shared 8-core dev host, load average 11–16):
- **Virtualization:** 33 of 12,004 rows in the DOM.
- **Keyboard:**
  - arrows move the active cell;
  - Home and End go to the ends of a row, and with Ctrl to the first and last row;
  - Page Up and Page Down move a screen;
  - Enter or Space toggles a region;
  - Left and Right on a region collapse and expand it;
  - Left on a customer goes to its region.

  The active cell is announced through `aria-activedescendant`, and rows carry `aria-level` and `aria-expanded`.
- **Scrolling:** at 100 px per frame (6,000 px/s), frames average 17.2 ms with a p95 of 16.8 ms, which is 60 fps. A fling of 600 px per frame averages 27 ms (about 37 fps) with a worst frame of 117 ms. That is without a GPU, on a loaded host.
- **Layout:** no page errors, and no horizontal page scroll in dark mode or at 390 px wide.

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
