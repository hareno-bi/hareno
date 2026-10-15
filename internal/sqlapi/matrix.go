package sqlapi

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Matrix tiles (P2-08): a tile's rows grouped by up to three row columns and one column column,
// with an aggregate per value, subtotals for every row group and the column, and a grand total.
// The database computes every total from the tile's rows, under the viewer's row policy, by
// wrapping the tile's query:
//
//	SELECT GROUPING("region", "category", "month") AS hareno_level, "region", "category", "month",
//	       sum("revenue") AS "revenue (sum)"
//	FROM (<tile SQL>) AS hareno_matrix GROUP BY ROLLUP("region", "category"), ROLLUP("month")
//
// so an average or a count is right at every level, not an average of averages. hareno_level
// says which columns a row totals over (one bit each, the first column highest), telling a
// total from a NULL value. Filters narrow the tile's query inside, before the totals.
//
// ponytail: one column column and no collapsing groups; more when the matrix designer (Phase 3) needs them.
type Matrix struct {
	Rows   []string      `json:"rows"`
	Cols   []string      `json:"cols"`
	Values []MatrixValue `json:"values"`
}

type MatrixValue struct {
	Column string `json:"column"`
	Agg    string `json:"agg"` // sum, avg, min, max or count
}

var aggs = []string{"sum", "avg", "min", "max", "count"}

func (m *Matrix) valid() bool {
	if len(m.Rows) == 0 || len(m.Rows) > 3 || len(m.Cols) > 1 || len(m.Values) == 0 || len(m.Values) > 6 {
		return false
	}
	dims := append(slices.Clone(m.Rows), m.Cols...)
	for i, c := range dims {
		if !validColumn(c) || slices.Contains(dims[:i], c) {
			return false
		}
	}
	for i, v := range m.Values {
		if !validColumn(v.Column) || !slices.Contains(aggs, v.Agg) || slices.Contains(m.Values[:i], v) {
			return false
		}
	}
	return true
}

// wrap totals sql's rows as the matrix asks. Placeholders in sql (filters) are kept.
func (m *Matrix) wrap(sql string) string {
	id := func(c string) string { return pgx.Identifier{c}.Sanitize() }
	ids := func(cs []string) string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = id(c)
		}
		return strings.Join(out, ", ")
	}
	dims := ids(append(slices.Clone(m.Rows), m.Cols...))
	sel := []string{"GROUPING(" + dims + ") AS hareno_level", dims}
	for _, v := range m.Values {
		sel = append(sel, fmt.Sprintf("%s(%s) AS %s", v.Agg, id(v.Column), id(v.Column+" ("+v.Agg+")")))
	}
	group := "ROLLUP(" + ids(m.Rows) + ")"
	if len(m.Cols) > 0 {
		group += ", ROLLUP(" + ids(m.Cols) + ")"
	}
	return "SELECT " + strings.Join(sel, ", ") + " FROM (\n" + strings.TrimRight(sql, "; \t\r\n") + "\n) AS hareno_matrix GROUP BY " + group
}
