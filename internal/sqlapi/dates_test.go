package sqlapi

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestDatesSpan: each preset counted back from today, today included; from and to, both included.
func TestDatesSpan(t *testing.T) {
	hk, _ := time.LoadLocation("Asia/Hong_Kong")
	today := time.Date(2026, 10, 21, 0, 0, 0, 0, hk)
	day := func(t time.Time) string { return t.Format(time.DateOnly) }
	for _, tc := range []struct {
		d        Dates
		from, to string
	}{
		{Dates{Last: "7d"}, "2026-10-15", "2026-10-22"},
		{Dates{Last: "30d"}, "2026-09-22", "2026-10-22"},
		{Dates{Last: "3m"}, "2026-07-22", "2026-10-22"},
		{Dates{Last: "12m"}, "2025-10-22", "2026-10-22"},
		{Dates{Last: "year"}, "2026-01-01", "2026-10-22"},
		{Dates{Last: "lastyear"}, "2025-01-01", "2026-01-01"},
		{Dates{From: "2026-02-01", To: "2026-03-31"}, "2026-02-01", "2026-04-01"},
	} {
		if !tc.d.valid() {
			t.Errorf("%+v: not valid", tc.d)
		}
		if from, to := tc.d.span(today); day(from) != tc.from || day(to) != tc.to {
			t.Errorf("%+v: %s to %s, want %s to %s", tc.d, day(from), day(to), tc.from, tc.to)
		}
	}
	for _, d := range []Dates{{Last: "5y"}, {From: "2026-02-01"}, {From: "2026-03-01", To: "2026-02-01"}, {Last: "7d", From: "2026-01-01", To: "2026-01-02"}, {From: "1 Feb", To: "2026-03-01"}} {
		if d.valid() {
			t.Errorf("%+v: valid", d)
		}
	}
}

// TestDateRange: a date range narrows a dataset tile with no date column (revenue by region),
// on the dataset's date in Hong Kong time, before the measures; the row policy still applies;
// a SQL tile is left as it is and says so; the CSV holds the same rows.
// Order 105 was placed on 31 March in UTC but 1 April in Hong Kong: February to March leaves it out.
func TestDateRange(t *testing.T) {
	g := newRig(t, 1000)
	call := func(user, method, path string, body any) map[string]any {
		t.Helper()
		st, b := g.call(t, user, method, path, body)
		if st >= 300 {
			t.Fatalf("%s %s %s: %d %v", user, method, path, st, b)
		}
		return b
	}
	byRegion := map[string]any{"title": "Revenue by region", "sql": "", "chart": nil, "show": "table", "wide": false,
		"dataset": map[string]any{"dataset": "sales", "dimensions": []string{"region_name"}, "measures": []string{"revenue"}}}
	sqlTile := map[string]any{"title": "Orders", "sql": "SELECT count(*) AS orders FROM mart.fact_order", "chart": nil, "show": "table", "wide": false}
	id := call("bob", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": map[string]any{"title": "Dates", "tiles": []any{byRegion, sqlTile}}})["id"].(string)
	path := "/api/reports/" + id
	call("bob", "POST", path+"/publish", map[string]any{"version": 1})
	call("bob", "POST", path+"/shares", map[string]any{"member": "m-alice", "name": "alice", "relation": "viewer"})
	g.f.Grant("c-alpha", "connection", "mart", "exporter", "bob")
	g.tick()

	febMar := map[string]any{"from": "2026-02-01", "to": "2026-03-31"}
	for _, tc := range []struct {
		user  string
		dates any
		want  string
	}{
		{"bob", nil, `[["North",238.5],["South",175.5],["West",173.75]]`},
		{"bob", febMar, `[["North",152.5],["South",85.5]]`},
		{"alice", febMar, `[["North",152.5]]`},
		{"bob", map[string]any{"last": "30d"}, `[]`}, // the test orders are from early 2026
	} {
		run := call(tc.user, "POST", path+"/run", map[string]any{"dates": tc.dates})
		tiles := run["tiles"].([]any)
		region, orders := tiles[0].(map[string]any), tiles[1].(map[string]any)
		if r := rounded(region["rows"]); r != tc.want || run["dates"] != true {
			t.Errorf("%s %v: %s (dates offered: %v), want %s", tc.user, tc.dates, r, run["dates"], tc.want)
		}
		if (region["dated"] == true) != (tc.dates != nil) || orders["dated"] != nil {
			t.Errorf("%s %v: dated %v and %v", tc.user, tc.dates, region["dated"], orders["dated"])
		}
	}

	r := httptest.NewRequest("GET", path+`/tiles/0/csv?dates={"from":"2026-02-01","to":"2026-03-31"}`, nil)
	r.Header.Set("Authorization", "Bearer bob")
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, r)
	if got := csvRows(w.Body.String()); w.Code != 200 || got != "North,152.5\nSouth,85.5" {
		t.Errorf("csv: %d %q", w.Code, got)
	}
	if st, b := g.call(t, "bob", "POST", path+"/run", map[string]any{"dates": map[string]any{"last": "5y"}}); st != 400 || !strings.Contains(fmt.Sprint(b["error"]), "dates") {
		t.Errorf("unknown span: %d %v", st, b)
	}
}
