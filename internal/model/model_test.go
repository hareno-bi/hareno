package model

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
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
	d, _ := Parse([]byte(base + "display:\n  m: {label: Amount, format: money}\n"))
	if d.Shown("m") != (Display{"Amount", "money"}) || d.Shown("d") != (Display{Label: "D"}) {
		t.Errorf("shown: %v, %v", d.Shown("m"), d.Shown("d"))
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
		{"  m: {sum: amt}\n", "  m: {sum: amt}\ndisplay:\n  x: {label: X}\n", "display x: not a dimension or measure"},
		{"  m: {sum: amt}\n", "  m: {sum: amt}\ndisplay:\n  m: {format: euros}\n", "display m: format must be"},
		{"root: a", "root: a\ndate: nope", "date nope: must be a dimension"},
		{"  d: bj.col\n", "  d: bj.col\n  dm: bj.col.month\ndate: dm\n", "date dm: must be a dimension"},
		{"  d: bj.col\n", "  d: bj.col\nhierarchies:\n  h: [d]\n", "hierarchy h: needs two levels"},
		{"  d: bj.col\n", "  d: bj.col\nhierarchies:\n  h: [d, x]\n", "hierarchy h: x is not a dimension"},
		{"  d: bj.col\n", "  d: bj.col\n  dh: bj.at.hour\nhierarchies:\n  h: [dh, d]\n", "dh cannot be drilled into"},
		{"  d: bj.col\n", "  source: bj.col\n", `dimension name "source" is a reserved word`},
		{"  m: {sum: amt}", "  months: {sum: amt}", `measure name "months" is a reserved word`},
	} {
		doc := strings.Replace(base, tc.from, tc.to, 1)
		_, err := Parse([]byte(doc))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q -> %q: got %v, want an error containing %q", tc.from, tc.to, err, tc.want)
		}
	}
}

// TestIs: a clicked value as a condition: a period as the days it spans, text as itself.
func TestIs(t *testing.T) {
	d := load(t, "sales.yaml")
	for _, tc := range []struct{ dim, value, want string }{
		{"order_year", "2026", "ordered_at >= @2026-01-01 and ordered_at < @2027-01-01"},
		{"order_quarter", "2026-Q4", "ordered_at >= @2026-10-01 and ordered_at < @2027-01-01"},
		{"order_month", "2026-02", "ordered_at >= @2026-02-01 and ordered_at < @2026-03-01"},
		{"order_day", "2026-02-28", "ordered_at >= @2026-02-28 and ordered_at < @2026-03-01"},
		{"category", "Toys 'n' games", `category = 'Toys \'n\' games'`},
	} {
		if got, err := d.Is(tc.dim, tc.value); got != tc.want || err != nil {
			t.Errorf("%s %s: %q %v, want %q", tc.dim, tc.value, got, err, tc.want)
		}
	}
	for _, bad := range [][2]string{{"order_year", "2026' or true"}, {"order_quarter", "2026-Q5"}, {"order_quarter", "2026-Q1x"}, {"order_month", "2026-13"}, {"nope", "x"}} {
		if got, err := d.Is(bad[0], bad[1]); err == nil {
			t.Errorf("%s %q: %q", bad[0], bad[1], got)
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
	// a date range is read in the business time zone: 17:00 UTC on 21 October is the 22nd in Hong Kong
	today := d.Today(time.Date(2026, 10, 21, 17, 0, 0, 0, time.UTC))
	if got := d.Between(today.AddDate(0, -3, 1), today.AddDate(0, 0, 1)); got != "ordered_at >= @2026-07-23 and ordered_at < @2026-10-23" {
		t.Errorf("between: %s", got)
	}
	if _, err := d.Plan(Query{Dimensions: []string{"category"}, Measures: []string{"shipping"}}); !errors.Is(err, ErrGrain) {
		t.Errorf("C06 must be refused by the planner, got %v", err)
	}
}
