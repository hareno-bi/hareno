package sqlapi

import (
	"fmt"
	"testing"
)

// TestModelFilters: a filter narrows a dataset tile by any text dimension of its dataset, shown in
// the tile or not; a measure above the filter's grain counts the orders that have a match; a SQL
// tile without the column is left as it is.
func TestModelFilters(t *testing.T) {
	g := newRig(t, 1000)
	call := func(user, method, path string, body any) map[string]any {
		t.Helper()
		st, b := g.call(t, user, method, path, body)
		if st >= 300 {
			t.Fatalf("%s %s %s: %d %v", user, method, path, st, b)
		}
		return b
	}
	tile := func(dims []string, measure string) map[string]any {
		return map[string]any{"title": measure, "sql": "", "chart": nil, "show": "table", "wide": false,
			"dataset": map[string]any{"dataset": "sales", "dimensions": dims, "measures": []string{measure}}}
	}
	sqlTile := map[string]any{"title": "Orders", "sql": "SELECT count(*) AS orders FROM mart.fact_order", "chart": nil, "show": "table", "wide": false}
	id := call("bob", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": map[string]any{"title": "Filters",
		"tiles": []any{tile([]string{"category"}, "revenue"), tile(nil, "revenue"), tile([]string{"region_name"}, "shipping"), sqlTile}}})["id"].(string)
	path := "/api/reports/" + id
	call("bob", "POST", path+"/publish", map[string]any{"version": 1})
	g.tick()

	for _, tc := range []struct {
		filters map[string]any
		want    []string // each tile's rows and the filters applied to it
	}{
		{map[string]any{"region_name": []string{"North"}}, []string{
			`[["Accessories",12.5],["Hardware",126],["Services",100]] [region_name]`, `[[238.5]] [region_name]`,
			`[["North",15.5]] [region_name]`, `[[8]] []`}},
		// shipping is per order: by category, it is the shipping of the orders with a Hardware line
		{map[string]any{"category": []string{"Hardware"}}, []string{
			`[["Hardware",271.5]] [category]`, `[[271.5]] [category]`,
			`[["North",15.5],["South",19.25],["West",8]] [category]`, `[[8]] []`}},
	} {
		run := call("bob", "POST", path+"/run", map[string]any{"filters": tc.filters})
		for i, x := range run["tiles"].([]any) {
			res := x.(map[string]any)
			on, _ := res["filtered"].([]any)
			if got := rounded(res["rows"]) + " " + fmt.Sprint(on); got != tc.want[i] {
				t.Errorf("%v, tile %d: %s, want %s", tc.filters, i, got, tc.want[i])
			}
		}
	}
}
