// S4 spike harness: does five-second polling stay bounded? Simulated viewers poll a report of
// ten charts every five seconds through one or more simulated API replicas. Charts run real SQL
// against the dev mart, slowed with pg_sleep to stand in for heavier queries. The modes compare
// no caching, a shared Valkey result cache with per-replica in-flight deduplication, an optional
// cross-replica lock, and stale-while-revalidate. Throwaway spike code; the findings are the
// deliverable (FINDINGS.md).
//
//	go run .            # all scenarios, about 6 minutes
//	go run . -only 3    # one scenario
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	martDSN  = "postgres://hareno_reader:hareno_reader@localhost:55433/mart"
	valkey   = "localhost:55435"
	reports  = 5
	charts   = 10
	poll     = 5 * time.Second
	warmup   = 5 * time.Second
	hardTTL  = 60 * time.Second // stale-while-revalidate: never serve older than this
	waitPoll = 25 * time.Millisecond
)

var scopes = []string{"North", "South", "West"} // row-policy scopes: viewers in one scope share results

type scenario struct {
	name     string
	viewers  int
	replicas int
	mode     string        // none | cache | swr
	lock     bool          // cross-replica in-flight lock in Valkey
	ttl      time.Duration // cache: entry lifetime; swr: age after which a background refresh starts
	cost     time.Duration // the slowest chart's pg_sleep; chart c sleeps cost*(c%5+1)/5
	pool     int           // source connections per replica
	run      time.Duration
}

type entry struct {
	Rows       [][]any `json:"rows"`
	ExecutedAt int64   `json:"executedAt"` // unix nanos when the source query started
}

// replica is one API server: its own connection pool and in-flight deduplication, a shared Valkey.
type replica struct {
	id  int
	s   scenario
	db  *pgxpool.Pool
	rdb *redis.Client
	sf  singleflight.Group
}

// stats are shared by every replica and viewer of a scenario.
type stats struct {
	measuring  atomic.Bool
	done       atomic.Bool  // the run is over; background refreshes still finishing are not errors
	execs      atomic.Int64 // source queries started in the measured window
	inflight   atomic.Int64
	maxInfl    atomic.Int64
	overlap    atomic.Int64 // a key executing on two replicas (or twice on one) at once
	errors     atomic.Int64 // excluding requests cut off by the end of the run
	firstErr   atomic.Value
	mu         sync.Mutex
	running    map[string]int
	latency    []time.Duration // per refresh (ten charts)
	age        []time.Duration // per chart delivered: now - ExecutedAt
	poolWait   []time.Duration // waiting for a source connection
	execTime   []time.Duration // source query duration
	staleShown atomic.Int64    // charts served from an entry older than ttl (swr)
	charts     atomic.Int64
}

func main() {
	only := flag.Int("only", 0, "run only scenario N (1-based)")
	flag.Parse()
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: valkey, PoolSize: 256})
	must(rdb.Ping(ctx).Err())

	base := scenario{replicas: 1, mode: "cache", ttl: 4 * time.Second, cost: 200 * time.Millisecond, pool: 8, run: 25 * time.Second}
	with := func(name string, f func(*scenario)) scenario { s := base; s.name = name; f(&s); return s }
	all := []scenario{
		with("no cache, 10 viewers", func(s *scenario) { s.mode, s.viewers = "none", 10 }),
		with("no cache, 100 viewers", func(s *scenario) { s.mode, s.viewers = "none", 100 }),
		with("cache, 10 viewers", func(s *scenario) { s.viewers = 10 }),
		with("cache, 100 viewers", func(s *scenario) { s.viewers = 100 }),
		with("cache, 1000 viewers", func(s *scenario) { s.viewers = 1000 }),
		with("cache, 5000 viewers", func(s *scenario) { s.viewers = 5000 }),
		with("cache, 1000 viewers, 3 replicas, no lock", func(s *scenario) { s.viewers, s.replicas = 1000, 3 }),
		with("cache, 1000 viewers, 3 replicas, lock", func(s *scenario) { s.viewers, s.replicas, s.lock = 1000, 3, true }),
		with("swr, 1000 viewers", func(s *scenario) { s.viewers, s.mode = 1000, "swr" }),
		with("slow source, cache, 1000 viewers", func(s *scenario) { s.viewers, s.cost = 1000, 2*time.Second }),
		with("slow source, swr, 1000 viewers", func(s *scenario) { s.viewers, s.cost, s.mode = 1000, 2*time.Second, "swr" }),
		with("slow source, swr, ttl 30s, 1000 viewers", func(s *scenario) {
			s.viewers, s.cost, s.mode, s.ttl, s.run = 1000, 2*time.Second, "swr", 30*time.Second, 60*time.Second
		}),
	}
	fmt.Printf("reports %d x charts %d x scopes %d = %d distinct queries; poll %v; chart cost %v..%v (slow: x10)\n\n",
		reports, charts, len(scopes), reports*charts*len(scopes), poll, base.cost/5, base.cost)
	fmt.Printf("%-2s %-42s %8s %8s %6s %7s %9s %9s %9s %9s %7s\n", "#", "scenario", "src q/s", "bound", "maxinf", "overlap",
		"lat p50", "lat p95", "age p50", "age p95", "stale%")
	for i, s := range all {
		if *only != 0 && *only != i+1 {
			continue
		}
		must(rdb.FlushDB(ctx).Err())
		st := runScenario(ctx, s, rdb)
		report(i+1, s, st)
	}
}

