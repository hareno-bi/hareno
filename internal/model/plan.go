package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Query is a request against a dataset, by dimension and measure name.
type Query struct {
	Dimensions []string
	Measures   []string
	// Where holds Malloy conditions over the dataset's fields.
	// ponytail: raw text, so only trusted callers (tests, admin); structured filters arrive with
	// the designer (P2-09) and row policy (P2-06).
	Where []string
}

// ErrGrain is wrapped when a measure is grouped by a dimension below the measure's grain.
var ErrGrain = errors.New("measure grouped below its grain")

// Check validates names and grain: a dimension reached through a one-to-many join splits rows
// finer than a sum or average computed above that join, and Malloy would attribute the whole
// amount to every part of the split (S1 C06: an order's shipping counted under each category of
// its lines). Such pairs are refused until the model defines an allocation rule (not supported
// yet).
func (d *Dataset) Check(dimensions, measures []string) error {
	for _, dim := range dimensions {
		if _, ok := d.Dimensions[dim]; !ok {
			return fmt.Errorf("unknown dimension %q", dim)
		}
	}
	for _, m := range measures {
		grains, err := d.grains(m, nil)
		if err != nil {
			return err
		}
		for _, dim := range dimensions {
			p := d.Dimensions[dim]
			joins, _ := d.path(p)
			split := d.lastMany(joins)
			for _, g := range grains {
				if len(g) < len(split) || !slices.Equal(g[:len(split)], split) {
					return fmt.Errorf("%w: %s is at the grain of %s, but %s splits it through %s (one-to-many); "+
						"no allocation rule is defined", ErrGrain, m, grainName(d, g), dim, strings.Join(split, "."))
				}
			}
		}
	}
	return nil
}

// lastMany is the prefix of a join path up to and including its last one-to-many join.
func (d *Dataset) lastMany(joins []string) []string {
	for i := len(joins) - 1; i >= 0; i-- {
		if d.joins[joins[i]].Kind == "many" {
			return joins[:i+1]
		}
	}
	return nil
}

func grainName(d *Dataset, g []string) string {
	if len(g) == 0 {
		return d.Root
	}
	return strings.Join(g, ".")
}

// Plan validates the query and returns it as Malloy, in the dataset's business time zone.
func (d *Dataset) Plan(q Query) (string, error) {
	if len(q.Measures) == 0 && len(q.Dimensions) == 0 {
		return "", errors.New("query has no fields")
	}
	if err := d.Check(q.Dimensions, q.Measures); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run: %s -> {\n  timezone: '%s'\n", d.Name, d.TimeZone)
	for _, w := range q.Where {
		fmt.Fprintf(&b, "  where: %s\n", w)
	}
	if len(q.Dimensions) > 0 {
		fmt.Fprintf(&b, "  group_by: %s\n", strings.Join(q.Dimensions, ", "))
	}
	if len(q.Measures) > 0 {
		fmt.Fprintf(&b, "  aggregate: %s\n", strings.Join(q.Measures, ", "))
	}
	if len(q.Dimensions) > 0 {
		fmt.Fprintf(&b, "  order_by: %s\n", strings.Join(q.Dimensions, ", "))
	}
	b.WriteString("}\n")
	return b.String(), nil
}
