package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hareno-bi/hareno/internal/compiler"
	"github.com/hareno-bi/hareno/internal/compiler/compilertest"
	"github.com/hareno-bi/hareno/internal/query"
)

// TestKnownAnswers runs dev mart known answers through dataset documents: planner, generated
// Malloy, sidecar, the mart as the restricted reader, and business-time labels. It covers the
// S1 cases raw Malloy gets wrong (C06, C17, C20), which the dataset layer must get right.
func TestKnownAnswers(t *testing.T) {
	compilertest.Require(t)
	sidecar, db := compilertest.Sidecar(t), compilertest.Mart(t)
	runner := func(d *Dataset) query.Runner {
		url := "file://hareno/" + d.Name + ".malloy"
		return query.Runner{
			Compiler: &compiler.Client{URL: sidecar}, DB: db, TimeZone: d.Location(),
			Model: query.Model{URL: url, Files: map[string]string{url: d.Malloy()}, Connection: d.Connection, Dialect: "postgres"},
		}
	}
	sales, customers := load(t, "sales.yaml"), load(t, "customers.yaml")

	for _, tc := range []struct {
		name string
		d    *Dataset
		q    Query
		want string // rows as "a b | c d", values rounded as in known-answers.sql
	}{
		{"totals", sales, Query{Measures: []string{"revenue", "order_count", "customer_count", "line_count"}}, "587.75 7 4 13"},
		{"fan-out (C04)", sales, Query{Measures: []string{"shipping", "revenue"}}, "42.75 587.75"},
		{"by region (C03, C05)", sales, Query{Dimensions: []string{"region_name"}, Measures: []string{"revenue", "shipping"}},
			"North 238.50 15.50 | South 175.50 19.25 | West 173.75 8.00"},
		{"distinct customers by category (C09)", sales, Query{Dimensions: []string{"category"}, Measures: []string{"customer_count"}},
			"Accessories 2 | Hardware 4 | Services 3"},
		{"margin recomputed (C10)", sales, Query{Dimensions: []string{"category"}, Measures: []string{"margin_pct"}},
			"Accessories 0.6000 | Hardware 0.3444 | Services 1.0000"},
		{"business-time months (C12)", sales, Query{Dimensions: []string{"order_month"}, Measures: []string{"revenue"}},
			"2026-01 86.00 | 2026-02 152.50 | 2026-03 85.50 | 2026-04 93.75 | 2026-05 170.00"},
		{"null sum stays null (C20)", sales, Query{Measures: []string{"shipping", "revenue"}, Where: []string{"order_id = 106"}}, "null 3.75"},
		{"guarded division (C17)", customers, Query{Dimensions: []string{"customer_name"}, Measures: []string{"aov"}},
			"Acme 93.0000 | Birch 52.5000 | Cobalt 87.7500 | Delta 86.8750 | Ember null"},
		{"aov total is not an average (C17)", sales, Query{Measures: []string{"aov"}}, "83.9643"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := tc.d.Plan(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			res, err := runner(tc.d).Run(context.Background(), q)
			if err != nil {
				t.Fatalf("%v\n%s", err, q)
			}
			if got := render(res, tc.want); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}

	t.Run("grain rejection (C06)", func(t *testing.T) {
		if _, err := sales.Plan(Query{Dimensions: []string{"category"}, Measures: []string{"shipping"}}); !errors.Is(err, ErrGrain) {
			t.Fatalf("want ErrGrain, got %v", err)
		}
	})
}

// render prints rows like want, rounding each number to the decimals of the matching value in
// want (half away from zero is enough here; the golden suite does exact half-even).
func render(res query.Result, want string) string {
	wantVals := strings.Fields(strings.ReplaceAll(want, "|", ""))
	var rows []string
	k := 0
	for _, r := range res.Rows {
		var vals []string
		for _, v := range r {
			s := fmt.Sprint(v)
			if v == nil {
				s = "null"
			} else if k < len(wantVals) {
				if i := strings.IndexByte(wantVals[k], '.'); i >= 0 {
					var f float64
					if _, err := fmt.Sscan(s, &f); err == nil {
						s = fmt.Sprintf("%.*f", len(wantVals[k])-i-1, f)
					}
				}
			}
			vals = append(vals, s)
			k++
		}
		rows = append(rows, strings.Join(vals, " "))
	}
	return strings.Join(rows, " | ")
}
