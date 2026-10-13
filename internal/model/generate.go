package model

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Location is the dataset's business time zone.
func (d *Dataset) Location() *time.Location { return d.loc }

// tableSource names the generated Malloy source for a table; the prefix keeps table sources apart
// from the dataset source.
func tableSource(key string) string { return "t_" + key }

// Malloy generates the model: one source per table (children first, each with its own joins and
// computed columns), then the dataset source with its dimensions and guarded measures.
func (d *Dataset) Malloy() string {
	var b strings.Builder
	fmt.Fprintf(&b, "// Generated from dataset model document %q. Do not edit.\n\n", d.Name)
	var emit func(table, viaJoin string)
	emit = func(table, viaJoin string) {
		var children []Join
		for _, j := range d.Joins {
			if j.Parent == viaJoin {
				children = append(children, d.joins[j.Name])
			}
		}
		for _, j := range children {
			emit(j.To, j.Name)
		}
		t := d.Tables[table]
		fmt.Fprintf(&b, "source: %s is %s.table('%s') extend {\n", tableSource(table), d.Connection, t.Table)
		if t.PrimaryKey != "" {
			fmt.Fprintf(&b, "  primary_key: %s\n", t.PrimaryKey)
		}
		if t.Where != "" {
			fmt.Fprintf(&b, "  where: %s\n", t.Where)
		}
		for _, j := range children {
			fmt.Fprintf(&b, "  join_%s: %s is %s on %s = %s.%s\n", j.Kind, j.Name, tableSource(j.To), j.FromKey, j.Name, j.ToKey)
		}
		for _, c := range sortedKeys(t.Columns) {
			fmt.Fprintf(&b, "  dimension: %s is %s\n", c, t.Columns[c])
		}
		b.WriteString("}\n\n")
	}
	emit(d.Root, "")

	fmt.Fprintf(&b, "source: %s is %s extend {\n", d.Name, tableSource(d.Root))
	for _, name := range sortedKeys(d.Dimensions) {
		if p := d.Dimensions[name]; p != name {
			fmt.Fprintf(&b, "  dimension: %s is %s\n", name, p)
		}
	}
	for _, name := range d.measureOrder() {
		fmt.Fprintf(&b, "  measure: %s is %s\n", name, d.measureExpr(d.Measures[name]))
	}
	b.WriteString("}\n")
	return b.String()
}

// measureExpr is the Malloy expression for a measure, with S1's guards.
func (d *Dataset) measureExpr(m Measure) string {
	agg := func(col, fn string) string { return col + "." + fn + "()" }
	switch {
	case m.Sum != "":
		// Null when no input is non-null (condition 2; Malloy's sum returns 0).
		joins, _ := d.path(m.Sum)
		counter := "count()"
		if len(joins) > 0 {
			counter = strings.Join(joins, ".") + ".count()"
		}
		return fmt.Sprintf("pick %s when %s { where: %s != null } > 0 else null", agg(m.Sum, "sum"), counter, m.Sum)
	case m.Avg != "":
		return agg(m.Avg, "avg")
	case m.Min != "":
		return agg(m.Min, "min")
	case m.Max != "":
		return agg(m.Max, "max")
	case m.CountDistinct != "":
		return "count(" + m.CountDistinct + ")"
	case m.Count == "*":
		return "count()"
	case m.Count != "":
		return strings.Join(d.joinPath(m.Count), ".") + ".count()"
	case m.Ratio != [2]string{}:
		// Null when the denominator is zero (condition 2; Malloy raises division by zero).
		return fmt.Sprintf("pick %s / %s when %s != 0 else null", m.Ratio[0], m.Ratio[1], m.Ratio[1])
	default:
		return m.Expr
	}
}

// joinPath is the chain of joins from the root to join j.
func (d *Dataset) joinPath(j string) []string {
	var p []string
	for ; j != ""; j = d.joins[j].Parent {
		p = append([]string{j}, p...)
	}
	return p
}

// measureOrder lists measures with every measure after the measures it refers to.
func (d *Dataset) measureOrder() []string {
	var out []string
	done := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		if done[n] {
			return
		}
		done[n] = true
		for _, r := range d.refs(d.Measures[n]) {
			visit(r)
		}
		out = append(out, n)
	}
	for _, n := range sortedKeys(d.Measures) {
		visit(n)
	}
	return out
}

func (d *Dataset) refs(m Measure) []string {
	if m.Ratio != [2]string{} {
		return m.Ratio[:]
	}
	if m.Expr != "" {
		return measureRefs(m.Expr, d.Measures)
	}
	return nil
}

// grains returns the join paths at which a measure's sums and averages are computed (the path of
// the aggregated column). Counts, distinct counts, min and max carry no grain constraint: split
// by a finer dimension they keep their meaning ("orders containing a hardware line"), they only
// stop being additive, and every total is recomputed by the database rather than added up.
func (d *Dataset) grains(name string, seen []string) ([][]string, error) {
	if slices.Contains(seen, name) {
		return nil, fmt.Errorf("measures refer to each other in a cycle: %s", strings.Join(append(seen, name), " -> "))
	}
	m, ok := d.Measures[name]
	if !ok {
		return nil, fmt.Errorf("unknown measure %q", name)
	}
	if refs := d.refs(m); refs != nil {
		var out [][]string
		for _, r := range refs {
			g, err := d.grains(r, append(seen, name))
			if err != nil {
				return nil, err
			}
			out = append(out, g...)
		}
		return out, nil
	}
	if m.Sum == "" && m.Avg == "" {
		return nil, nil
	}
	p, err := d.path(m.column())
	return [][]string{p}, err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
