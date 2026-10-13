package matrix

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMalloy(t *testing.T) {
	got := Spec{Source: "complete", Rows: []string{"region_name", "customer.customer_name"}, Columns: []string{"category"},
		Measures: []string{"revenue"}}.Malloy()
	for _, want := range []string{
		"group_by: r0 is region_name, r1 is customer.customer_name, c0 is category",
		"m0_r2_c1 is revenue",
		"m0_r2_c0 is all(revenue, r0, r1)",
		"m0_r1_c1 is all(revenue, r0, c0)",
		"m0_r1_c0 is all(revenue, r0)",
		"m0_r0_c1 is all(revenue, c0)",
		"m0_r0_c0 is all(revenue)",
		"order_by: r0, r1, c0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("query lacks %q:\n%s", want, got)
		}
	}
}

// Rows as Malloy returns them for region x category (the dev mart's Q15), deliberately out of
// order and with South missing Accessories.
const q15 = `[
 {"r0":"West","c0":"Hardware","m0_r1_c1":50,"m0_r1_c0":173.75,"m0_r0_c1":271.5,"m0_r0_c0":587.75},
 {"r0":"North","c0":"Accessories","m0_r1_c1":12.5,"m0_r1_c0":238.5,"m0_r0_c1":16.25,"m0_r0_c0":587.75},
 {"r0":"North","c0":"Hardware","m0_r1_c1":126,"m0_r1_c0":238.5,"m0_r0_c1":271.5,"m0_r0_c0":587.75},
 {"r0":"North","c0":"Services","m0_r1_c1":100,"m0_r1_c0":238.5,"m0_r0_c1":300,"m0_r0_c0":587.75},
 {"r0":"South","c0":"Hardware","m0_r1_c1":95.5,"m0_r1_c0":175.5,"m0_r0_c1":271.5,"m0_r0_c0":587.75},
 {"r0":"South","c0":"Services","m0_r1_c1":80,"m0_r1_c0":175.5,"m0_r0_c1":300,"m0_r0_c0":587.75},
 {"r0":"West","c0":"Accessories","m0_r1_c1":3.75,"m0_r1_c0":173.75,"m0_r0_c1":16.25,"m0_r0_c0":587.75},
 {"r0":"West","c0":"Services","m0_r1_c1":120,"m0_r1_c0":173.75,"m0_r0_c1":300,"m0_r0_c0":587.75}
]`

// Q15Want is the dev mart's known answer, one line per matrix row: key | cells by column | total.
const Q15Want = `North | Accessories=12.5 Hardware=126 Services=100 | 238.5
South | Accessories=- Hardware=95.5 Services=80 | 175.5
West | Accessories=3.75 Hardware=50 Services=120 | 173.75
TOTAL | Accessories=16.25 Hardware=271.5 Services=300 | 587.75`

func TestShapeQ15(t *testing.T) {
	var rows []map[string]Value
	d := json.NewDecoder(strings.NewReader(q15))
	d.UseNumber()
	if err := d.Decode(&rows); err != nil {
		t.Fatal(err)
	}
	spec := Spec{Source: "complete", Rows: []string{"region_name"}, Columns: []string{"category"}, Measures: []string{"revenue"}}
	m, err := spec.Shape(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := Render(m); got != Q15Want {
		t.Errorf("got\n%s\nwant\n%s", got, Q15Want)
	}
}

func TestShapeTwoLevels(t *testing.T) {
	// Region > customer rows, no columns: each region's subtotal precedes its customers.
	rows := []map[string]Value{
		{"r0": "North", "r1": "Birch", "m0_r2_c0": json.Number("105"), "m0_r1_c0": json.Number("238.5"), "m0_r0_c0": json.Number("587.75")},
		{"r0": "North", "r1": "Acme", "m0_r2_c0": json.Number("133.5"), "m0_r1_c0": json.Number("238.5"), "m0_r0_c0": json.Number("587.75")},
		{"r0": "South", "r1": "Cobalt", "m0_r2_c0": json.Number("175.5"), "m0_r1_c0": json.Number("175.5"), "m0_r0_c0": json.Number("587.75")},
	}
	spec := Spec{Source: "complete", Rows: []string{"region_name", "customer.customer_name"}, Measures: []string{"revenue"}}
	m, err := spec.Shape(rows)
	if err != nil {
		t.Fatal(err)
	}
	want := `North | 238.5
North/Acme | 133.5
North/Birch | 105
South | 175.5
South/Cobalt | 175.5
TOTAL | 587.75`
	if got := Render(m); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
