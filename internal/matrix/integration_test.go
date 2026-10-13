package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hareno-bi/hareno/internal/compiler"
)

// TestQ15ThroughMalloy runs the matrix end to end: Spec -> sidecar -> SQL on the dev mart as the
// restricted reader -> Shape, and compares with the mart's known answer Q15. Needs the dev mart
// (docker compose up -d mart) and the sidecar's node_modules; runs when HARENO_INTEGRATION=1.
func TestQ15ThroughMalloy(t *testing.T) {
	env := integration(t)
	spec := Spec{Source: "complete", Rows: []string{"region_name"}, Columns: []string{"category"}, Measures: []string{"revenue"}}
	m := env.run(t, spec)
	if got := Render(m); got != Q15Want {
		t.Errorf("got\n%s\nwant\n%s", got, Q15Want)
	}
}

// TestTwoLevelsWithRatio checks every cell and total of region > customer x category, for revenue
// and a ratio (margin), against PostgreSQL's own GROUPING SETS over the same data. Margin must be
// recomputed at each level, not averaged.
func TestTwoLevelsWithRatio(t *testing.T) {
	env := integration(t)
	spec := Spec{Source: "complete", Rows: []string{"region_name", "customer.customer_name"}, Columns: []string{"category"},
		Measures: []string{"revenue", "margin_pct"}}
	m := env.run(t, spec)

	oracle := map[string][2]float64{} // "region|customer|category" ("*" = all) -> revenue, margin
	rows, err := env.db.Query(context.Background(), `
		WITH l AS (
			SELECT r.region_name, c.customer_name, p.category,
			       li.quantity * li.unit_price * (1 - coalesce(li.discount, 0)) AS rev, li.quantity * p.unit_cost AS cost
			FROM mart.fact_order o
			JOIN mart.dim_customer c USING (customer_id) JOIN mart.dim_region r USING (region_id)
			JOIN mart.fact_order_line li USING (order_id) JOIN mart.dim_product p USING (product_id)
			WHERE o.status = 'complete')
		SELECT CASE WHEN GROUPING(region_name) = 1 THEN '*' ELSE region_name END,
		       CASE WHEN GROUPING(customer_name) = 1 THEN '*' ELSE customer_name END,
		       CASE WHEN GROUPING(category) = 1 THEN '*' ELSE category END,
		       sum(rev)::float8, ((sum(rev) - sum(cost)) / sum(rev))::float8
		FROM l GROUP BY GROUPING SETS ((region_name, customer_name, category), (region_name, customer_name),
		                               (region_name, category), (region_name), (category), ())`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r, c, k string
		var v [2]float64
		if err := rows.Scan(&r, &c, &k, &v[0], &v[1]); err != nil {
			t.Fatal(err)
		}
		oracle[r+"|"+c+"|"+k] = v
	}
	checked := 0
	check := func(where string, got []Value, want [2]float64) {
		for i := range want {
			g, _ := got[i].(json.Number).Float64()
			if math.Abs(g-want[i]) > 1e-9 {
				t.Errorf("%s measure %d: got %v, want %v", where, i, g, want[i])
			}
		}
		checked++
	}
	for _, r := range m.Rows {
		key := []string{"*", "*"}
		for i, k := range r.Key {
			key[i] = fmt.Sprint(k)
		}
		for ci, col := range m.Columns {
			id := key[0] + "|" + key[1] + "|" + fmt.Sprint(col[0])
			if r.Cells[ci] == nil {
				if _, ok := oracle[id]; ok {
					t.Errorf("%s: missing cell", id)
				}
				continue
			}
			check(id, r.Cells[ci], oracle[id])
		}
		check(key[0]+"|"+key[1]+"|*", r.Total, oracle[key[0]+"|"+key[1]+"|*"])
	}
	if checked != len(oracle) {
		t.Errorf("checked %d values, the oracle has %d", checked, len(oracle))
	}
}

type env struct {
	db      *pgxpool.Pool
	sidecar string
	model   string
}

func integration(t *testing.T) env {
	t.Helper()
	if os.Getenv("HARENO_INTEGRATION") != "1" {
		t.Skip("set HARENO_INTEGRATION=1 to run against the dev mart and compile sidecar")
	}
	dsn := os.Getenv("HARENO_MART_DSN")
	if dsn == "" {
		dsn = "postgres://hareno_reader:hareno_reader@localhost:55433/mart"
	}
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	model, err := os.ReadFile("../../dev/mart/mart.malloy")
	if err != nil {
		t.Fatal(err)
	}
	return env{db: db, sidecar: startSidecar(t), model: string(model)}
}

// run compiles spec through the sidecar, executes it as the restricted reader and shapes it.
func (e env) run(t *testing.T, spec Spec) Matrix {
	t.Helper()
	ctx := context.Background()
	c := compiler.Client{URL: e.sidecar}
	res, err := c.Compile(ctx, compiler.Request{
		ModelURL: "file://hareno/mart.malloy", Files: map[string]string{"file://hareno/mart.malloy": e.model},
		Connection: "mart", Dialect: "postgres", Query: spec.Malloy(),
	}, compiler.Postgres{DB: e.db})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := e.db.Query(ctx, res.SQL)
	if err != nil {
		t.Fatalf("%v\n%s", err, res.SQL)
	}
	var out []map[string]Value
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var r map[string]Value
		if err := d.Decode(&r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	m, err := spec.Shape(out)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func startSidecar(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.Command("node", "server.ts")
	cmd.Dir = "../../compiler"
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	for range 100 {
		if r, err := http.Get(url + "/healthz"); err == nil {
			r.Body.Close()
			return url
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("sidecar did not start")
	return ""
}
