# S4 refresh spike: findings

Date: 2026-10-13 (rows 13–15 added the same day). Gate: PROJECT-PLAN.md Phase 1, S4; ARCHITECTURE-PLAN.md §8. Harness: [main.go](main.go). Raw output: [results.txt](results.txt).

## Setup

**Workload:**
- Simulated viewers poll a report of 10 charts every 5 s, never overlapping their own polls. The first poll comes at a random phase.
- 5 reports × 10 charts × 3 row-policy scopes (North, South, West) gives **K = 150 distinct queries**.
- Each chart runs real SQL on the dev mart as `hareno_reader`, slowed with `pg_sleep` to 40–200 ms (slow-source scenarios: 0.4–2 s).

**Infrastructure:**
- Valkey 8.1 (`compose.yaml`, port 55435) is the shared result cache.
- Each simulated API replica has an 8-connection pool to the mart.
- Each scenario runs for 25 s after a 5 s warm-up.

**Host:** shared, 8 cores, with a load average of 20–25 during the run. Latencies are pessimistic. The query *counts*, which are the question here, do not depend on host speed.

## Recommendation

**Five-second polling stays bounded. Adopt these refresh rules** (item 5 revised after rows 13–15):

1. **Shared result cache in Valkey.** Cache identity follows §8.2. Read the whole report in one `MGET` per refresh.
2. **Deduplicate in-flight work on two levels:** per replica (singleflight), and across replicas with a Valkey lock (`SET NX PX`). The waiter polls for the winner's result.
3. **Serve stale while one refresh runs in the background.** Above a hard age ceiling, wait for the refresh instead. Mark stale results as such in the UI (§8.1).
4. **Cap source concurrency per connection** with the pool size, so the mart is never overrun however many viewers there are.
5. **Refresh on change where the mart has a load watermark.** Poll one cheap watermark query per connection every second on a reserved connection, and refresh cached charts only when it moves. Without a watermark, use the timer-based TTL, with an admin-set minimum per connection as the overload control. A publish-time cost estimate is deferred until a client outgrows these.
6. **Keep row-policy scopes coarse.** Viewers share results only when their effective scope is the same. A scope per user (for example "my own customers") multiplies K by the number of users and removes sharing.

## Results

| # | Scenario | Source queries/s | Bound K/TTL | Max in flight | Refresh p95 | Data age p95 | Older than TTL + query |
|---|---|---|---|---|---|---|---|
| 1 | no cache, 10 viewers | 20.0 | – | 23 | 370 ms | 0.2 s | – |
| 2 | **no cache, 100 viewers** | **91.6** (saturated) | – | **1000** | **15.5 s** | 0.2 s | – |
| 3 | cache, 10 viewers | 16.0 | 37.5 | 14 | 250 ms | 1.9 s | 0% |
| 4 | cache, 100 viewers | 30.3 | 37.5 | 75 | 660 ms | 3.5 s | 0% |
| 5 | cache, 1,000 viewers | 35.3 | 37.5 | 137 | 210 ms | 3.9 s | 0.1% |
| 6 | **cache, 5,000 viewers** | **36.8** | 37.5 | **150** | 160 ms | 3.9 s | 0.1% |
| 7 | cache, 1,000 viewers, 3 replicas, no lock | 68.9 | 112.5 | 319 | 210 ms | 3.9 s | 0.1% |
| 8 | **cache, 1,000 viewers, 3 replicas, lock** | **33.9** | 37.5 | 148 | 200 ms | 3.9 s | 0.1% |
| 9 | **stale-while-revalidate, 1,000 viewers** | 36.6 | 37.5 | 145 | **< 10 ms** | 4.0 s | 3.0% |
| 10 | slow source, cache, 1,000 viewers | 6.6 | 37.5 | 150 | **18.9 s** | 3.3 s | 0% |
| 11 | slow source, stale-while-revalidate, 1,000 viewers | 6.6 | 37.5 | 150 | 16.1 s | **21.6 s** | 60.7% |
| 12 | slow source, stale-while-revalidate, **TTL 30 s**, 1,000 viewers | 3.1 | 5.0 | 150 | 8.0 s* | 31.6 s | 6.2% |
| 13 | **change detection, loads every 60 s**, 1,000 viewers | **2.5** | 2.5 | 143 | 0 s | **7.3 s†** | 3.6%‡ |
| 14 | change detection, loads every 15 s, 1,000 viewers, 3 replicas, lock | 10.0 | 10.0 | 136 | 0 s | 5.3 s† | 3.9%‡ |
| 15 | slow source, change detection, loads every 60 s, 1,000 viewers | 1.9 | 2.5 | 150 | 20 ms | 24.8 s† | 14.8%‡ |

