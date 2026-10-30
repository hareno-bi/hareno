package sqlapi

import "testing"

// TestSampleReport: the dashboard titled as the sample report is readable without signing in,
// once published, with every row, read only; no other dashboard is reachable that way.
func TestSampleReport(t *testing.T) {
	g := newRig(t, 1000)
	tile := map[string]any{"title": "Regions", "sql": "", "chart": nil, "show": "table", "wide": false,
		"dataset": map[string]any{"dataset": "sales", "dimensions": []string{"region_name"}, "measures": []string{"revenue"}}}
	create := func(title string) string {
		st, b := g.call(t, "alice", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": map[string]any{"title": title, "tiles": []any{tile}}})
		if st != 201 && st != 200 {
			t.Fatalf("create %s: %d %v", title, st, b)
		}
		return b["id"].(string)
	}
	other := create("Other")
	g.call(t, "alice", "POST", "/api/reports/"+other+"/publish", map[string]any{"version": 1})
	sample := create("Sample")
	if st, _ := g.call(t, "", "GET", "/api/sample-report", nil); st != 404 {
		t.Errorf("unpublished: %d", st)
	}
	if st, b := g.call(t, "alice", "POST", "/api/reports/"+sample+"/publish", map[string]any{"version": 1}); st != 200 {
		t.Fatalf("publish: %d %v", st, b)
	}
	st, b := g.call(t, "", "GET", "/api/sample-report", nil)
	if st != 200 || b["canEdit"] != false || b["canExport"] != false || b["published"].(map[string]any)["body"].(map[string]any)["title"] != "Sample" {
		t.Errorf("view: %d %v", st, b)
	}
	// alice sees North only; anyone, signed out, sees every region of the sample
	st, b = g.call(t, "", "POST", "/api/sample-report/run", map[string]any{})
	if st != 200 || b["data"] != "sample" || rounded(b["tiles"].([]any)[0].(map[string]any)["rows"]) != `[["North",238.5],["South",175.5],["West",173.75]]` {
		t.Errorf("run: %d %v", st, b)
	}
	if st, _ := g.call(t, "", "POST", "/api/reports/"+other+"/run", map[string]any{}); st != 401 {
		t.Errorf("another dashboard, signed out: %d", st)
	}
}
