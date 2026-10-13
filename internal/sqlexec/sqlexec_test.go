package sqlexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hareno-bi/hareno/internal/compiler/compilertest"
)

// These run against the dev mart with dev/mart/rls.sql applied (docker compose up -d mart).
func gate(t *testing.T) Exec {
	t.Helper()
	compilertest.Require(t)
	dsn := os.Getenv("HARENO_GATE_DSN")
	if dsn == "" {
		dsn = "postgres://hareno_gate:hareno_gate@localhost:55433/mart"
	}
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return Exec{DB: db, Role: "hareno_sql", Timeout: time.Second, MaxRows: 1000}
}

var north = Scope{"region": {"North"}}

func text(r Result) string {
	var rows []string
	for _, row := range r.Rows {
		var v []string
		for _, x := range row {
			v = append(v, fmt.Sprint(x))
		}
		rows = append(rows, strings.Join(v, " "))
	}
	return strings.Join(rows, " | ")
}

func TestScope(t *testing.T) {
	e := gate(t)
	ctx := context.Background()
	const revenue = `SELECT r.region_name, round(sum(l.quantity * l.unit_price * (1 - coalesce(l.discount, 0))), 2)
		FROM mart.fact_order o JOIN mart.dim_customer c USING (customer_id) JOIN mart.dim_region r USING (region_id)
		JOIN mart.fact_order_line l USING (order_id) WHERE o.status = 'complete' GROUP BY 1 ORDER BY 1`
	for _, tc := range []struct {
		name  string
		scope Scope
		sql   string
		want  string
	}{
		{"north sees north (Q17)", north, revenue, "North 238.50"},
		{"all regions", Scope{"region": {"*"}}, revenue, "North 238.50 | South 175.50 | West 173.75"},
		{"two regions", Scope{"region": {"North", "West"}}, revenue, "North 238.50 | West 173.75"},
		{"no scope sees nothing", nil, revenue, ""},
		{"empty scope sees nothing", Scope{"region": {}}, revenue, ""},
		{"customers are filtered too", north, "SELECT customer_name FROM mart.dim_customer ORDER BY 1", "Acme | Birch"},
		{"lines without joins are filtered", north, "SELECT count(*) FROM mart.fact_order_line", "7"}, // incl. cancelled 107
		{"unrestricted tables stay visible", north, "SELECT count(*) FROM mart.dim_product", "4"},
		{"reading the scope is harmless", north, "SELECT current_setting('hareno.region')", `["North"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := e.Run(ctx, tc.scope, tc.sql)
			if err != nil {
				t.Fatal(err)
			}
			if got := text(r); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEscapes: with a North-only scope, every attempt to widen the scope, change role, run a
// second statement or write must fail, and none may return rows outside North.
func TestEscapes(t *testing.T) {
	e := gate(t)
	ctx := context.Background()
	for _, tc := range []struct{ name, sql string }{
		{"set_config in a select", `SELECT set_config('hareno.region', '["*"]', true), count(*) FROM mart.dim_region`},
		{"set_config in a where", `SELECT region_name FROM mart.dim_region WHERE set_config('hareno.region', '["*"]', true) IS NOT NULL`},
		{"qualified set_config", `SELECT pg_catalog.set_config('hareno.region', '["*"]', true)`},
		{"switch role inside a statement", `SELECT region_name FROM mart.dim_region WHERE set_config('role', 'hareno_gate', true) IS NOT NULL`},
		{"two statements", `SELECT 1; SELECT region_name FROM mart.dim_region`},
		{"SET the scope", `SET LOCAL hareno.region = '["*"]'`},
		{"RESET ROLE", `RESET ROLE`},
		{"SET ROLE", `SET ROLE hareno_gate`},
		{"COMMIT", `COMMIT`},
		{"DO block", `DO $$ BEGIN PERFORM 1; END $$`},
		{"update returning", `UPDATE mart.dim_region SET region_name = 'x' RETURNING *`},
		{"delete in a CTE", `WITH d AS (DELETE FROM mart.fact_order RETURNING *) SELECT * FROM d`},
		{"update pg_settings", `UPDATE pg_settings SET setting = '["*"]' WHERE name = 'hareno.region' RETURNING name`},
		{"create a table", `CREATE TABLE mart.x AS SELECT 1 AS a`},
		{"temp table", `CREATE TEMP TABLE x AS SELECT * FROM mart.dim_region`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := e.Run(ctx, north, tc.sql)
			if err == nil {
				t.Fatalf("expected an error, got rows %q", text(r))
			}
			t.Logf("refused: %v", err)
		})
	}
	// After all that, the next statement still sees North only.
	r, err := e.Run(ctx, north, "SELECT region_name FROM mart.dim_region ORDER BY 1")
	if err != nil || text(r) != "North" {
		t.Fatalf("scope changed after escape attempts: %q %v", text(r), err)
	}
}

func TestLimits(t *testing.T) {
	e := gate(t)
	ctx := context.Background()
	e.MaxRows = 10
	r, err := e.Run(ctx, north, "SELECT g FROM generate_series(1, 100) g")
	if err != nil || len(r.Rows) != 10 || !r.Truncated {
		t.Errorf("row limit: %d rows, truncated=%v, err=%v", len(r.Rows), r.Truncated, err)
	}
	start := time.Now()
	_, err = e.Run(ctx, north, "SELECT pg_sleep(5)")
	if err == nil || time.Since(start) > 3*time.Second {
		t.Errorf("timeout: err=%v after %v", err, time.Since(start))
	}
	if _, err := e.Run(ctx, north, "SET LOCAL statement_timeout = 0"); !errors.Is(err, ErrNotQuery) && err == nil {
		t.Errorf("changing the timeout must be refused, got %v", err)
	}
}