Cache TTL is 4 s unless stated. *Row 12's p95 latency is the cold start: filling 150 slow keys through 8 connections takes about 22 s, which runs into the measured window. Hit latency is under 10 ms.

Rows 13–15 (run 09:46Z, load average 11–18): the bound is K ÷ load interval, and source q/s excludes the version checks (1 per second per replica). †Load to screen: the time from a data load until a viewer is shown the new version, per chart, which is what the user perceives. Rows 1–12 measure data age at delivery, which does not include the wait for a load. ‡Charts shown from a version older than the latest load. The load clock is simulated, since the dev mart does not load data; the check query is one round trip (`SELECT $1::bigint`), standing in for reading a one-row load log.

## What it shows

- **Without a cache, load grows with viewers and collapses early.**
  - 100 viewers demand 200 queries/s. The 8-connection pool delivers about 92/s.
  - The queue reaches 1,000 waiting queries and refreshes take 12–15 s.
  - Each viewer's 5-second refresh silently becomes about 15 s.
- **With the cache and in-flight deduplication, load depends on the number of distinct queries, not on viewers.**
  - From 10 to 5,000 viewers (rows 3–6), source queries rise from 16/s to 36.8/s and stop at the bound of K/TTL = 37.5.
  - Queries in flight never exceed K = 150.
  - 5,000 viewers cost the mart the same as 1,000.
- **Across replicas, the cross-replica lock matters.**
  - With 3 replicas and no lock, each replica fills the same keys: 68.9 queries/s, and 1,108 occasions where a key ran on two replicas at once.
  - The Valkey lock brings this back to 33.9/s with zero overlaps.
  - The lock costs one `SET NX` per fill and a 25 ms poll for losers.
- **Stale-while-revalidate removes refresh waits.**
  - Refresh p95 drops from about 200 ms to under 10 ms, at the same source rate and nearly the same data age.
  - 3% of charts were older than TTL plus query time, during the moments a background refresh was running.
- **When the source is too slow, nothing yields fresh data. The design decides only who waits.**
  - Rows 10–11 use 150 queries of 0.4–2 s on 8 connections: capacity is about 6.7 queries/s against a demand of 37.5/s.
  - Plain cache: viewers wait up to 19 s for fresh results.
  - Stale-while-revalidate: viewers get an instant answer that is up to 22 s old, with 61% of charts past the freshness target.
  - Either way the load stays bounded: in flight ≤ K, and the source runs at its own capacity.
  - Row 12 shows the remedy: a 30 s TTL brings demand (5/s) under capacity, and the system settles.
  - Freshness on a timer is a capacity contract per report, not a global constant. Change detection (below) removes most of that problem.
- **Change detection cuts source load by an order of magnitude and handles slow sources by itself.** This is what Looker (datagroups) and Power BI (automatic page refresh with change detection) do.
  - With loads every 60 s, the source runs 2.5 queries/s against 36.8 for the 4 s TTL (row 13 vs row 5), plus one check per second per replica.
  - Viewers see a load 4.7 s (p50) / 7.3 s (p95) after it lands: check interval, then a refill burst, then the viewer's next poll. That is as quick as the timer, for a fifteenth of the load.
  - Three replicas with the lock and loads every 15 s (row 14) still hit the bound exactly: 10 queries/s, 5.3 s p95.
  - On the slow source (row 15), load stays at 1.9 queries/s with no tuning. The delay becomes the time to recompute the report through 8 connections, 25 s p95. On the timer, the same source needed a hand-picked 30 s TTL (row 12).
  - Each load invalidates all K keys at once, so the pool sees a burst of K queries. The concurrency cap absorbs it (max in flight 143–150, as on the timer).
  - It needs a load watermark from the mart, which is the client's to provide. The same watermark gives the UI a true "data as of" time (ARCHITECTURE-PLAN §8.2).
- **The freshness the user sees** is data age plus the time since the last poll. With a 4 s TTL and 5 s polling, delivered data is at most about 4 s old, and the screen at most about 9 s. The client should agree that number (§15).

## Not covered

- Kiban decisions per refresh. S2 settled this with a 30 s decision cache; with it, a refresh costs Kiban one check per 30 s per viewer.
- Browser behaviour: hidden-tab pause, cancelling superseded filter queries, keeping state.
- Change detection with real loads: a real watermark query, loads that land mid-refresh, and partial loads (a watermark that moves before every table is loaded). The last needs the mart to commit a load atomically or publish the watermark after it.
- Consistent-snapshot mode, large result sets, and Valkey outages. Proposed fallback for an outage: per-replica singleflight only, still bounded at K per replica.
- Real query costs, which depend on the client's mart (client-owned per §14).

## Running the spike

```sh
docker compose up -d mart cache
cd spikes/s4 && go run .          # about 12 minutes; -only N runs one scenario
```