func runScenario(ctx context.Context, s scenario, rdb *redis.Client) *stats {
	st := &stats{running: map[string]int{}}
	var reps []*replica
	for i := range s.replicas {
		cfg, err := pgxpool.ParseConfig(martDSN)
		must(err)
		cfg.MaxConns = int32(s.pool)
		db, err := pgxpool.NewWithConfig(ctx, cfg)
		must(err)
		defer db.Close()
		reps = append(reps, &replica{id: i, s: s, db: db, rdb: rdb})
	}
	runCtx, cancel := context.WithTimeout(ctx, warmup+s.run)
	defer cancel()
	time.AfterFunc(warmup, func() { st.measuring.Store(true) })
	var wg sync.WaitGroup
	for v := range s.viewers {
		rep := reps[v%len(reps)]
		report, scope := rand.IntN(reports), scopes[rand.IntN(len(scopes))]
		wg.Go(func() {
			// First poll at a random phase, then every poll interval; never overlap a viewer's own polls.
			next := time.Now().Add(time.Duration(rand.Int64N(int64(poll))))
			for {
				select {
				case <-runCtx.Done():
					return
				case <-time.After(time.Until(next)):
				}
				t := time.Now()
				ages := rep.refresh(runCtx, st, report, scope)
				if st.measuring.Load() && runCtx.Err() == nil {
					st.mu.Lock()
					st.latency = append(st.latency, time.Since(t))
					st.age = append(st.age, ages...)
					st.mu.Unlock()
				}
				next = t.Add(poll)
				if next.Before(time.Now()) {
					next = time.Now()
				}
			}
		})
	}
	wg.Wait()
	st.done.Store(true)
	return st
}

// refresh is one viewer poll: the ten charts of a report, fetched concurrently. Returns each
// chart's data age at delivery.
func (r *replica) refresh(ctx context.Context, st *stats, report int, scope string) []time.Duration {
	keys := make([]string, charts)
	for c := range charts {
		keys[c] = fmt.Sprintf("r:%d:%d:%s", report, c, scope)
	}
	var cached []any
	if r.s.mode != "none" {
		var err error
		cached, err = r.rdb.MGet(ctx, keys...).Result() // one round trip for the whole report
		if err != nil {
			st.fail(ctx, err)
			return nil
		}
	}
	ages := make([]time.Duration, charts)
	var wg sync.WaitGroup
	for c := range charts {
		wg.Go(func() {
			var e *entry
			if cached != nil && cached[c] != nil {
				e = decode(cached[c].(string))
			}
			e = r.chart(ctx, st, report, c, scope, keys[c], e)
			if e == nil {
				return // counted by fail, unless the run was ending
			}
			age := time.Since(time.Unix(0, e.ExecutedAt))
			ages[c] = age
			if st.measuring.Load() {
				st.charts.Add(1)
				if age > r.s.ttl+costOf(r.s, c) {
					st.staleShown.Add(1)
				}
			}
		})
	}
	wg.Wait()
	return ages
}

func (r *replica) chart(ctx context.Context, st *stats, report, c int, scope, key string, e *entry) *entry {
	switch r.s.mode {
	case "none":
		e, err := r.execute(ctx, st, report, c, scope, key, false)
		st.fail(ctx, err)
		return e
	case "cache":
		if e != nil {
			return e // Valkey expires entries at ttl
		}
		return r.fill(ctx, st, report, c, scope, key)
	default: // swr
		if e != nil {
			age := time.Since(time.Unix(0, e.ExecutedAt))
			if age > r.s.ttl {
				go r.fill(context.WithoutCancel(ctx), st, report, c, scope, key) // background refresh, deduplicated
			}
			if age < hardTTL {
				return e
			}
		}
		return r.fill(ctx, st, report, c, scope, key)
	}
}

// fill computes a key once per replica at a time (singleflight) and, with the lock, once across
// replicas: a replica that loses the lock waits for the winner's result instead of querying.
func (r *replica) fill(ctx context.Context, st *stats, report, c int, scope, key string) *entry {
	v, err, _ := r.sf.Do(key, func() (any, error) {
		if r.s.lock {
			ok, err := r.rdb.SetNX(ctx, "lock:"+key, r.id, 30*time.Second).Result()
			if err != nil {
				return nil, err
			}
			if !ok {
				return r.waitFor(ctx, key)
			}
			defer r.rdb.Del(context.WithoutCancel(ctx), "lock:"+key)
		}
		return r.execute(ctx, st, report, c, scope, key, true)
	})
	st.fail(ctx, err)
	e, _ := v.(*entry)
	return e
}

