package sqlapi

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Dashboard filters (P2-08; owner decision 2026-10-14: automatic). A filter is a column name and
// the values to keep. It applies to every tile whose result has a column of that name, by wrapping
// the tile's query:
//
//	SELECT * FROM (<tile SQL>) AS hareno_tile WHERE "region"::text = ANY($1) AND ...
//
// so it narrows the tile's own result and never widens it: the database's row policy still
// applies inside. A tile without the column is left as it is, and says so. A report's draft
// lists the columns its filter bar offers; clicking a chart adds a filter on its X column.
//
// ponytail: values compare as the column's text, so a number or time matches only as PostgreSQL
// writes it, and filter choices leave times out. Typed filters come with the semantic model (P2-09).
type Filters map[string][]string

const (
	maxFilters      = 10
	maxFilterValues = 100
	maxOptions      = 200 // choices offered per filter
)

func validColumn(c string) bool { return c != "" && len(c) <= 63 }

func (f Filters) valid() bool {
	if len(f) > maxFilters {
		return false
	}
	for c, vs := range f {
		if !validColumn(c) || len(vs) == 0 || len(vs) > maxFilterValues {
			return false
		}
		for _, v := range vs {
			if len(v) > 1000 {
				return false
			}
		}
	}
	return true
}

// on is the filtered columns among cols, sorted.
func (f Filters) on(cols []string) []string {
	var out []string
	for c := range f {
		if slices.Contains(cols, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
}

// wrap narrows sql by the filters on columns.
func (f Filters) wrap(sql string, columns []string) (string, []any) {
	var where []string
	var args []any
	for i, c := range columns {
		where = append(where, fmt.Sprintf("%s::text = ANY($%d)", pgx.Identifier{c}.Sanitize(), i+1))
		args = append(args, f[c])
	}
	return "SELECT * FROM (\n" + strings.TrimRight(sql, "; \t\r\n") + "\n) AS hareno_tile WHERE " + strings.Join(where, " AND "), args
}

// options are the values of each column across the results, sorted: the filter bar's choices.
// They come from the viewer's own results, so they never show a value outside their rows.
func options(cols []string, results []tileResult) map[string][]string {
	out := map[string][]string{}
	for _, c := range cols {
		seen := map[string]bool{}
		for _, r := range results {
			if r.response == nil {
				continue
			}
			i := slices.Index(r.Columns, c)
			if i < 0 {
				continue
			}
			for _, row := range r.Rows {
				switch v := row[i].(type) {
				case nil, time.Time:
				case json.Number:
					seen[string(v)] = true
				default:
					seen[fmt.Sprint(v)] = true
				}
			}
		}
		vals := make([]string, 0, len(seen))
		for v := range seen {
			vals = append(vals, v)
		}
		slices.Sort(vals)
		out[c] = vals[:min(len(vals), maxOptions)]
	}
	return out
}
