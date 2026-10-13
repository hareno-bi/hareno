# S4 refresh spike: findings

Date: 2026-10-13. Gate: PROJECT-PLAN.md Phase 1, S4; ARCHITECTURE-PLAN.md §8. Harness: [main.go](main.go). Raw output: [results.txt](results.txt).

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

**Five-second polling stays bounded. Adopt these refresh rules:**

1. **Shared result cache in Valkey.** Cache identity follows §8.2. Read the whole report in one `MGET` per refresh.
2. **Deduplicate in-flight work on two levels:** per replica (singleflight), and across replicas with a Valkey lock (`SET NX PX`). The waiter polls for the winner's result.
3. **Serve stale while one refresh runs in the background.** Above a hard age ceiling, wait for the refresh instead. Mark stale results as such in the UI (§8.1).
4. **Cap source concurrency per connection** with the pool size, so the mart is never overrun however many viewers there are.
5. **Check source capacity when a report is published.** Hareno estimates `Σ(query cost) / cache TTL` for the report's distinct queries and compares it with the connection's concurrency budget. When it doesn't fit, the publisher must accept a longer TTL (a slower freshness promise) or simplify the report.
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

Cache TTL is 4 s unless stated. *Row 12's p95 latency is the cold start: filling 150 slow keys through 8 connections takes about 22 s, which runs into the measured window. Hit latency is under 10 ms.

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
  - This is condition 5: freshness is a capacity contract per report, not a global constant.
- **The freshness the user sees** is data age plus the time since the last poll. With a 4 s TTL and 5 s polling, delivered data is at most about 4 s old, and the screen at most about 9 s. The client should agree that number (§15).

## Not covered

- Kiban decisions per refresh. S2 settled this with a 30 s decision cache; with it, a refresh costs Kiban one check per 30 s per viewer.
- Browser behaviour: hidden-tab pause, cancelling superseded filter queries, keeping state.
- Consistent-snapshot mode, large result sets, and Valkey outages. Proposed fallback for an outage: per-replica singleflight only, still bounded at K per replica.
- Real query costs, which depend on the client's mart (client-owned per §14).

## Running the spike

```sh
docker compose up -d mart cache
cd spikes/s4 && go run .          # about 6 minutes; -only N runs one scenario
```
