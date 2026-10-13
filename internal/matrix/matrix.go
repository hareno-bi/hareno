// Package matrix turns one Malloy query into a matrix with subtotals: hierarchical row groups,
// column groups, and totals at every level, computed by the database in a single query. Each
// total is a Malloy all() aggregate, so ratio measures are recomputed at every level rather than
// averaged (S1 C16, S3).
package matrix

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Spec describes a matrix over a Malloy source. Field names are model field paths.
type Spec struct {
	Source   string
	Rows     []string // row dimensions, outermost first
	Columns  []string // column dimensions, combined into one column key
	Measures []string
}

// Value is one result value: a json.Number, string, bool or nil.
type Value = any

// Matrix is the shaped result.
type Matrix struct {
	Columns [][]Value // column keys in display order
	Rows    []Row     // groups before their children; the grand total last
}

// Row is a leaf row, a group subtotal or the grand total.
type Row struct {
	Level int       // number of row dimensions fixed: len(Spec.Rows) for a leaf, 0 for the grand total
	Key   []Value   // the row dimension values, Level long
	Cells [][]Value // per column, per measure; nil where the group has no data for that column
	Total []Value   // per measure, across all columns
}

// alias names the aggregate for a measure at row depth k, with or without the column key.
func alias(m, k int, withCols bool) string {
	c := 0
	if withCols {
		c = 1
	}
	return fmt.Sprintf("m%d_r%d_c%d", m, k, c)
}

