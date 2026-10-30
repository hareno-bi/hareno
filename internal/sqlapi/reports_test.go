package sqlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rosschiu/kiban/sdk"

	"github.com/hareno-bi/hareno/internal/appdb"
	"github.com/hareno-bi/hareno/internal/appdb/appdbtest"
	"github.com/hareno-bi/hareno/internal/authz"
	"github.com/hareno-bi/hareno/internal/compiler"
	"github.com/hareno-bi/hareno/internal/compiler/compilertest"
	"github.com/hareno-bi/hareno/internal/model"
	"github.com/hareno-bi/hareno/internal/sqlexec"
)

// grants writes tuples for users, and for members through their linked login, into the fake
// Kiban, as Kiban resolves them.
type grants struct {
	f       *authz.Fake
	members map[string]string // member id -> subject
}

func (g grants) subject(t sdk.Tuple) string {
	switch {
	case t.SubjectType == "user":
		return t.SubjectID
	case t.SubjectType == "member" && t.SubjectRelation == "mapped_user":
		return g.members[t.SubjectID]
	}
	return ""
}

func (g grants) Grant(_ context.Context, company string, ts ...sdk.Tuple) error {
	for _, t := range ts {
		if sub := g.subject(t); sub != "" {
			g.f.Grant(company, t.ObjectType, t.ObjectID, t.Relation, sub)
		}
	}
	return nil
}

func (g grants) Revoke(_ context.Context, company string, ts ...sdk.Tuple) error {
	for _, t := range ts {
		if sub := g.subject(t); sub != "" {
			g.f.Revoke(company, t.ObjectType, t.ObjectID, t.Relation, sub)
		}
	}
	return nil
}

// MemberBySubject answers from the same member map, in any company.
func (g grants) MemberBySubject(_ context.Context, _, sub string) (sdk.MemberFact, error) {
	for m, s := range g.members {
		if s == sub {
			return sdk.MemberFact{IsMember: true, IsActive: true, MemberID: &m}, nil
		}
	}
	return sdk.MemberFact{}, nil
}

// directory is a fake member directory: company id -> entries, matched by name substring.
type directory map[string][]sdk.DirectoryEntry

func (d directory) Search(_ context.Context, _, company, q string) ([]sdk.DirectoryEntry, error) {
	var out []sdk.DirectoryEntry
	for _, e := range d[company] {
		if strings.Contains(e.DisplayName, q) {
			out = append(out, e)
		}
	}
	return out, nil
}

// rig is the reports API over the dev mart and the compile sidecar, with fake Kiban users:
// alice holds North and sql_user, bob all regions and sql_user, mallory North only, dan South
// and dataset_user, erin nothing; carol is in another company. exportRows caps downloads.
type rig struct {
	f     *authz.Fake
	kiban grants
	mux   *http.ServeMux
	tick  func() // past the decision cache after a grant changes
}

