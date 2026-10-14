package sqlapi

import (
	"encoding/json"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/hareno-bi/hareno/internal/sqlexec"
)

// Dashboard results cache (R1-5; ARCHITECTURE-PLAN §8.2). Dashboards poll every five seconds; the
// cache keeps the source from running each tile's query for every viewer on every poll.
//
//   - A result is keyed by connection, row scope and SQL: people with the same row scope share
//     it, people with different scopes never do. Access is checked before the cache is asked.
//   - Where the connection has a load watermark, a result stays current until the watermark
//     moves. The watermark is read at most once per refresh interval per connection.
//     Without a watermark, a result is current for one refresh interval.
//   - The same query running at the same time runs once.
//   - If a refresh fails, the last good result is served, marked stale, up to maxStale old.
//
// ponytail: in process, so replicas do not share results or watermark reads; move both to Valkey
// (compose.yaml) when Hareno runs more than one replica. Entries live until maxStale, and the
// whole cache is dropped past maxEntries.
const (
	maxStale   = 10 * time.Minute
	maxEntries = 10_000
)

type results struct {
	Now func() time.Time // for tests; defaults to time.Now

	mu      sync.Mutex
	entries map[string]cachedResult
	marks   map[string]cachedMark
	runs    singleflight.Group
}

type cachedResult struct {
	res  *response
	mark string // the watermark it was computed under, "" without one
	at   time.Time
}

type cachedMark struct {
	mark string
	at   time.Time
}

// tileResult is one tile's result as a viewer gets it.
type tileResult struct {
	*response
	RanAt time.Time `json:"ranAt"`           // when the query ran
	AsOf  string    `json:"asOf,omitempty"`  // the source's load watermark it ran under
	Stale bool      `json:"stale,omitempty"` // a refresh failed: this is the last good result
	Error string    `json:"error,omitempty"` // why the refresh failed, or why there is no result
}

func (c *results) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func resultKey(conn string, scope sqlexec.Scope, sql string) string {
	s, _ := json.Marshal(scope) // map keys sorted
	return conn + "\x00" + string(s) + "\x00" + sql
}

// mark is the connection's load watermark, read with read at most once per interval. "" when
// the connection has none or it cannot be read: results then expire by age.
func (c *results) mark(conn string, interval time.Duration, read func() (string, error)) string {
	c.mu.Lock()
	m, ok := c.marks[conn]
	c.mu.Unlock()
	if ok && c.now().Sub(m.at) < interval {
		return m.mark
	}
	v, _, _ := c.runs.Do("mark\x00"+conn, func() (any, error) {
		mark, err := read()
		if err != nil {
			mark = ""
		}
		c.mu.Lock()
		if c.marks == nil {
			c.marks = map[string]cachedMark{}
		}
		c.marks[conn] = cachedMark{mark, c.now()}
		c.mu.Unlock()
		return mark, nil
	})
	return v.(string)
}

// get returns the result for key: the cached one while it is current, else run's.
func (c *results) get(key, mark string, interval time.Duration, run func() (*response, error)) tileResult {
	now := c.now()
	c.mu.Lock()
	e, ok := c.entries[key]
	c.mu.Unlock()
	if ok && (mark != "" && e.mark == mark || mark == "" && e.mark == "" && now.Sub(e.at) < interval) {
		return tileResult{response: e.res, RanAt: e.at, AsOf: e.mark}
	}
	v, err, _ := c.runs.Do(key, func() (any, error) {
		res, err := run()
		if err != nil {
			return nil, err
		}
		e := cachedResult{res, mark, c.now()}
		c.mu.Lock()
		if c.entries == nil || len(c.entries) >= maxEntries {
			c.entries = map[string]cachedResult{}
		}
		c.entries[key] = e
		for k, old := range c.entries { // ponytail: a sweep per store; a heap when entries are many
			if e.at.Sub(old.at) > maxStale {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
		return e, nil
	})
	if err != nil {
		if ok && now.Sub(e.at) <= maxStale {
			return tileResult{response: e.res, RanAt: e.at, AsOf: e.mark, Stale: true, Error: err.Error()}
		}
		return tileResult{Error: err.Error()}
	}
	e = v.(cachedResult)
	return tileResult{response: e.res, RanAt: e.at, AsOf: e.mark}
}
