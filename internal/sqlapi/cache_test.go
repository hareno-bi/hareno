package sqlapi

import (
	"errors"
	"testing"
	"time"

	"github.com/hareno-bi/hareno/internal/sqlexec"
)

// TestResults: the cache rules of cache.go, with a fake clock and fake queries.
func TestResults(t *testing.T) {
	now := time.Now()
	c := &results{Now: func() time.Time { return now }}
	runs := 0
	var fails error
	run := func() (*response, error) {
		runs++
		return &response{Columns: []string{"n"}, Rows: [][]any{{runs}}}, fails
	}
	get := func(key, mark string) tileResult { return c.get(key, mark, 5*time.Second, run) }
	expect := func(what string, r tileResult, wantRuns int, stale bool) {
		t.Helper()
		if runs != wantRuns || r.Stale != stale || (r.response == nil) != (r.Error != "" && !stale) {
			t.Fatalf("%s: runs %d (want %d), %+v", what, runs, wantRuns, r)
		}
	}
	north := resultKey("mart", sqlexec.Scope{"region": {"North"}}, "SELECT 1")
	all := resultKey("mart", sqlexec.Scope{"region": {"*"}}, "SELECT 1")

	// Without a watermark: current for one interval.
	expect("first", get(north, ""), 1, false)
	expect("within the interval", get(north, ""), 1, false)
	expect("another scope never shares", get(all, ""), 2, false)
	now = now.Add(6 * time.Second)
	expect("after the interval", get(north, ""), 3, false)

	// With a watermark: current until it moves, however old.
	expect("under a watermark", get(north, "m1"), 4, false)
	now = now.Add(time.Hour)
	if r := get(north, "m1"); runs != 4 || r.AsOf != "m1" {
		t.Fatalf("same watermark an hour later: runs %d, %+v", runs, r)
	}
	expect("watermark moved", get(north, "m2"), 5, false)

	// A failed refresh serves the last good result, marked stale, until it is too old.
	fails = errors.New("source down")
	r := get(north, "m3")
	expect("refresh fails", r, 6, true)
	if r.Error != "source down" || r.Rows[0][0] != 5 {
		t.Fatalf("stale result: %+v", r)
	}
	now = now.Add(maxStale + time.Second)
	if r := get(north, "m3"); r.response != nil || r.Error != "source down" {
		t.Fatalf("too old to serve: %+v", r)
	}
	fails = nil

	// The watermark is read at most once per interval.
	reads := 0
	read := func() (string, error) { reads++; return "w", nil }
	c.mark("mart", 5*time.Second, read)
	c.mark("mart", 5*time.Second, read)
	now = now.Add(6 * time.Second)
	if c.mark("mart", 5*time.Second, read); reads != 2 {
		t.Fatalf("watermark reads: %d", reads)
	}
	if m := c.mark("other", 5*time.Second, func() (string, error) { return "x", errors.New("no table") }); m != "" {
		t.Fatalf("unreadable watermark: %q", m)
	}
}
