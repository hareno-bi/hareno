package sqlapi

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestDrill: a chart by year drills into quarters, then months, each total computed at its level,
// under the viewer's row policy and the date range; a text hierarchy drills too; a tile outside
// any hierarchy says it does not drill; the CSV holds the drilled rows.
func TestDrill(t *testing.T) {
	g := newRig(t, 1000)
	call := func(user, method, path string, body any) map[string]any {
		t.Helper()
		st, b := g.call(t, user, method, path, body)
		if st >= 300 {
			t.Fatalf("%s %s %s: %d %v", user, method, path, st, b)
		}
		return b
	}
	tile := func(dims ...string) map[string]any {
		return map[string]any{"title": dims[0], "sql": "", "chart": nil, "show": "table", "wide": false,
			"dataset": map[string]any{"dataset": "sales", "dimensions": dims, "measures": []string{"revenue"}}}
	}
	id := call("bob", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": map[string]any{"title": "Drill",
		"tiles": []any{tile("order_year"), tile("category"), tile("region_name")}}})["id"].(string)
	path := "/api/reports/" + id
	call("bob", "POST", path+"/publish", map[string]any{"version": 1})
	call("bob", "POST", path+"/shares", map[string]any{"member": "m-alice", "name": "alice", "relation": "viewer"})
	g.f.Grant("c-alpha", "connection", "mart", "exporter", "bob")
	g.tick()

	calendar := "[order_year order_quarter order_month order_day]"
	for _, tc := range []struct {
		user  string
		drill map[string]any
		dates any
		tile  int
		rows  string
		at    string // where the tile is: its levels and how far down
	}{
		{"bob", nil, nil, 0, `[["2026",587.75]]`, calendar + " 0"},
		{"bob", map[string]any{"0": []string{"2026"}}, nil, 0, `[["2026-Q1",324],["2026-Q2",263.75]]`, calendar + " 1"},
		{"bob", map[string]any{"0": []string{"2026", "2026-Q2"}}, nil, 0, `[["2026-04",93.75],["2026-05",170]]`, calendar + " 2"},
		{"alice", map[string]any{"0": []string{"2026"}}, nil, 0, `[["2026-Q1",238.5]]`, calendar + " 1"},
		{"bob", map[string]any{"0": []string{"2026"}}, map[string]any{"from": "2026-02-01", "to": "2026-03-31"}, 0, `[["2026-Q1",238]]`, calendar + " 1"},
		{"bob", map[string]any{"1": []string{"Services"}}, nil, 1, `[["Support Plan",300]]`, "[category product_name] 1"},
		{"bob", nil, nil, 2, `[["North",238.5],["South",175.5],["West",173.75]]`, "<nil>"},
	} {
		run := call(tc.user, "POST", path+"/run", map[string]any{"drill": tc.drill, "dates": tc.dates})
		res := run["tiles"].([]any)[tc.tile].(map[string]any)
		at := "<nil>"
		if d, ok := res["drill"].(map[string]any); ok {
			at = fmt.Sprint(d["levels"], " ", d["at"])
		}
		if r := rounded(res["rows"]); r != tc.rows || at != tc.at {
			t.Errorf("%s %v: %s at %s, want %s at %s", tc.user, tc.drill, r, at, tc.rows, tc.at)
		}
	}
	for _, drill := range []map[string]any{{"0": []string{"2026", "2026-Q2", "2026-04", "2026-04-02"}}, {"0": []string{"2026' or true"}}, {"2": []string{"North"}}} {
		run := call("bob", "POST", path+"/run", map[string]any{"drill": drill})
		for i, x := range run["tiles"].([]any) {
			if e, _ := x.(map[string]any)["error"].(string); (e != "") != (fmt.Sprint(i) == fmt.Sprint(firstKey(drill))) {
				t.Errorf("%v: tile %d error %q", drill, i, e)
			}
		}
	}

	r := httptest.NewRequest("GET", path+"/tiles/0/csv?drill="+url.QueryEscape(`["2026"]`), nil)
	r.Header.Set("Authorization", "Bearer bob")
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, r)
	if got := csvRows(w.Body.String()); w.Code != 200 || got != "2026-Q1,324\n2026-Q2,263.75" {
		t.Errorf("csv: %d %q", w.Code, got)
	}
	if st, b := g.call(t, "bob", "POST", path+"/run", map[string]any{"drill": map[string]any{"0": strings.Split(strings.Repeat("x,", 11), ",")}}); st != 400 {
		t.Errorf("eleven values: %d %v", st, b)
	}
}

func firstKey(m map[string]any) string {
	for k := range m {
		return k
	}
	return ""
}
