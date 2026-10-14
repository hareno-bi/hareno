package sqlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rosschiu/kiban/sdk"

	"github.com/hareno-bi/hareno/internal/appdb"
	"github.com/hareno-bi/hareno/internal/appdb/appdbtest"
	"github.com/hareno-bi/hareno/internal/authz"
	"github.com/hareno-bi/hareno/internal/sqlexec"
)

// grants writes user tuples into the fake Kiban, as Kiban would.
type grants struct{ f *authz.Fake }

func (g grants) Grant(_ context.Context, company string, ts ...sdk.Tuple) error {
	for _, t := range ts {
		if t.SubjectType == "user" {
			g.f.Grant(company, t.ObjectType, t.ObjectID, t.Relation, t.SubjectID)
		}
	}
	return nil
}

func (g grants) Revoke(_ context.Context, company string, ts ...sdk.Tuple) error {
	for _, t := range ts {
		if t.SubjectType == "user" {
			g.f.Revoke(company, t.ObjectType, t.ObjectID, t.Relation, t.SubjectID)
		}
	}
	return nil
}

// TestReports: create, save with version checks, publish, run as viewers with their own row
// scope, list, and delete; with the permission rules of reports.go.
func TestReports(t *testing.T) {
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
		"alice": alpha, "bob": alpha, "mallory": alpha, "carol": {{ID: "c-beta", Code: "BETA", IsActive: true}},
	}}
	f.Grant("c-alpha", "connection", "mart", "sql_user", "alice")
	f.Grant("c-alpha", "connection", "mart", "sql_user", "bob")
	f.Grant("c-alpha", "data_scope", "mart.region.North", "viewer", "alice")
	f.Grant("c-alpha", "data_scope", "mart.region.*", "viewer", "bob")
	f.Grant("c-alpha", "data_scope", "mart.region.North", "viewer", "mallory")

	now := time.Now()
	tick := func() { now = now.Add(time.Minute) } // past the decision cache after a grant changes
	h := &Handler{
		Authz: &authz.Authz{K: f, Module: "hareno", Feature: "hareno.use", AllowTTL: 30 * time.Second, DenyTTL: 5 * time.Second,
			Now: func() time.Time { return now }},
		Connections: map[string]Connection{"mart": {
			Company: "ALPHA", ID: connID,
			Exec:  sqlexec.Exec{DB: mart, Role: "hareno_sql", Timeout: 5 * time.Second, MaxRows: 1000},
			Scope: map[string][]string{"region": {"North", "South", "West"}},
		}},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:     appDB,
		Grants: grants{f},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/reports", h.ListReports)
	mux.HandleFunc("POST /api/reports", h.CreateReport)
	mux.HandleFunc("GET /api/reports/{id}", h.GetReport)
	mux.HandleFunc("PUT /api/reports/{id}", h.SaveReport)
	mux.HandleFunc("DELETE /api/reports/{id}", h.DeleteReport)
	mux.HandleFunc("POST /api/reports/{id}/publish", h.PublishReport)
	mux.HandleFunc("POST /api/reports/{id}/run", h.RunReport)

	call := func(user, method, path string, body any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+user)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	expect := func(what string, got, want int, body map[string]any) {
		t.Helper()
		if got != want {
			t.Fatalf("%s: %d %v, want %d", what, got, body, want)
		}
	}
	const regions = "SELECT region_name FROM mart.dim_region ORDER BY 1"
	draft := func(title, sql string) map[string]any {
		return map[string]any{"title": title, "sql": sql, "chart": map[string]any{"kind": "bar", "x": "region_name"}}
	}
	rows := func(body map[string]any) string {
		b, _ := json.Marshal(body["rows"])
		return string(b)
	}

	st, b := call("mallory", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": draft("x", regions)})
	expect("create without sql_user", st, 403, b)
	st, b = call("carol", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": draft("x", regions)})
	expect("create on another company's connection", st, 404, b)
	st, b = call("alice", "POST", "/api/reports", map[string]any{"connection": "mart", "draft": draft("", regions)})
	expect("create without a title", st, 400, b)

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

	st, b = call("alice", "PUT", path, map[string]any{"draft": draft("Broken", "SELECT nope FROM nowhere"), "version": 2})
	expect("save broken SQL (drafts may be broken)", st, 200, b)
	st, b = call("alice", "POST", path+"/publish", map[string]any{"version": 3})
	expect("publish broken SQL", st, 400, b)
	if !strings.HasPrefix(b["error"].(string), "not published") {
		t.Errorf("publish error: %v", b)
	}
	st, b = call("alice", "PUT", path, map[string]any{"draft": draft("Regions", regions), "version": 3})
	expect("fix", st, 200, b)
	st, b = call("alice", "POST", path+"/publish", map[string]any{"version": 4})
	expect("publish", st, 200, b)
	if b["revision"].(float64) != 1 {
		t.Errorf("revision: %v", b)
	}

	// Shared with bob as viewer: he sees all regions, alice North only, from the same revision.
	f.Grant("c-alpha", "report", id, "viewer", "bob")
	f.Grant("c-alpha", "report", id, "viewer", "mallory")
	tick()
	st, b = call("alice", "POST", path+"/run", nil)
	expect("alice runs", st, 200, b)
	if rows(b) != `[["North"]]` {
		t.Errorf("alice sees %s", rows(b))
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

	// Without sql_user an editor can still view, but not change the SQL.
	f.Revoke("c-alpha", "connection", "mart", "sql_user", "alice")
	tick()
	st, b = call("alice", "PUT", path, map[string]any{"draft": draft("x", regions), "version": 5})
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
}
