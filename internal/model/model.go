// Package model reads dataset model documents, generates the Malloy model from them, and plans
// queries against a dataset (ARCHITECTURE-PLAN.md §5). A document is admin-authored YAML: the
// tables, a tree of joins from the root table, named dimensions and named measures.
//
// The generator closes three Malloy gaps found by S1 (PHASE1-GATE.md conditions 1-2):
//   - sums are null when no input is non-null (Malloy returns 0);
//   - ratios are null when the denominator is zero (Malloy raises division by zero);
//   - the planner rejects a measure grouped by a dimension below the measure's grain (Malloy
//     answers with a meaningless number).
package model

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Dataset is a parsed model document.
type Dataset struct {
	Name       string             `yaml:"name"`
	Connection string             `yaml:"connection"`
	TimeZone   string             `yaml:"timezone"` // business time zone (IANA), e.g. Asia/Hong_Kong
	Root       string             `yaml:"root"`     // key of the root table
	Tables     map[string]Table   `yaml:"tables"`
	Joins      []Join             `yaml:"joins"`
	Dimensions map[string]string  `yaml:"dimensions"` // name -> path from the root, e.g. customer.region.region_name
	Measures   map[string]Measure `yaml:"measures"`
	Display    map[string]Display `yaml:"display"` // optional, by dimension or measure name
	// Date is the dimension a dashboard's date range narrows (a time or date, read in TimeZone);
	// optional: without it, date ranges leave the dataset's tiles as they are.
	Date string `yaml:"date"`
	// Hierarchies are drill paths, by name: dimensions from the top level down, e.g. year,
	// quarter, month. Each level above the last is text or a period (year, quarter, month, week, day).
	Hierarchies map[string][]string `yaml:"hierarchies"`

	joins map[string]Join
	loc   *time.Location
}

// Table is a source table and its computed columns (Malloy expressions over its own columns and
// its child joins).
type Table struct {
	Table      string            `yaml:"table"` // schema.table
	PrimaryKey string            `yaml:"primary_key"`
	Where      string            `yaml:"where"` // optional row filter (Malloy expression)
	Columns    map[string]string `yaml:"columns"`
}

// Join is an edge of the join tree: from its parent (another join, or the root when empty) to a
// table. The parent's from_key equals the table's to_key (to_key defaults to from_key).
type Join struct {
	Name    string `yaml:"name"`
	Parent  string `yaml:"parent"`
	To      string `yaml:"to"`
	Kind    string `yaml:"kind"` // one | many
	FromKey string `yaml:"from_key"`
	ToKey   string `yaml:"to_key"`
}

// Measure is exactly one of: an aggregate over a column path (sum, avg, min, max,
// count_distinct), a row count of the root ("*") or of a join, a ratio of two measures, or an
// arithmetic expression over other measures.
type Measure struct {
	Sum           string    `yaml:"sum"`
	Avg           string    `yaml:"avg"`
	Min           string    `yaml:"min"`
	Max           string    `yaml:"max"`
	CountDistinct string    `yaml:"count_distinct"`
	Count         string    `yaml:"count"`
	Ratio         [2]string `yaml:"ratio"`
	Expr          string    `yaml:"expr"`
}

// Display is how a dimension or measure is shown: its label (by default its name, capitalised,
// with spaces for underscores) and, for numbers, a format: number (the default, up to 2
// decimals), integer, money (2 decimals, no currency symbol) or percent (a ratio, times 100).
type Display struct {
	Label  string `yaml:"label" json:"label"`
	Format string `yaml:"format" json:"format,omitempty"`
}

var formats = []string{"", "number", "integer", "money", "percent"}

// Shown is how the field name is shown.
func (d *Dataset) Shown(name string) Display {
	s := d.Display[name]
	if s.Label == "" {
		s.Label = strings.ToUpper(name[:1]) + strings.ReplaceAll(name[1:], "_", " ")
	}
	return s
}

var ident = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// Parse reads and validates a model document.
func Parse(doc []byte) (*Dataset, error) {
	var d Dataset
	dec := yaml.NewDecoder(strings.NewReader(string(doc)))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("model document: %w", err)
	}
	return &d, d.validate()
}