// Malloy returns the query. Dimensions are aliased r0.. and c0.. (all() and order_by take output
// names, and two paths may share a last segment). For row depth k and every measure it aggregates
// all(measure, first k row dimensions [+ columns]); the leaf with columns is the plain measure.
func (s Spec) Malloy() string {
	var agg []string
	n := len(s.Rows)
	rowNames, colNames := aliases("r", len(s.Rows)), aliases("c", len(s.Columns))
	for mi, m := range s.Measures {
		for k := n; k >= 0; k-- {
			for _, withCols := range []bool{true, false} {
				if len(s.Columns) == 0 && withCols {
					continue
				}
				expr := m
				if k < n || !withCols {
					keep := slices.Clone(rowNames[:k])
					if withCols {
						keep = append(keep, colNames...)
					}
					expr = "all(" + strings.Join(append([]string{m}, keep...), ", ") + ")"
				}
				agg = append(agg, alias(mi, k, withCols)+" is "+expr)
			}
		}
	}
	var group []string
	for i, p := range s.Rows {
		group = append(group, rowNames[i]+" is "+p)
	}
	for i, p := range s.Columns {
		group = append(group, colNames[i]+" is "+p)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run: %s -> {\n", s.Source)
	if len(group) > 0 {
		fmt.Fprintf(&b, "  group_by: %s\n", strings.Join(group, ", "))
	}
	fmt.Fprintf(&b, "  aggregate:\n    %s\n", strings.Join(agg, "\n    "))
	if len(group) > 0 {
		fmt.Fprintf(&b, "  order_by: %s\n", strings.Join(append(slices.Clone(rowNames), colNames...), ", "))
	}
	b.WriteString("}\n")
	return b.String()
}

// Shape builds the matrix from the query's result rows (each a JSON object keyed by output name,
// decoded with json.Decoder.UseNumber).
func (s Spec) Shape(rows []map[string]Value) (Matrix, error) {
	n := len(s.Rows)
	rowNames, colNames := aliases("r", len(s.Rows)), aliases("c", len(s.Columns))
	pick := func(r map[string]Value, ns []string) []Value {
		out := make([]Value, len(ns))
		for i, f := range ns {
			out[i] = r[f]
		}
		return out
	}

	// Column keys, sorted.
	var cols [][]Value
	colIdx := map[string]int{}
	if len(s.Columns) > 0 {
		seen := map[string]bool{}
		for _, r := range rows {
			k := pick(r, colNames)
			if id := keyID(k); !seen[id] {
				seen[id] = true
				cols = append(cols, k)
			}
		}
		slices.SortFunc(cols, compareKeys)
	} else {
		cols = [][]Value{{}}
	}
	for i, c := range cols {
		colIdx[keyID(c)] = i
	}

	// One node per row prefix, created in result order (already sorted by the row dimensions).
	nodes := map[string]*Row{}
	var order []string
	node := func(key []Value) *Row {
		id := keyID(key)
		if r, ok := nodes[id]; ok {
			return r
		}
		r := &Row{Level: len(key), Key: key, Cells: make([][]Value, len(cols))}
		nodes[id] = r
		order = append(order, id)
		return r
	}
	for _, r := range rows {
		rk := pick(r, rowNames)
		ci := colIdx[keyID(pick(r, colNames))]
		for k := n; k >= 0; k-- {
			nd := node(rk[:k])
			if len(s.Columns) > 0 && nd.Cells[ci] == nil {
				nd.Cells[ci] = make([]Value, len(s.Measures))
				for mi := range s.Measures {
					nd.Cells[ci][mi] = r[alias(mi, k, true)]
				}
			}
			if nd.Total == nil {
				nd.Total = make([]Value, len(s.Measures))
				for mi := range s.Measures {
					nd.Total[mi] = r[alias(mi, k, false)]
				}
			}
		}
	}
	if len(s.Columns) == 0 {
		cols = nil
		for _, nd := range nodes {
			nd.Cells = nil
		}
	}

	// Display order: depth first, a group before its children, the grand total last.
	m := Matrix{Columns: cols}
	for _, id := range order {
		if nd := nodes[id]; nd.Level > 0 {
			m.Rows = append(m.Rows, *nd)
		}
	}
	slices.SortStableFunc(m.Rows, func(a, b Row) int { return compareKeys(a.Key, b.Key) })
	if g, ok := nodes[keyID([]Value{})]; ok {
		m.Rows = append(m.Rows, *g)
	}
	return m, nil
}

func aliases(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

func keyID(k []Value) string {
	b, _ := json.Marshal(k)
	return string(b)
}

// compareKeys orders keys element by element: nulls last, numbers numerically, otherwise as
// text. A shorter key that is a prefix of a longer one sorts first (a group before its rows).
func compareKeys(a, b []Value) int {
	for i := range min(len(a), len(b)) {
		if c := compareValues(a[i], b[i]); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
}

func compareValues(a, b Value) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	an, aok := a.(json.Number)
	bn, bok := b.(json.Number)
	if aok && bok {
		af, _ := an.Float64()
		bf, _ := bn.Float64()
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		}
		return 0
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

// Render prints a matrix as text, one line per row (key | column=value... | total), for tests and
// debugging. Missing cells print as "-"; only the first measure is shown. Numbers print as exact
// decimal text without trailing zeros.
func Render(m Matrix) string {
	var lines []string
	for _, r := range m.Rows {
		key := "TOTAL"
		if r.Level > 0 {
			parts := make([]string, len(r.Key))
			for i, k := range r.Key {
				parts[i] = fmt.Sprint(k)
			}
			key = strings.Join(parts, "/")
		}
		line := key + " |"
		if len(m.Columns) > 0 {
			for ci, c := range m.Columns {
				v := "-"
				if r.Cells[ci] != nil {
					v = text(r.Cells[ci][0])
				}
				line += fmt.Sprintf(" %s=%s", c[0], v)
			}
			line += " |"
		}
		lines = append(lines, line+" "+text(r.Total[0]))
	}
	return strings.Join(lines, "\n")
}

func text(v Value) string {
	n, ok := v.(json.Number)
	if !ok || !strings.Contains(string(n), ".") || strings.ContainsAny(string(n), "eE") {
		return fmt.Sprint(v)
	}
	return strings.TrimSuffix(strings.TrimRight(string(n), "0"), ".")
}
