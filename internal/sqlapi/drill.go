package sqlapi

import (
	"fmt"
	"slices"
)

// Drill-down in place (D4): a dataset tile grouped first by a level of one of its dataset's
// hierarchies (model: hierarchies:) drills down it. Clicking 2026 in a chart by year shows that
// year by quarter in the same tile; clicking 2026-Q1 shows its months. The viewer's drill is the
// values clicked, top down, sent with each run: the tile's first dimension becomes the level below
// the last value, kept to those values. Every level is the dataset query again, so a total at each
// level is computed from the rows, never added up from the level below.
//
// A matrix does not drill: its rows already open down their groups.
type Drill struct {
	Levels []string `json:"levels"` // the tile's own level, then those below it
	At     int      `json:"at"`     // how far down it is: Levels[At] is shown
}

// drilled is t drilled down through values, and where it is in its hierarchy; nil when the tile
// does not drill.
func (h *Handler) drilled(t Tile, values []string) (Tile, *Drill, error) {
	q := t.Dataset
	var levels []string
	if q != nil && t.Matrix == nil && len(q.Dimensions) > 0 && h.Datasets != nil && h.Datasets.ByName[q.Dataset] != nil {
		for _, name := range sortedNames(h.Datasets.ByName[q.Dataset].Hierarchies) {
			l := h.Datasets.ByName[q.Dataset].Hierarchies[name]
			// its own level, not the last; no lower level is already in the tile
			if i := slices.Index(l, q.Dimensions[0]); i >= 0 && i < len(l)-1 && !slices.ContainsFunc(q.Dimensions[1:], func(d string) bool { return slices.Contains(l[i:], d) }) {
				levels = l[i:]
				break
			}
		}
	}
	if levels == nil {
		if len(values) > 0 {
			return t, nil, fmt.Errorf("%w: this tile does not drill down", ErrDataset)
		}
		return t, nil, nil
	}
	if len(values) >= len(levels) {
		return t, nil, fmt.Errorf("%w: %s is as far down as it goes", ErrDataset, levels[len(levels)-1])
	}
	d := h.Datasets.ByName[q.Dataset]
	down := *q
	down.Dimensions = append([]string{levels[len(values)]}, q.Dimensions[1:]...)
	down.Where = slices.Clone(q.Where)
	for i, v := range values {
		cond, err := d.Is(levels[i], v)
		if err != nil {
			return t, nil, fmt.Errorf("%w: %v", ErrDataset, err)
		}
		down.Where = append(down.Where, cond)
	}
	t.Dataset = &down
	return t, &Drill{Levels: levels, At: len(values)}, nil
}

// validDrill: drills by tile, each at most ten values of up to 200 characters.
func validDrill(d map[int][]string) bool {
	for _, vs := range d {
		if len(vs) > 10 || slices.ContainsFunc(vs, func(v string) bool { return len(v) > 200 }) {
			return false
		}
	}
	return len(d) <= 100
}
