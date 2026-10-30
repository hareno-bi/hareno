package sqlapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestTextTiles: a text tile publishes and runs no query (its result is null), has nothing to
// download, and a text tile with a query, or with no text, is refused.
func TestTextTiles(t *testing.T) {
	g := newRig(t, 1000)
	call := func(user, method, path string, body any) map[string]any {
		t.Helper()
		st, b := g.call(t, user, method, path, body)
		if st >= 300 {
			t.Fatalf("%s %s %s: %d %v", user, method, path, st, b)
		}
		return b
	}
	text := func(words string) map[string]any {
		return map[string]any{"title": "", "sql": "", "chart": nil, "show": "text", "wide": true, "text": words}
	}
	data := map[string]any{"title": "Regions", "sql": "", "chart": nil, "show": "table", "wide": false,
		"dataset": map[string]any{"dataset": "sales", "dimensions": []string{"region_name"}, "measures": []string{"revenue"}}}
	draft := func(tiles ...any) map[string]any { return map[string]any{"title": "Report", "tiles": tiles} }

	id := call("bob", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": draft(text("## Overview\nRevenue grew."), data)})["id"].(string)
	path := "/api/reports/" + id
	call("bob", "POST", path+"/publish", map[string]any{"version": 1})
	g.tick()
	run := call("bob", "POST", path+"/run", map[string]any{})["tiles"].([]any)
	if run[0] != nil || rounded(run[1].(map[string]any)["rows"]) != `[["North",238.5],["South",175.5],["West",173.75]]` {
		t.Errorf("run: %v", run)
	}
	g.f.Grant("c-alpha", "connection", "mart", "exporter", "bob")
	r := httptest.NewRequest("GET", path+"/tiles/0/csv", nil)
	r.Header.Set("Authorization", "Bearer bob")
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Errorf("csv of a text tile: %d", w.Code)
	}

	withSQL := text("Words")
	withSQL["sql"] = "SELECT 1"
	for name, d := range map[string]map[string]any{
		"text with a query": draft(withSQL),
		"no text":           draft(text("  \n")),
		"too long":          draft(text(strings.Repeat("x", maxText+1))),
		"text on a chart":   draft(map[string]any{"title": "", "sql": "SELECT 1", "chart": nil, "show": "table", "wide": false, "text": "Words"}),
	} {
		if st, b := g.call(t, "bob", "PUT", path, map[string]any{"version": 1, "draft": d}); st != 400 {
			t.Errorf("%s: %d %v", name, st, b)
		}
	}
}
