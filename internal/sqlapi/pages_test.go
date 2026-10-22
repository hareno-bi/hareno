package sqlapi

import (
	"testing"
)

// TestPages: a dashboard of three pages runs only the page shown, and a filter set on one page
// narrows the tiles of the page shown; drafts with one page, an unknown page or a page named twice are refused.
func TestPages(t *testing.T) {
	g := newRig(t, 1000)
	call := func(user, method, path string, body any) map[string]any {
		t.Helper()
		st, b := g.call(t, user, method, path, body)
		if st >= 300 {
			t.Fatalf("%s %s %s: %d %v", user, method, path, st, b)
		}
		return b
	}
	tile := func(page int, dim string) map[string]any {
		return map[string]any{"title": dim, "sql": "", "chart": nil, "show": "table", "wide": false, "page": page,
			"dataset": map[string]any{"dataset": "sales", "dimensions": []string{dim}, "measures": []string{"revenue"}}}
	}
	draft := func(pages []string, tiles ...any) map[string]any {
		return map[string]any{"title": "Pages", "pages": pages, "tiles": tiles}
	}
	pages := []string{"Overview", "Regions", "Products"}
	id := call("bob", "POST", "/api/reports", map[string]any{"connection": "mart",
		"draft": draft(pages, tile(0, "order_year"), tile(1, "region_name"), tile(2, "category"), tile(1, "segment"))})["id"].(string)
	path := "/api/reports/" + id
	call("bob", "POST", path+"/publish", map[string]any{"version": 1})
	g.tick()

	for _, tc := range []struct {
		page    int
		filters any
		want    []string // each tile's rows, "-" where it did not run
	}{
		{0, nil, []string{`[["2026",587.75]]`, "-", "-", "-"}},
		{1, nil, []string{"-", `[["North",238.5],["South",175.5],["West",173.75]]`, "-", `[["Enterprise",186],["SMB",226.25],[null,175.5]]`}},
		{2, map[string]any{"category": []string{"Services"}}, []string{"-", "-", `[["Services",300]]`, "-"}},
	} {
		run := call("bob", "POST", path+"/run", map[string]any{"page": tc.page, "filters": tc.filters})
		for i, x := range run["tiles"].([]any) {
			got := "-"
			if x != nil {
				got = rounded(x.(map[string]any)["rows"])
			}
			if got != tc.want[i] {
				t.Errorf("page %d %v, tile %d: %s, want %s", tc.page, tc.filters, i, got, tc.want[i])
			}
		}
	}
	for name, d := range map[string]map[string]any{
		"one page":     draft([]string{"Only"}, tile(0, "order_year")),
		"unknown page": draft([]string{"A", "B"}, tile(2, "order_year")),
		"no pages, p1": draft(nil, tile(1, "order_year")),
		"named twice":  draft([]string{"A", "A"}, tile(0, "order_year")),
		"unnamed":      draft([]string{"A", " "}, tile(0, "order_year")),
	} {
		if st, b := g.call(t, "bob", "PUT", path, map[string]any{"version": 1, "draft": d}); st != 400 {
			t.Errorf("%s: %d %v", name, st, b)
		}
	}
}
