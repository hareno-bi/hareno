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

	"github.com/hareno-bi/hareno/internal/authz"
	"github.com/hareno-bi/hareno/internal/compiler/compilertest"
	"github.com/hareno-bi/hareno/internal/sqlexec"
)

// TestEndToEnd: fake Kiban users against the real dev mart (with rls.sql), through the handler.
// Grants mirror dev/kiban/seed.sh: alice holds North, bob all regions, eve may use SQL but holds
// no scope, mallory holds North but not SQL, carol is in another company.
func TestEndToEnd(t *testing.T) {
	compilertest.Require(t)
	db, err := pgxpool.New(context.Background(), "postgres://hareno_gate:hareno_gate@localhost:55433/mart")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	alpha := []sdk.Company{{ID: "c-alpha", Code: "ALPHA", IsActive: true}}
	f := &authz.Fake{Companies: map[string][]sdk.Company{
		"alice": alpha, "bob": alpha, "eve": alpha, "mallory": alpha,
		"carol": {{ID: "c-beta", Code: "BETA", IsActive: true}},
	}}
	for _, u := range []string{"alice", "bob", "eve"} {
		f.Grant("c-alpha", "connection", "mart", "sql_user", u)
	}
	f.Grant("c-alpha", "data_scope", "mart.region.North", "viewer", "alice")
	f.Grant("c-alpha", "data_scope", "mart.region.North", "viewer", "mallory")
	f.Grant("c-alpha", "data_scope", "mart.region.*", "viewer", "bob")

	var audit bytes.Buffer
	h := &Handler{
		Authz: &authz.Authz{K: f, Module: "hareno", Feature: "hareno.use", AllowTTL: 30 * time.Second, DenyTTL: 5 * time.Second},
		Connections: map[string]Connection{"mart": {
			Company: "ALPHA",
			Exec:    sqlexec.Exec{DB: db, Role: "hareno_sql", Timeout: 5 * time.Second, MaxRows: 1000},
			Scope:   map[string][]string{"region": {"North", "South", "West"}},
		}},
		Log: slog.New(slog.NewJSONHandler(&audit, nil)),
	}
	const sql = "SELECT region_name FROM mart.dim_region ORDER BY 1"
	call := func(user, conn, sql string) (int, string) {
		body, _ := json.Marshal(map[string]string{"connection": conn, "sql": sql})
		r := httptest.NewRequest(http.MethodPost, "/api/sql", bytes.NewReader(body))
		if user != "" {
			r.Header.Set("Authorization", "Bearer "+user)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		b, _ := io.ReadAll(w.Body)
		return w.Code, strings.TrimSpace(string(b))
	}
	for _, tc := range []struct {
		user, conn, sql string
		status          int
		want            string
	}{
		{"alice", "mart", sql, 200, `{"columns":["region_name"],"rows":[["North"]],"truncated":false}`},
		{"bob", "mart", sql, 200, `{"columns":["region_name"],"rows":[["North"],["South"],["West"]],"truncated":false}`},
		{"eve", "mart", sql, 200, `{"columns":["region_name"],"rows":[],"truncated":false}`},
		{"mallory", "mart", sql, 403, ""},
		{"carol", "mart", sql, 404, ""}, // another company's connection is not revealed
		{"", "mart", sql, 401, ""},
		{"alice", "nope", sql, 404, ""},
		{"alice", "mart", "DELETE FROM mart.dim_region", 400, ""},
		{"alice", "mart", `SELECT set_config('hareno.region', '["*"]', true)`, 400, ""},
		{"alice", "mart", "SELECT round(sum(quantity * unit_price), 2) AS gross FROM mart.fact_order_line", 200,
			`{"columns":["gross"],"rows":[[262.50]],"truncated":false}`}, // North lines incl. cancelled order 107
	} {
		status, body := call(tc.user, tc.conn, tc.sql)
		if status != tc.status || (tc.want != "" && body != tc.want) {
			t.Errorf("%s %s %q: %d %s, want %d %s", tc.user, tc.conn, tc.sql, status, body, tc.status, tc.want)
		}
	}
	for user, want := range map[string]string{"alice": `["mart"]`, "mallory": `[]`, "carol": `[]`} {
		r := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
		r.Header.Set("Authorization", "Bearer "+user)
		w := httptest.NewRecorder()
		h.List(w, r)
		if got := strings.TrimSpace(w.Body.String()); got != `{"connections":`+want+`,"sql":`+want+`}` {
			t.Errorf("%s connections: %s, want %s", user, got, want)
		}
	}
	for _, want := range []string{`"msg":"sql ran"`, `"msg":"sql refused"`, `"msg":"sql failed"`, `"user":"alice"`} {
		if !strings.Contains(audit.String(), want) {
			t.Errorf("audit log lacks %s", want)
		}
	}
}
