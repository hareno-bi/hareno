package model

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func load(t *testing.T, file string) *Dataset {
	t.Helper()
	b, err := os.ReadFile("../../dev/mart/datasets/" + file)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestGrain(t *testing.T) {
	d := load(t, "sales.yaml")
	for _, tc := range []struct {
		dims, measures []string
		reject         bool
	}{
		{[]string{"category"}, []string{"shipping"}, true},     // C06: order-grain sum split by line category
		{[]string{"category"}, []string{"avg_shipping"}, true}, // same for an average
		{[]string{"category"}, []string{"revenue", "margin_pct"}, false},
		{[]string{"region_name"}, []string{"shipping"}, false},                   // dimension on the one side
		{[]string{"category"}, []string{"customer_count", "order_count"}, false}, // counts keep their meaning
		{[]string{"product_name"}, []string{"aov"}, false},                       // aov = revenue (line grain) / orders containing
		{[]string{"order_month"}, []string{"shipping"}, false},
	} {
		err := d.Check(tc.dims, tc.measures)
		if got := errors.Is(err, ErrGrain); got != tc.reject {
			t.Errorf("%v by %v: rejected=%v, want %v (%v)", tc.measures, tc.dims, got, tc.reject, err)
		}
	}
	if err := d.Check([]string{"nope"}, []string{"revenue"}); err == nil || errors.Is(err, ErrGrain) {
		t.Errorf("unknown dimension: got %v", err)
	}
}

func TestMalloyGuards(t *testing.T) {
	m := load(t, "sales.yaml").Malloy()
	for _, want := range []string{
		"source: t_products is mart.table('mart.dim_product')",
		"join_many: line is t_lines on order_id = line.order_id",
		"join_one: product is t_products on product_id = product.product_id",
		"where: status = 'complete'",
		"measure: shipping is pick shipping_cost.sum() when count() { where: shipping_cost != null } > 0 else null",
		"measure: revenue is pick line.line_revenue.sum() when line.count() { where: line.line_revenue != null } > 0 else null",
		"measure: aov is pick revenue / order_count when order_count != 0 else null",
		"dimension: region_name is customer.region.region_name",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("model lacks %q", want)
		}
	}
	// Tables are emitted before the sources that join them.
	if strings.Index(m, "source: t_products") > strings.Index(m, "source: t_lines") {
		t.Error("t_products must come before t_lines, which joins it")
	}
	if strings.Contains(m, "dimension: order_id is order_id") {
		t.Error("a dimension that is the root column of the same name must not be redefined")
	}
}

func TestParseRejects(t *testing.T) {
	base := `name: s
connection: mart
timezone: Asia/Hong_Kong
root: a
tables:
  a: {table: x.a, primary_key: id}
  b: {table: x.b}
joins:
  - {name: bj, to: b, kind: many, from_key: id}
dimensions:
  d: bj.col
measures:
  m: {sum: amt}
`
	if _, err := Parse([]byte(base)); err != nil {
		t.Fatalf("base document must parse: %v", err)
	}
	for _, tc := range []struct{ from, to, want string }{
		{"timezone: Asia/Hong_Kong", "timezone: Mars/Base", "timezone"},
		{"kind: many", "kind: lots", "kind must be one or many"},
		{"  m: {sum: amt}", "  m: {sum: amt, avg: amt}", "exactly one"},
		{"  m: {sum: amt}", "  m: {ratio: [m, m]}", "not another measure"},
		{"  d: bj.col", "  d: nope.x.col", "unknown join or field"},
		{"  m: {sum: amt}", "  m: {sum: amt}\n  n: {expr: \"n + 1\"}", "expr must refer to other measures"},
		{"  b: {table: x.b}", "  b: {table: x.b}\n  c: {table: x.c}", "not reachable"},
		{"root: a", "root: a\nextra: 1", "field extra not found"},
	} {
		doc := strings.Replace(base, tc.from, tc.to, 1)
		_, err := Parse([]byte(doc))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q -> %q: got %v, want an error containing %q", tc.from, tc.to, err, tc.want)
		}
	}
}

func TestPlan(t *testing.T) {
	d := load(t, "sales.yaml")
	q, err := d.Plan(Query{Dimensions: []string{"order_month"}, Measures: []string{"revenue"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"run: sales -> {", "timezone: 'Asia/Hong_Kong'", "group_by: order_month", "aggregate: revenue", "order_by: order_month"} {
		if !strings.Contains(q, want) {
			t.Errorf("plan lacks %q:\n%s", want, q)
		}
	}
	if _, err := d.Plan(Query{Dimensions: []string{"category"}, Measures: []string{"shipping"}}); !errors.Is(err, ErrGrain) {
		t.Errorf("C06 must be refused by the planner, got %v", err)
	}
}
