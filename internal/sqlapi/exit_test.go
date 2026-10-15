package sqlapi

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestExitSuite is the Phase 2 exit suite (PROJECT-PLAN P2-12): the known answers of
// dev/mart/known-answers.sql, end to end through the API, as a dashboard built from the sales
// and customers datasets and a SQL tile; and the row policy (Q17) on view, preview and export.
// Run against a clean mart (local/bin/loader -clean): the loader's extra orders change totals.
func TestExitSuite(t *testing.T) {
	g := newRig(t, 1000)
	call := func(user, method, path string, body any) map[string]any {
		t.Helper()
		st, b := g.call(t, user, method, path, body)
		if st >= 300 {
			t.Fatalf("%s %s %s: %d %v", user, method, path, st, b)
		}
		return b
	}
	ds := func(dataset string, dims []string, measures ...string) map[string]any {
		return map[string]any{"dataset": dataset, "dimensions": dims, "measures": measures}
	}
	sales := func(dims []string, measures ...string) map[string]any { return ds("sales", dims, measures...) }
	byRegionCategory := sales([]string{"region_name", "category"}, "revenue")
	tiles := []struct {
		name   string
		query  map[string]any // a dataset tile's picks, or nil for sql
		sql    string
		matrix map[string]any
		all    string // bob: every region
		north  string // alice: North only (Q17)
	}{
		{name: "Q01 totals", query: sales(nil, "revenue", "order_count", "customer_count", "line_count"),
			all: `[[587.75,7,4,13]]`, north: `[[238.5,3,2,6]]`},
		{name: "Q03 Q05 by region", query: sales([]string{"region_name"}, "revenue", "shipping"),
			all: `[["North",238.5,15.5],["South",175.5,19.25],["West",173.75,8]]`, north: `[["North",238.5,15.5]]`},
		{name: "Q08 Q09 Q10 by category", query: sales([]string{"category"}, "revenue", "customer_count", "margin_pct"),
			all:   `[["Accessories",16.25,2,0.6],["Hardware",271.5,4,0.3444],["Services",300,3,1]]`,
			north: `[["Accessories",12.5,1,0.6],["Hardware",126,2,0.3175],["Services",100,1,1]]`},
		{name: "Q07 by segment, null kept", query: sales([]string{"segment"}, "revenue"),
			all: `[["Enterprise",186],["SMB",226.25],[null,175.5]]`, north: `[["Enterprise",186],["SMB",52.5]]`},
		{name: "Q12 by month in Hong Kong time", query: sales([]string{"order_month"}, "revenue"),
			all:   `[["2026-01",86],["2026-02",152.5],["2026-03",85.5],["2026-04",93.75],["2026-05",170]]`,
			north: `[["2026-01",86],["2026-02",152.5]]`},
		{name: "Q04 Q06 Q10 Q11 Q16 order-grain totals", query: sales(nil, "shipping", "avg_shipping", "profit", "margin_pct", "discount_amount", "aov"),
			all: `[[42.75,7.125,403.25,0.6861,28.5,83.9643]]`, north: `[[15.5,5.1667,147.5,0.6184,4,79.5]]`},
		{name: "Q15 matrix", query: byRegionCategory,
			matrix: map[string]any{"rows": []string{"region_name", "category"}, "values": []any{map[string]any{"column": "revenue", "agg": ""}}},
			all: `[[0,"North","Accessories",12.5],[0,"North","Hardware",126],[0,"North","Services",100],[0,"South","Hardware",95.5],[0,"South","Services",80],` +
				`[0,"West","Accessories",3.75],[0,"West","Hardware",50],[0,"West","Services",120],[1,"North",null,238.5],[1,"South",null,175.5],[1,"West",null,173.75],[3,null,null,587.75]]`,
			north: `[[0,"North","Accessories",12.5],[0,"North","Hardware",126],[0,"North","Services",100],[1,"North",null,238.5],[3,null,null,238.5]]`},
		{name: "Q16 average order value by customer", query: ds("customer_orders", []string{"customer_name"}, "aov"),
			all:   `[["Acme",93],["Birch",52.5],["Cobalt",87.75],["Delta",86.875],["Ember",null]]`,
			north: `[["Acme",93],["Birch",52.5]]`},
		{name: "Q03 as a SQL tile", sql: `SELECT r.region_name, sum(l.quantity * l.unit_price * (1 - coalesce(l.discount, 0))) AS revenue
FROM mart.fact_order_line l JOIN mart.fact_order o USING (order_id) JOIN mart.dim_customer c USING (customer_id) JOIN mart.dim_region r USING (region_id)
WHERE o.status = 'complete' GROUP BY 1 ORDER BY 1`,
			all: `[["North",238.5],["South",175.5],["West",173.75]]`, north: `[["North",238.5]]`},
	}

	// bob builds and publishes the dashboard, and shares it with alice, who sees her own rows.
	var draft []any
	for _, tc := range tiles {
		show := "table"
		if tc.matrix != nil {
			show = "matrix"
		}
		tile := map[string]any{"title": tc.name, "sql": tc.sql, "chart": nil, "show": show, "wide": true, "matrix": tc.matrix}
		if tc.query != nil {
			tile["dataset"] = tc.query
		}
		draft = append(draft, tile)
	}
	id := call("bob", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": map[string]any{"title": "Known answers", "tiles": draft}})["id"].(string)
	path := "/api/reports/" + id
	call("bob", "POST", path+"/publish", map[string]any{"version": 1})
	call("bob", "POST", path+"/shares", map[string]any{"member": "m-alice", "name": "alice", "relation": "viewer"})
	g.f.Grant("c-alpha", "connection", "mart", "exporter", "alice")
	g.f.Grant("c-alpha", "connection", "mart", "exporter", "bob")
	g.tick()

	for _, u := range []struct{ user, want string }{{"bob", "all"}, {"alice", "north"}} {
		view := call(u.user, "POST", path+"/run", nil)["tiles"].([]any)
		for i, tc := range tiles {
			want := tc.all
			if u.want == "north" {
				want = tc.north
			}
			got := view[i].(map[string]any)
			if r := rounded(got["rows"]); r != want {
				t.Errorf("view, %s, %s: %s %v, want %s", u.user, tc.name, r, got["error"], want)
			}
			// Preview: the same tile, as its editor runs it.
			preview := call(u.user, "POST", "/api/sql", map[string]any{"connection": "mart", "sql": tc.sql, "dataset": tc.query, "matrix": tc.matrix})
			if r := rounded(preview["rows"]); r != want {
				t.Errorf("preview, %s, %s: %s, want %s", u.user, tc.name, r, want)
			}
			// Export: the rows the tile shows (a matrix tile's: those it totals), as text.
			if tc.matrix != nil {
				continue
			}
			r := httptest.NewRequest("GET", fmt.Sprintf("%s/tiles/%d/csv", path, i), nil)
			r.Header.Set("Authorization", "Bearer "+u.user)
			w := httptest.NewRecorder()
			g.mux.ServeHTTP(w, r)
			if got, want := csvRows(w.Body.String()), csvOf(want); w.Code != 200 || got != want {
				t.Errorf("export, %s, %s: %d %q, want %q", u.user, tc.name, w.Code, got, want)
			}
		}
	}

	// Q04: shipping by product category has no right answer; it is refused, not computed.
	st, b := g.call(t, "bob", "POST", "/api/sql", map[string]any{"connection": "mart", "dataset": sales([]string{"category"}, "shipping")})
	if st != 400 || !strings.Contains(fmt.Sprint(b["error"]), "grain") {
		t.Errorf("shipping by category: %d %v", st, b)
	}
	// Q17 on filters: alice asking for another region's rows gets none, not that region.
	filtered := call("alice", "POST", path+"/run", map[string]any{"filters": map[string][]string{"region_name": {"South"}}})["tiles"].([]any)
	if r := rounded(filtered[1].(map[string]any)["rows"]); r != `[]` {
		t.Errorf("alice filtered to South: %s", r)
	}
}

// rounded is rows as JSON, numbers to 4 decimals (the known answers' precision).
func rounded(rows any) string {
	rs, _ := rows.([]any)
	out := make([][]any, len(rs))
	for i, r := range rs {
		for _, v := range r.([]any) {
			if f, ok := v.(float64); ok {
				v = math.Round(f*1e4) / 1e4
			}
			out[i] = append(out[i], v)
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// csvOf is the expected JSON rows as CSV data lines, csvRows a download's data lines, both
// with numbers to 4 decimals.
func csvOf(rows string) string {
	var rs [][]any
	json.Unmarshal([]byte(rows), &rs)
	lines := make([]string, len(rs))
	for i, r := range rs {
		cells := make([]string, len(r))
		for j, v := range r {
			if v != nil {
				cells[j] = fmt.Sprint(v)
			}
		}
		lines[i] = strings.Join(cells, ",")
	}
	return strings.Join(lines, "\n")
}

func csvRows(body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")[1:] // after the header
	for i, l := range lines {
		cells := strings.Split(l, ",")
		for j, c := range cells {
			if f, err := strconv.ParseFloat(c, 64); err == nil {
				cells[j] = fmt.Sprint(math.Round(f*1e4) / 1e4)
			}
		}
		lines[i] = strings.Join(cells, ",")
	}
	return strings.Join(lines, "\n")
}