func newRig(t *testing.T, exportRows int) *rig {
	appDB := appdbtest.New(t)
	mart, err := pgxpool.New(context.Background(), "postgres://hareno_gate:hareno_gate@localhost:55433/mart")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mart.Close)
	connID, err := appdb.Connection(context.Background(), appDB, "mart", "ALPHA", "database-enforced")
	if err != nil {
		t.Fatal(err)
	}

	alpha := []sdk.Company{{ID: "c-alpha", Code: "ALPHA", IsActive: true}}
	f := &authz.Fake{Companies: map[string][]sdk.Company{
		"alice": alpha, "bob": alpha, "mallory": alpha, "erin": alpha, "dan": alpha, "carol": {{ID: "c-beta", Code: "BETA", IsActive: true}},
	}}
	f.Grant("c-alpha", "connection", "mart", "sql_user", "alice")
	f.Grant("c-alpha", "connection", "mart", "sql_user", "bob")
	f.Grant("c-alpha", "data_scope", "mart.region.North", "viewer", "alice")
	f.Grant("c-alpha", "data_scope", "mart.region.*", "viewer", "bob")
	f.Grant("c-alpha", "data_scope", "mart.region.North", "viewer", "mallory")
	f.Grant("c-alpha", "connection", "mart", "data_sharer", "alice") // erin has no access of her own
	f.Grant("c-alpha", "connection", "mart", "dataset_user", "dan")  // dan builds from datasets, without SQL
	f.Grant("c-alpha", "data_scope", "mart.region.South", "viewer", "dan")

	kiban := grants{f, map[string]string{"m-alice": "alice", "m-bob": "bob", "m-mallory": "mallory", "m-erin": "erin", "m-carol": "carol"}}
	now := time.Now()
	tick := func() { now = now.Add(time.Minute) } // past the decision cache after a grant changes
	h := &Handler{
		Authz: &authz.Authz{K: f, Module: "hareno", Feature: "hareno.use", AllowTTL: 30 * time.Second, DenyTTL: 5 * time.Second,
			Now: func() time.Time { return now }},
		Connections: map[string]Connection{"mart": {
			Company: "ALPHA", ID: connID,
			Exec:       sqlexec.Exec{DB: mart, Role: "hareno_sql", Timeout: 5 * time.Second, MaxRows: 1000},
			Scope:      map[string][]string{"region": {"North", "South", "West"}},
			ExportRows: exportRows, ExportTimeout: 5 * time.Second,
		}},
		Datasets: &Datasets{Compiler: &compiler.Client{URL: compilertest.Sidecar(t)}, ByName: datasets(t, "sales", "customers")},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:       appDB,
		Grants:   kiban,
		Members:  kiban,
		People: directory{
			"c-alpha": {
				{ID: "m-alice", DisplayName: "alice", Kind: "person", HasLinkedUser: true},
				{ID: "m-bob", DisplayName: "bob", Kind: "person", HasLinkedUser: true},
				{ID: "m-mallory", DisplayName: "mallory", Kind: "person", HasLinkedUser: true},
				{ID: "m-erin", DisplayName: "erin", Kind: "person", HasLinkedUser: true},
				{ID: "m-bobby", DisplayName: "bobby (no login)", Kind: "person"},
				{ID: "m-bot", DisplayName: "bob-bot", Kind: "service", HasLinkedUser: true},
			},
			"c-beta": {{ID: "m-carol", DisplayName: "carol", Kind: "person", HasLinkedUser: true}},
		},
	}
	mux := http.NewServeMux()
	mux.Handle("POST /api/sql", h)
	mux.HandleFunc("GET /api/datasets", h.ListDatasets)
	mux.HandleFunc("GET /api/reports", h.ListReports)
	mux.HandleFunc("POST /api/reports", h.CreateReport)
	mux.HandleFunc("GET /api/reports/{id}", h.GetReport)
	mux.HandleFunc("PUT /api/reports/{id}", h.SaveReport)
	mux.HandleFunc("DELETE /api/reports/{id}", h.DeleteReport)
	mux.HandleFunc("POST /api/reports/{id}/publish", h.PublishReport)
	mux.HandleFunc("POST /api/reports/{id}/run", h.RunReport)
	mux.HandleFunc("GET /api/reports/{id}/tiles/{n}/csv", h.ExportTile)
	mux.HandleFunc("GET /api/reports/{id}/people", h.SearchPeople)
	mux.HandleFunc("GET /api/reports/{id}/shares", h.ListShares)
	mux.HandleFunc("POST /api/reports/{id}/shares", h.Share)
	mux.HandleFunc("DELETE /api/reports/{id}/shares/{member}", h.Unshare)
	h.SampleReport = "Sample"
	mux.HandleFunc("GET /api/sample-report", h.SampleReportView)
	mux.HandleFunc("POST /api/sample-report/run", h.SampleReportRun)

	return &rig{f, kiban, mux, tick}
}