func (d *Dataset) validate() error {
	var errs []string
	bad := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	for _, n := range []string{d.Name, d.Connection, d.Root} {
		if !ident.MatchString(n) {
			bad("name, connection and root must be lower-case identifiers (got %q)", n)
		}
	}
	loc, err := time.LoadLocation(d.TimeZone)
	if err != nil || d.TimeZone == "" {
		bad("timezone %q: must be an IANA zone", d.TimeZone)
	}
	d.loc = loc
	if _, ok := d.Tables[d.Root]; !ok {
		bad("root %q is not a table", d.Root)
	}
	used := map[string]bool{d.Root: true}
	d.joins = map[string]Join{}
	for _, j := range d.Joins {
		switch {
		case !ident.MatchString(j.Name):
			bad("join name %q", j.Name)
		case d.joins[j.Name] != (Join{}):
			bad("join %s: defined twice", j.Name)
		case j.Kind != "one" && j.Kind != "many":
			bad("join %s: kind must be one or many", j.Name)
		case j.FromKey == "":
			bad("join %s: from_key is required", j.Name)
		}
		if _, ok := d.Tables[j.To]; !ok {
			bad("join %s: table %q is not defined", j.Name, j.To)
		}
		if used[j.To] {
			bad("join %s: table %q is already in the join tree (each table appears once)", j.Name, j.To)
		}
		used[j.To] = true
		if j.Parent != "" {
			if _, ok := d.joins[j.Parent]; !ok {
				bad("join %s: parent %q must be defined before it", j.Name, j.Parent)
			}
		}
		if j.ToKey == "" {
			j.ToKey = j.FromKey
		}
		d.joins[j.Name] = j
	}
	for k := range d.Tables {
		if !used[k] {
			bad("table %s is not reachable from the root", k)
		}
	}
	for name, p := range d.Dimensions {
		if !ident.MatchString(name) {
			bad("dimension name %q", name)
		}
		if _, err := d.path(p); err != nil {
			bad("dimension %s: %v", name, err)
		}
	}
	for name, m := range d.Measures {
		if !ident.MatchString(name) {
			bad("measure name %q", name)
		}
		if _, ok := d.Dimensions[name]; ok {
			bad("%s is both a dimension and a measure", name)
		}
		if err := d.checkMeasure(name, m); err != nil {
			bad("measure %s: %v", name, err)
		}
	}
	if d.Date != "" {
		path, ok := d.Dimensions[d.Date]
		if !ok || timeframes[path[strings.LastIndex(path, ".")+1:]] { // e.g. ordered_at.month
			bad("date %s: must be a dimension holding a time or date, not a month or other period", d.Date)
		}
	}
	for name, levels := range d.Hierarchies {
		if len(levels) < 2 {
			bad("hierarchy %s: needs two levels or more", name)
		}
		for i, l := range levels {
			path, ok := d.Dimensions[l]
			if !ok || slices.Contains(levels[:i], l) {
				bad("hierarchy %s: %s is not a dimension, or is there twice", name, l)
			} else if frame := path[strings.LastIndex(path, ".")+1:]; i < len(levels)-1 && timeframes[frame] && periods[frame] == nil {
				bad("hierarchy %s: %s cannot be drilled into (only years, quarters, months, weeks and days)", name, l)
			}
		}
	}
	for name, s := range d.Display {
		_, dim := d.Dimensions[name]
		if _, ok := d.Measures[name]; !ok && !dim {
			bad("display %s: not a dimension or measure", name)
		}
		if !slices.Contains(formats, s.Format) {
			bad("display %s: format must be one of number, integer, money, percent", name)
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("model document %s:\n  %s", d.Name, strings.Join(errs, "\n  "))
	}
	return nil
}

func (d *Dataset) checkMeasure(name string, m Measure) error {
	kinds := 0
	for _, v := range []string{m.Sum, m.Avg, m.Min, m.Max, m.CountDistinct, m.Count, m.Expr} {
		if v != "" {
			kinds++
		}
	}
	if m.Ratio != [2]string{} {
		kinds++
		for _, r := range m.Ratio {
			if _, ok := d.Measures[r]; !ok || r == name {
				return fmt.Errorf("ratio term %q is not another measure", r)
			}
		}
	}
	if kinds != 1 {
		return fmt.Errorf("must have exactly one of sum, avg, min, max, count_distinct, count, ratio, expr")
	}
	if m.Count != "" && m.Count != "*" {
		if _, ok := d.joins[m.Count]; !ok {
			return fmt.Errorf("count %q: must be \"*\" or a join", m.Count)
		}
	}
	if col := m.column(); col != "" {
		if _, err := d.path(col); err != nil {
			return err
		}
	}
	if m.Expr != "" {
		refs := measureRefs(m.Expr, d.Measures)
		if len(refs) == 0 || slices.Contains(refs, name) {
			return fmt.Errorf("expr must refer to other measures")
		}
	}
	_, err := d.grains(name, nil)
	return err
}

func (m Measure) column() string {
	for _, v := range []string{m.Sum, m.Avg, m.Min, m.Max, m.CountDistinct} {
		if v != "" {
			return v
		}
	}
	return ""
}

var word = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

func measureRefs(expr string, measures map[string]Measure) []string {
	var refs []string
	for _, w := range word.FindAllString(expr, -1) {
		if _, ok := measures[w]; ok && !slices.Contains(refs, w) {
			refs = append(refs, w)
		}
	}
	return refs
}

// path splits a field path into the joins it walks from the root and the remaining field. Each
// join must be a child of the previous one (or of the root).
func (d *Dataset) path(p string) ([]string, error) {
	segs := strings.Split(p, ".")
	var joins []string
	parent := ""
	for len(segs) > 1 {
		j, ok := d.joins[segs[0]]
		if !ok {
			break
		}
		if j.Parent != parent {
			return nil, fmt.Errorf("%q: join %s is not reachable through %q", p, j.Name, parent)
		}
		joins = append(joins, j.Name)
		parent = j.Name
		segs = segs[1:]
	}
	// What remains is a field, optionally with a time bucket (ordered_at.month).
	if !ident.MatchString(segs[0]) || len(segs) > 2 || (len(segs) == 2 && !timeframes[segs[1]]) {
		return nil, fmt.Errorf("%q: unknown join or field %q", p, strings.Join(segs, "."))
	}
	return joins, nil
}

var timeframes = map[string]bool{"year": true, "quarter": true, "month": true, "week": true, "day": true, "hour": true, "minute": true}

// Between is the Malloy condition keeping the dataset's date from the day from up to, not
// including, the day to, both read in the business time zone.
func (d *Dataset) Between(from, to time.Time) string { return between(d.Date, from, to) }

func between(field string, from, to time.Time) string {
	day := func(t time.Time) string { return "@" + t.Format(time.DateOnly) }
	return fmt.Sprintf("%s >= %s and %s < %s", field, day(from), field, day(to))
}

// periods read a period's label as a query shows it (2026, 2026-Q1, 2026-03, 2026-03-02) and
// give the day after it ends.
var periods = map[string]func(label string) (from, to time.Time, err error){
	"year": func(v string) (time.Time, time.Time, error) {
		t, err := time.Parse("2006", v)
		return t, t.AddDate(1, 0, 0), err
	},
	"quarter": func(v string) (time.Time, time.Time, error) {
		var y, q int
		if n, _ := fmt.Sscanf(v, "%4d-Q%1d", &y, &q); n != 2 || q < 1 || q > 4 || fmt.Sprintf("%04d-Q%d", y, q) != v {
			return time.Time{}, time.Time{}, fmt.Errorf("%q is not a quarter", v)
		}
		t := time.Date(y, time.Month(q*3-2), 1, 0, 0, 0, 0, time.UTC)
		return t, t.AddDate(0, 3, 0), nil
	},
	"month": func(v string) (time.Time, time.Time, error) {
		t, err := time.Parse("2006-01", v)
		return t, t.AddDate(0, 1, 0), err
	},
	"week": func(v string) (time.Time, time.Time, error) {
		t, err := time.Parse(time.DateOnly, v)
		return t, t.AddDate(0, 0, 7), err
	},
	"day": func(v string) (time.Time, time.Time, error) {
		t, err := time.Parse(time.DateOnly, v)
		return t, t.AddDate(0, 0, 1), err
	},
}

// Is is the Malloy condition keeping the rows where the dimension shows value: for a period, the
// days it spans in the business time zone; otherwise the text itself.
func (d *Dataset) Is(dim, value string) (string, error) {
	path, ok := d.Dimensions[dim]
	if !ok {
		return "", fmt.Errorf("unknown dimension %q", dim)
	}
	i := strings.LastIndex(path, ".")
	if read := periods[path[i+1:]]; read != nil {
		from, to, err := read(value)
		if err != nil {
			return "", fmt.Errorf("%s: %q is not one of its periods", dim, value)
		}
		return between(path[:i], from, to), nil
	}
	return dim + " = '" + escapes.Replace(value) + "'", nil
}

var escapes = strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

// Today is the day it is now in the business time zone, at midnight there.
func (d *Dataset) Today(now time.Time) time.Time {
	t := now.In(d.loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, d.loc)
}