func (r *replica) waitFor(ctx context.Context, key string) (*entry, error) {
	start := time.Now()
	for time.Since(start) < 30*time.Second {
		s, err := r.rdb.Get(ctx, key).Result()
		if err == nil {
			if e := decode(s); r.s.mode != "swr" || time.Since(time.Unix(0, e.ExecutedAt)) <= r.s.ttl {
				return e, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(waitPoll):
		}
	}
	return nil, fmt.Errorf("timed out waiting for %s", key)
}

func (r *replica) execute(ctx context.Context, st *stats, report, c int, scope, key string, store bool) (*entry, error) {
	if st.measuring.Load() {
		st.execs.Add(1)
	}
	st.mu.Lock()
	st.running[key]++
	if st.running[key] > 1 && r.s.mode != "none" {
		st.overlap.Add(1)
	}
	st.mu.Unlock()
	n := st.inflight.Add(1)
	for m := st.maxInfl.Load(); n > m && !st.maxInfl.CompareAndSwap(m, n); m = st.maxInfl.Load() {
	}
	defer func() {
		st.inflight.Add(-1)
		st.mu.Lock()
		st.running[key]--
		st.mu.Unlock()
	}()

	waitStart := time.Now()
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	e := &entry{ExecutedAt: time.Now().UnixNano()} // data time: when the query starts, not when it queued
	if st.measuring.Load() {
		st.mu.Lock()
		st.poolWait = append(st.poolWait, time.Since(waitStart))
		st.mu.Unlock()
	}
	defer func() {
		if st.measuring.Load() {
			st.mu.Lock()
			st.execTime = append(st.execTime, time.Since(time.Unix(0, e.ExecutedAt)))
			st.mu.Unlock()
		}
	}()
	// The MATERIALIZED CTE runs pg_sleep exactly once; a plain subquery is re-run per joined row.
	sql := fmt.Sprintf(`WITH cost AS MATERIALIZED (SELECT pg_sleep($2))
		SELECT %d AS report, %d AS chart, r.region_name, sum(l.quantity * l.unit_price)::text AS revenue
		FROM mart.fact_order o
		JOIN mart.dim_customer cu USING (customer_id)
		JOIN mart.dim_region r USING (region_id)
		JOIN mart.fact_order_line l USING (order_id), cost
		WHERE r.region_name = $1
		GROUP BY r.region_name`, report, c)
	rows, err := conn.Query(ctx, sql, scope, costOf(r.s, c).Seconds())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		e.Rows = append(e.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if store {
		b, _ := json.Marshal(e)
		exp := r.s.ttl
		if r.s.mode == "swr" {
			exp = hardTTL
		}
		if err := r.rdb.Set(context.WithoutCancel(ctx), key, b, exp).Err(); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func (st *stats) fail(ctx context.Context, err error) {
	if err != nil && ctx.Err() == nil && !st.done.Load() {
		st.errors.Add(1)
		st.firstErr.CompareAndSwap(nil, err.Error())
	}
}

func costOf(s scenario, c int) time.Duration { return s.cost * time.Duration(c%5+1) / 5 }

func decode(s string) *entry {
	var e entry
	must(json.Unmarshal([]byte(s), &e))
	return &e
}

func report(i int, s scenario, st *stats) {
	secs := s.run.Seconds()
	bound := "-"
	if s.mode != "none" {
		// Each distinct query runs at most once per ttl (plus its own duration) per replica, or
		// once across replicas with the lock.
		k, n := float64(reports*charts*len(scopes)), float64(s.replicas)
		if s.lock {
			n = 1
		}
		bound = fmt.Sprintf("%.1f", k*n/s.ttl.Seconds())
	}
	pct := func(d []time.Duration, p float64) string {
		if len(d) == 0 {
			return "-"
		}
		slices.Sort(d)
		return d[int(p*float64(len(d)-1))].Round(10 * time.Millisecond).String()
	}
	stale := "-"
	if n := st.charts.Load(); n > 0 {
		stale = fmt.Sprintf("%.1f", 100*float64(st.staleShown.Load())/float64(n))
	}
	fmt.Printf("%-2d %-42s %8.1f %8s %6d %7d %9s %9s %9s %9s %7s", i, s.name, float64(st.execs.Load())/secs, bound,
		st.maxInfl.Load(), st.overlap.Load(), pct(st.latency, .5), pct(st.latency, .95), pct(st.age, .5), pct(st.age, .95), stale)
	fmt.Printf("  wait p95 %s exec p50 %s", pct(st.poolWait, .95), pct(st.execTime, .5))
	if e := st.errors.Load(); e > 0 {
		fmt.Printf("  errors %d (first: %v)", e, st.firstErr.Load())
	}
	fmt.Println()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