// call sends a JSON request as user and decodes the JSON reply.
func (g *rig) call(t *testing.T, user, method, path string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+user)
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, r)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// TestReports: create, save with version checks, publish, run as viewers with their own row
// scope, list, and delete; with the permission rules of reports.go.
func TestReports(t *testing.T) {
	g := newRig(t, 2)
	f, kiban, mux, tick := g.f, g.kiban, g.mux, g.tick
	call := func(user, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return g.call(t, user, method, path, body)
	}
	expect := func(what string, got, want int, body map[string]any) {
		t.Helper()
		if got != want {
			t.Fatalf("%s: %d %v, want %d", what, got, body, want)
		}
	}
	const regions = "SELECT region_name FROM mart.dim_region ORDER BY 1"
	draft := func(title string, sqls ...string) map[string]any {
		var tiles []any
		for _, q := range sqls {
			tiles = append(tiles, map[string]any{"title": "", "sql": q, "chart": map[string]any{"kind": "bar", "x": "region_name"}, "show": "chart", "wide": true})
		}
		return map[string]any{"title": title, "tiles": tiles}
	}
	// rows are the first tile's rows of a run
	rows := func(body map[string]any) string {
		tiles, _ := body["tiles"].([]any)
		if len(tiles) == 0 {
			return fmt.Sprint(body)
		}
		b, _ := json.Marshal(tiles[0].(map[string]any)["rows"])
		return string(b)
	}

	st, b := call("mallory", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": draft("x", regions)})
	expect("create without sql_user", st, 403, b)
	st, b = call("carol", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": draft("x", regions)})
	expect("create on another company's connection", st, 404, b)
	st, b = call("alice", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": draft("", regions)})
	expect("create without a title", st, 400, b)
	st, b = call("alice", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": draft("No tiles")})
	expect("create without tiles", st, 400, b)

	st, b = call("alice", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": draft("Regions", regions)})
	expect("create", st, 201, b)
	id := b["id"].(string)
	path := "/api/reports/" + id

	st, b = call("bob", "GET", path, nil)
	expect("bob before sharing", st, 404, b)
	st, b = call("alice", "POST", path+"/run", nil)
	expect("run before publishing", st, 404, b)

	st, b = call("alice", "PUT", path, map[string]any{"draft": draft("Regions v2", regions), "version": 1})
	expect("save", st, 200, b)
	st, b = call("alice", "PUT", path, map[string]any{"draft": draft("Stale", regions), "version": 1})
	expect("save from an old version", st, 409, b)

	bad := draft("Filters", regions)
	bad["filters"] = []string{"region_name", "region_name"}
	st, b = call("alice", "PUT", path, map[string]any{"draft": bad, "version": 2})
	expect("save with a filter listed twice", st, 400, b)
	st, b = call("alice", "PUT", path, map[string]any{"draft": draft("Broken", regions, "SELECT nope FROM nowhere"), "version": 2})
	expect("save broken SQL (drafts may be broken)", st, 200, b)
	st, b = call("alice", "POST", path+"/publish", map[string]any{"version": 3})
	expect("publish broken SQL", st, 400, b)
	if !strings.HasPrefix(b["error"].(string), "not published: tile 2: ") {
		t.Errorf("publish error: %v", b)
	}
	st, b = call("alice", "PUT", path, map[string]any{"draft": draft("Regions", regions, "SELECT count(*) AS n FROM mart.dim_region"), "version": 3})
	expect("fix", st, 200, b)
	st, b = call("alice", "POST", path+"/publish", map[string]any{"version": 4})
	expect("publish", st, 200, b)
	if b["revision"].(float64) != 1 {
		t.Errorf("revision: %v", b)
	}

	// Sharing: only people with a login in the report's company, picked from the directory.
	st, b = call("alice", "GET", path+"/people?q=bob", nil)
	expect("search people", st, 200, b)
	if got, _ := json.Marshal(b["people"]); string(got) != `[{"email":"","id":"m-bob","name":"bob"}]` {
		t.Errorf("people matching bob: %s", got)
	}
	share := func(by, member, name, relation string) (int, map[string]any) {
		return call(by, "POST", path+"/shares", map[string]any{"member": member, "name": name, "relation": relation})
	}
	st, b = share("alice", "m-carol", "carol", "viewer")
	expect("share with another company's member", st, 400, b)
	st, b = share("alice", "m-bobby", "bobby", "viewer")
	expect("share with a member without a login", st, 400, b)
	st, b = share("alice", "m-bot", "bob", "viewer")
	expect("share with a service account", st, 400, b)
	st, b = share("alice", "m-bob", "bob", "owner")
	expect("share with an unknown relation", st, 400, b)

	// Shared with bob as viewer: he sees all regions, alice North only, from the same revision.
	st, b = share("alice", "m-bob", "bob", "viewer")
	expect("share with bob", st, 200, b)
	st, b = share("alice", "m-mallory", "mall", "viewer")
	expect("share with mallory", st, 200, b)
	st, b = call("alice", "GET", path+"/shares", nil)
	if got, _ := json.Marshal(b["shares"]); !strings.Contains(string(got), `"member":"m-bob","name":"bob","relation":"viewer"`) ||
		!strings.Contains(string(got), `"name":"mallory"`) {
		t.Errorf("shares: %s", got)
	}
	tick() // bob's earlier denial is cached for up to 5 s
	st, b = call("bob", "GET", path+"/shares", nil)
	expect("viewer lists shares", st, 403, b)
	st, b = share("bob", "m-bob", "bob", "editor")
	expect("viewer shares", st, 403, b)
	tick()
	st, b = call("alice", "POST", path+"/run", nil)
	expect("alice runs", st, 200, b)
	if rows(b) != `[["North"]]` || len(b["tiles"].([]any)) != 2 || b["data"] != "viewer" {
		t.Errorf("alice sees %v", b)
	}
	st, b = call("bob", "POST", path+"/run", nil)
	expect("bob runs", st, 200, b)
	if rows(b) != `[["North"],["South"],["West"]]` {
		t.Errorf("bob sees %s", rows(b))
	}
	st, b = call("mallory", "POST", path+"/run", nil) // no sql_user: viewing needs none
	expect("mallory runs", st, 200, b)
	st, b = call("bob", "GET", path, nil)
	expect("bob reads", st, 200, b)
	if b["draft"] != nil || b["canEdit"] != false {
		t.Errorf("a viewer got the draft: %v", b)
	}
	st, b = call("bob", "PUT", path, map[string]any{"draft": draft("Bob's", regions), "version": 4})
	expect("viewer saves", st, 403, b)

	// Showing the sharer's data: erin, with no access of her own, sees alice's North rows.
	shareData := func(by, member, name, relation, data string) (int, map[string]any) {
		return call(by, "POST", path+"/shares", map[string]any{"member": member, "name": name, "relation": relation, "data": data})
	}
	st, b = shareData("alice", "m-erin", "erin", "viewer", "viewer")
	expect("share with erin, her own data", st, 200, b)
	tick()
	st, b = call("erin", "POST", path+"/run", nil)
	if rows(b) != `[]` || b["data"] != "viewer" {
		t.Errorf("erin with her own data sees %v", b)
	}
	st, b = shareData("alice", "m-erin", "erin", "editor", "sharer")
	expect("share the sharer's data with an editor", st, 400, b)
	st, b = shareData("alice", "m-erin", "erin", "viewer", "everyone")
	expect("share unknown data", st, 400, b)
	st, b = shareData("alice", "m-erin", "erin", "viewer", "sharer")
	expect("share alice's data with erin", st, 200, b)
	st, b = call("erin", "POST", path+"/run", nil)
	if rows(b) != `[["North"]]` || b["data"] != "sharer" {
		t.Errorf("erin with alice's data sees %v", b)
	}
	// Downloads need exporter, asked fresh each time, and hold the downloader's own rows only.
	download := func(user string, tile int, filters ...string) (int, string, http.Header) {
		t.Helper()
		u := fmt.Sprintf("%s/tiles/%d/csv", path, tile)
		if len(filters) > 0 {
			u += "?filters=" + url.QueryEscape(filters[0])
		}
		r := httptest.NewRequest("GET", u, nil)
		r.Header.Set("Authorization", "Bearer "+user)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code, w.Body.String(), w.Header()
	}
	const bom = "\uFEFF"
	st, csv, _ := download("alice", 0)
	expect("download without exporter", st, 403, nil)
	f.Grant("c-alpha", "connection", "mart", "exporter", "alice")
	f.Grant("c-alpha", "connection", "mart", "exporter", "bob")
	f.Grant("c-alpha", "connection", "mart", "exporter", "erin")
	st, csv, hdr := download("alice", 0) // no tick: the denial above is not cached for a download
	if st != 200 || csv != bom+"region_name\nNorth\n" || hdr.Get("Content-Disposition") != `attachment; filename="Regions - tile 1.csv"` {
		t.Errorf("alice downloads %d %q %v", st, csv, hdr)
	}
	if st, csv, _ = download("alice", 1); csv != bom+"n\n1\n" {
		t.Errorf("alice downloads tile 2: %d %q", st, csv)
	}
	if st, csv, _ = download("erin", 0); csv != bom+"region_name\n" {
		t.Errorf("erin downloads %d %q: her own rows, not alice's", st, csv)
	}
	if st, csv, _ = download("bob", 0); csv != bom+"region_name\nNorth\nSouth\n… more rows not included: a download stops at 2 rows\n" {
		t.Errorf("bob downloads past the limit: %d %q", st, csv)
	}
	st, _, _ = download("alice", 2)
	expect("download a tile that is not there", st, 404, nil)
	st, _, _ = download("carol", 0)
	expect("download from another company", st, 404, nil)
	tick()
	if st, b = call("alice", "GET", path, nil); b["canExport"] != true {
		t.Errorf("alice's report: %v", b)
	}
	// Filters narrow the tiles with that column, and never widen what the viewer sees.
	filtered := func(user string, filters map[string][]string) map[string]any {
		t.Helper()
		st, b := call(user, "POST", path+"/run", map[string]any{"filters": filters})
		expect(user+" runs filtered", st, 200, b)
		return b
	}
	tile := func(b map[string]any, i int) string {
		t := b["tiles"].([]any)[i].(map[string]any)
		r, _ := json.Marshal(t["rows"])
		return fmt.Sprintf("%s %v%v", r, t["filtered"], t["error"])
	}
	b = filtered("bob", map[string][]string{"region_name": {"North", "West"}, "nowhere": {"x"}})
	if tile(b, 0) != `[["North"],["West"]] [region_name]<nil>` || tile(b, 1) != `[[3]] <nil><nil>` {
		t.Errorf("bob filtered: %s | %s", tile(b, 0), tile(b, 1))
	}
	if o, _ := json.Marshal(b["options"]); string(o) != `{"nowhere":[],"region_name":["North","South","West"]}` {
		t.Errorf("bob's choices: %s", o)
	}
	b = filtered("alice", map[string][]string{"region_name": {"South"}})
	if tile(b, 0) != `[] [region_name]<nil>` {
		t.Errorf("alice filtered to South: %s", tile(b, 0))
	}
	if o, _ := json.Marshal(b["options"]); string(o) != `{"region_name":["North"]}` {
		t.Errorf("alice's choices: %s", o)
	}
	st, b = call("bob", "POST", path+"/run", map[string]any{"filters": map[string]any{"region_name": "North"}})
	expect("filter values not a list", st, 400, b)
	st, b = call("bob", "POST", path+"/run", map[string]any{"filters": map[string]any{"region_name": []string{}}})
	expect("filter without values", st, 400, b)
	if st, csv, _ = download("bob", 0, `{"region_name":["West"]}`); csv != bom+"region_name\nWest\n" {
		t.Errorf("bob downloads filtered: %d %q", st, csv)
	}
	st, _, _ = download("bob", 0, `{"region_name":"West"}`)
	expect("download with a bad filter", st, 400, nil)

	// A matrix tile: the database totals the tile's rows, after filters, at every level.
	const sales = "SELECT * FROM (VALUES ('North', 'A', 1), ('North', 'B', 2), ('South', 'A', 4), ('South', NULL, 8)) AS v(region_name, cat, n)"
	mtile := map[string]any{"title": "", "sql": sales, "chart": nil, "show": "matrix", "wide": true,
		"matrix": map[string]any{"rows": []string{"region_name"}, "cols": []string{"cat"}, "values": []any{map[string]any{"column": "n", "agg": "sum"}}}}
	st, b = call("bob", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": map[string]any{"title": "Matrix", "tiles": []any{mtile}}})
	expect("create a matrix", st, 201, b)
	mpath := "/api/reports/" + b["id"].(string)
	st, b = call("bob", "POST", mpath+"/publish", map[string]any{"version": 1})
	expect("publish a matrix", st, 200, b)
	b = func() map[string]any {
		st, b := call("bob", "POST", mpath+"/run", nil)
		expect("run a matrix", st, 200, b)
		return b
	}()
	if c, _ := json.Marshal(b["tiles"].([]any)[0].(map[string]any)["columns"]); string(c) != `["hareno_level","region_name","cat","n (sum)"]` {
		t.Errorf("matrix columns: %s", c)
	}
	// level 1: all categories; 2: all regions; 3: both. A NULL category (level 0) is not a total.
	want := `[[0,"North","A",1],[0,"North","B",2],[0,"South","A",4],[0,"South",null,8],[1,"North",null,3],[1,"South",null,12],` +
		`[2,null,"A",5],[2,null,"B",2],[2,null,null,8],[3,null,null,15]]`
	sorted := func(b map[string]any) string {
		rs := b["tiles"].([]any)[0].(map[string]any)["rows"].([]any)
		slices.SortFunc(rs, func(x, y any) int {
			a, _ := json.Marshal(x)
			c, _ := json.Marshal(y)
			return strings.Compare(string(a), string(c))
		})
		out, _ := json.Marshal(rs)
		return string(out)
	}
	if got := sorted(b); got != want {
		t.Errorf("matrix rows:\n got %s\nwant %s", got, want)
	}
	st, b = call("bob", "POST", mpath+"/run", map[string]any{"filters": map[string][]string{"region_name": {"South"}}})
	if got := sorted(b); got != `[[0,"South","A",4],[0,"South",null,8],[1,"South",null,12],[2,null,"A",4],[2,null,null,8],[3,null,null,12]]` {
		t.Errorf("matrix filtered to South: %s", got)
	}
	for name, m := range map[string]any{
		"no rows":        map[string]any{"rows": []string{}, "values": []any{map[string]any{"column": "n", "agg": "sum"}}},
		"bad aggregate":  map[string]any{"rows": []string{"region_name"}, "values": []any{map[string]any{"column": "n", "agg": "median"}}},
		"column twice":   map[string]any{"rows": []string{"cat"}, "cols": []string{"cat"}, "values": []any{map[string]any{"column": "n", "agg": "sum"}}},
		"missing matrix": nil,
	} {
		mtile["matrix"] = m
		st, b = call("bob", "PUT", mpath, map[string]any{"draft": map[string]any{"title": "Matrix", "tiles": []any{mtile}}, "version": 1})
		expect("save a matrix with "+name, st, 400, b)
	}
	st, b = call("bob", "DELETE", mpath, nil)
	expect("delete the matrix", st, 204, b)

	// Dataset tiles: dan builds from the sales dataset without SQL rights, and sees South only.
	st, b = call("dan", "GET", "/api/datasets", nil)
	if d, _ := json.Marshal(b["datasets"]); !strings.Contains(string(d), `"name":"sales"`) || !strings.Contains(string(d), `"measures":["aov","avg_shipping","cost"`) {
		t.Errorf("dan's datasets: %s", d)
	}
	if st, b = call("mallory", "GET", "/api/datasets", nil); fmt.Sprint(b["datasets"]) != "[]" {
		t.Errorf("mallory's datasets: %v", b)
	}
	byRegion := map[string]any{"dataset": "sales", "dimensions": []string{"region_name"}, "measures": []string{"revenue", "order_count"}}
	dtile := map[string]any{"title": "", "sql": "", "chart": nil, "show": "table", "wide": true, "dataset": byRegion}
	st, b = call("dan", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": draft("SQL", regions)})
	expect("dan creates a SQL tile", st, 403, b)
	st, b = call("dan", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": map[string]any{"title": "Sales", "tiles": []any{dtile}}})
	expect("dan creates a dataset tile", st, 201, b)
	dpath := "/api/reports/" + b["id"].(string)
	st, b = call("dan", "POST", dpath+"/publish", map[string]any{"version": 1})
	expect("dan publishes", st, 200, b)
	st, b = call("dan", "POST", dpath+"/run", nil)
	if got := tile(b, 0); got != `[["South",175.5,2]] <nil><nil>` {
		t.Errorf("dan's sales by region: %s", got)
	}
	if c, _ := json.Marshal(b["tiles"].([]any)[0].(map[string]any)["columns"]); string(c) != `["region_name","revenue","order_count"]` {
		t.Errorf("dataset tile columns: %s", c)
	}
	if d, _ := json.Marshal(b["tiles"].([]any)[0].(map[string]any)["display"]); string(d) != `{"order_count":{"format":"integer","label":"Orders"},"region_name":{"label":"Region"},"revenue":{"format":"money","label":"Revenue"}}` {
		t.Errorf("dataset tile display: %s", d)
	}
	// Shipping beside lines is a fan-out-safe (symmetric) sum: exact, without the scale of 9
	// Malloy's version of it leaves (compiler/patches).
	raw, _ := json.Marshal(map[string]any{"connection": "mart", "dataset": map[string]any{"dataset": "sales", "measures": []string{"shipping", "line_count"}}})
	r := httptest.NewRequest("POST", "/api/sql", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer bob")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if got := strings.TrimSpace(w.Body.String()); !strings.Contains(got, `"rows":[[42.75,`) {
		t.Errorf("total shipping: %d %s", w.Code, got)
	}
	st, b = call("dan", "PUT", dpath, map[string]any{"draft": draft("SQL", regions), "version": 1})
	expect("dan adds SQL", st, 403, b)
	st, b = call("dan", "POST", "/api/sql", map[string]any{"connection": "mart", "dataset": byRegion,
		"matrix": map[string]any{"rows": []string{"region_name"}, "values": []any{map[string]any{"column": "revenue", "agg": "sum"}}}})
	expect("a dataset matrix value with an aggregate", st, 400, b)
	st, b = call("dan", "POST", "/api/sql", map[string]any{"connection": "mart", "dataset": byRegion,
		"matrix": map[string]any{"rows": []string{"region_name"}, "values": []any{map[string]any{"column": "revenue", "agg": ""}}}})
	expect("dan previews a matrix of a dataset tile", st, 200, b)
	if r, _ := json.Marshal(b["rows"]); string(r) != `[[0,"South",175.5],[1,null,175.5]]` {
		t.Errorf("dan's preview: %s", r)
	}
	if b["display"].(map[string]any)["region_name"] == nil {
		t.Errorf("preview display: %v", b["display"])
	}
	st, b = call("dan", "POST", "/api/sql", map[string]any{"connection": "mart", "sql": regions})
	expect("dan previews SQL", st, 403, b)
	st, b = call("dan", "POST", "/api/sql", map[string]any{"connection": "mart", "dataset": map[string]any{"dataset": "sales", "dimensions": []string{"category"}, "measures": []string{"shipping"}}})
	if st != 400 || !strings.Contains(fmt.Sprint(b["error"]), "grain") {
		t.Errorf("shipping by category: %d %v", st, b)
	}
	// bob, with all regions, sees every region through dan's report once shared, and filters it.
	st, b = call("dan", "POST", dpath+"/shares", map[string]any{"member": "m-bob", "name": "bob", "relation": "viewer"})
	expect("dan shares with bob", st, 200, b)
	tick()
	b = func() map[string]any {
		st, b := call("bob", "POST", dpath+"/run", nil)
		expect("bob runs dan's report", st, 200, b)
		return b
	}()
	if got := tile(b, 0); got != `[["North",238.5,3],["South",175.5,2],["West",173.75,2]] <nil><nil>` {
		t.Errorf("bob's sales by region: %s", got)
	}
	st, b = call("bob", "POST", dpath+"/run", map[string]any{"filters": map[string][]string{"region_name": {"West"}}})
	if got := tile(b, 0); got != `[["West",173.75,2]] [region_name]<nil>` {
		t.Errorf("bob filtered to West: %s", got)
	}
	// A ratio's total is computed again at each level (profit / revenue), not added up or averaged.
	margin := map[string]any{"dataset": "sales", "dimensions": []string{"region_name"}, "measures": []string{"profit", "revenue", "margin_pct"}}
	st, b = call("bob", "POST", "/api/sql", map[string]any{"connection": "mart", "dataset": margin,
		"matrix": map[string]any{"rows": []string{"region_name"}, "values": []any{map[string]any{"column": "profit", "agg": ""}, map[string]any{"column": "revenue", "agg": ""}, map[string]any{"column": "margin_pct", "agg": ""}}}})
	expect("bob previews margin", st, 200, b)
	rs := b["rows"].([]any)
	total := rs[len(rs)-1].([]any)
	if p, r, m := total[2].(float64), total[3].(float64), total[4].(float64); total[0] != 1.0 || math.Abs(m-p/r) > 1e-9 {
		t.Errorf("margin total %v, want profit / revenue = %v", total, p/r)
	}
	st, b = call("bob", "POST", dpath+"/run", map[string]any{"filters": map[string][]string{"region_name": {"O'Brien \\ x"}}})
	if got := tile(b, 0); got != `[] [region_name]<nil>` {
		t.Errorf("a quote in a dataset filter: %s", got)
	}
	st, b = call("dan", "DELETE", dpath, nil)
	expect("dan deletes", st, 204, b)

	f.Revoke("c-alpha", "connection", "mart", "exporter", "bob")
	st, _, _ = download("bob", 0) // no tick: a revoked exporter stops the next download
	expect("download after exporter is revoked", st, 403, nil)

	// alice's scope grows: erin sees it once alice opens the report again.
	f.Grant("c-alpha", "data_scope", "mart.region.South", "viewer", "alice")
	tick()
	st, b = call("erin", "POST", path+"/run", nil)
	if rows(b) != `[["North"]]` {
		t.Errorf("erin before alice returns sees %s", rows(b))
	}
	st, b = call("alice", "GET", path, nil)
	if b["canShareData"] != true {
		t.Errorf("alice's report: %v", b)
	}
	st, b = call("erin", "POST", path+"/run", nil)
	if rows(b) != `[["North"],["South"]]` {
		t.Errorf("erin after alice returns sees %s", rows(b))
	}
	// alice leaves the company: her data goes with her.
	delete(kiban.members, "m-alice")
	st, b = call("erin", "POST", path+"/run", nil)
	if rows(b) != `[]` || b["data"] != "viewer" {
		t.Errorf("erin after alice left sees %v", b)
	}
	kiban.members["m-alice"] = "alice"
	// alice may no longer share her data: her shares fall back to each viewer's own.
	f.Revoke("c-alpha", "connection", "mart", "data_sharer", "alice")
	f.Revoke("c-alpha", "data_scope", "mart.region.South", "viewer", "alice")
	tick()
	st, b = call("alice", "GET", path, nil)
	if b["canShareData"] != false {
		t.Errorf("alice's report: %v", b)
	}
	st, b = call("erin", "POST", path+"/run", nil)
	if rows(b) != `[]` {
		t.Errorf("erin after alice lost data_sharer sees %s", rows(b))
	}
	st, b = shareData("alice", "m-erin", "erin", "viewer", "sharer")
	expect("share data without data_sharer", st, 403, b)
	st, b = call("alice", "GET", path+"/shares", nil)
	if got, _ := json.Marshal(b["shares"]); !strings.Contains(string(got), `"data":"viewer","member":"m-erin","name":"erin","relation":"viewer"`) {
		t.Errorf("shares: %s", got)
	}

	// Drafts after publishing do not reach viewers until the next publish.
	st, b = call("alice", "PUT", path, map[string]any{"draft": draft("Regions, unpublished", "SELECT 1 AS one"), "version": 4})
	expect("save after publish", st, 200, b)
	st, b = call("bob", "POST", path+"/run", nil)
	if rows(b) != `[["North"],["South"],["West"]]` {
		t.Errorf("bob sees the unpublished draft: %s", rows(b))
	}
	st, b = call("bob", "GET", "/api/reports", nil)
	if list := b["reports"].([]any); len(list) != 1 || list[0].(map[string]any)["title"] != "Regions" {
		t.Errorf("bob's list: %v", b)
	}
	st, b = call("carol", "GET", "/api/reports", nil)
	if list := b["reports"].([]any); len(list) != 0 {
		t.Errorf("carol's list: %v", b)
	}

	// bob made editor (he has sql_user) can save; back to viewer, he cannot.
	st, b = share("alice", "m-bob", "bob", "editor")
	expect("bob to editor", st, 200, b)
	tick()
	st, b = call("bob", "PUT", path, map[string]any{"draft": draft("Regions, by bob", regions), "version": 5})
	expect("shared editor saves", st, 200, b)
	st, b = share("alice", "m-bob", "bob", "viewer")
	expect("bob back to viewer", st, 200, b)
	tick()
	st, b = call("bob", "PUT", path, map[string]any{"draft": draft("x", regions), "version": 6})
	expect("former editor saves", st, 403, b)
	st, b = call("alice", "DELETE", path+"/shares/m-mallory", nil)
	expect("unshare mallory", st, 204, b)
	tick()
	st, b = call("mallory", "POST", path+"/run", nil)
	expect("mallory after unshare", st, 404, b)

	// Without sql_user an editor can still view, but not change the SQL.
	f.Revoke("c-alpha", "connection", "mart", "sql_user", "alice")
	tick()
	st, b = call("alice", "PUT", path, map[string]any{"draft": draft("x", regions), "version": 6})
	expect("editor without sql_user saves", st, 403, b)
	st, b = call("alice", "POST", path+"/run", nil)
	expect("editor without sql_user runs", st, 200, b)

	st, b = call("bob", "DELETE", path, nil)
	expect("viewer deletes", st, 403, b)
	st, b = call("alice", "DELETE", path, nil)
	expect("delete", st, 204, b)
	tick()
	st, b = call("alice", "GET", path, nil)
	expect("after delete", st, 404, b)
	for k := range f.Grants {
		if strings.Contains(k, "|report|"+id+"|") {
			t.Errorf("grant left after delete: %s", k)
		}
	}
}

// datasets parses the dev mart's dataset documents by file name.
func datasets(t *testing.T, files ...string) map[string]*model.Dataset {
	out := map[string]*model.Dataset{}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(compilertest.Root(), "dev", "mart", "datasets", f+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		d, err := model.Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		out[d.Name] = d
	}
	return out
}
