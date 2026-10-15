package sqlapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hareno-bi/hareno/internal/compiler"
)

func TestCell(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"North", "North"},
		{"=HYPERLINK(\"http://x\")", "'=HYPERLINK(\"http://x\")"},
		{"+1", "'+1"}, {"-1", "'-1"}, {"@SUM(A1)", "'@SUM(A1)"}, {"\tx", "'\tx"},
		{json.Number("-1234567890123456.78"), "-1234567890123456.78"}, // numbers are not formulas
		{-1.5, "-1.5"}, {1e21, "1000000000000000000000"}, {int64(-3), "-3"}, {true, "true"},
		{time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), "2026-10-14"},
		{time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC), "2026-10-14 09:30:00"},
		{map[string]any{"a": "=1"}, `{"a":"=1"}`},
		{[]any{"=1"}, `["=1"]`},
	} {
		if got := cell(tc.in); got != tc.want {
			t.Errorf("cell(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWrap(t *testing.T) {
	f := Filters{"region": {"North"}, `odd "name`: {"a", "b"}}
	sql, args := f.wrap("SELECT 1 -- note\n;  \n", []string{"region", `odd "name`})
	want := "SELECT * FROM (\nSELECT 1 -- note\n) AS hareno_tile WHERE \"region\"::text = ANY($1) AND \"odd \"\"name\"::text = ANY($2)"
	if sql != want || len(args) != 2 {
		t.Errorf("wrap = %q %v", sql, args)
	}
}

func TestMatrixWrap(t *testing.T) {
	m := &Matrix{Rows: []string{"region", "cat"}, Cols: []string{"month"}, Values: []MatrixValue{{"n", "sum"}, {"n", "avg"}}}
	want := `SELECT GROUPING("region", "cat", "month") AS hareno_level, "region", "cat", "month", sum("n") AS "n (sum)", avg("n") AS "n (avg)" FROM (
SELECT 1
) AS hareno_matrix GROUP BY ROLLUP("region", "cat"), ROLLUP("month")`
	if got := m.wrap("SELECT 1;\n"); got != want || !m.valid(false) {
		t.Errorf("wrap = %s", got)
	}
}

func TestDatasetSQL(t *testing.T) {
	if got := where(Filters{"region": {"North", `O'Brien \ x`}}, []string{"region"}); got[0] != `(region = 'North' or region = 'O\'Brien \\ x')` {
		t.Errorf("where = %s", got)
	}
	c := compiler.Result{SQL: "SELECT row_to_json(x) FROM x;", Columns: []string{"month", "revenue"},
		Types: []map[string]any{{"kind": "timestamptz_type", "timeframe": "month"}, {"kind": "number_type"}}}
	want := `SELECT to_char(hareno_r."month" AT TIME ZONE 'Asia/Hong_Kong', 'YYYY-MM') AS "month", hareno_r."revenue" AS "revenue" FROM (
SELECT row_to_json(x) FROM x
) AS hareno_q(hareno_row) CROSS JOIN LATERAL json_to_record(hareno_row::json) AS hareno_r("month" timestamptz, "revenue" numeric) ORDER BY 1`
	if got := unwrap(c, "Asia/Hong_Kong", 1); got != want {
		t.Errorf("unwrap = %s", got)
	}
}
