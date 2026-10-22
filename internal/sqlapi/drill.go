package sqlapi

import (
	"fmt"
	"maps"
	"slices"
)

// Drill-down in place (D4): a dataset tile grouped first by a level of one of its dataset's
// hierarchies (model: hierarchies:) drills down it. Clicking 2026 in a chart by year shows that
// year by quarter in the same tile; clicking 2026-Q1 shows its months. The viewer's drill is the
// values clicked in each tile, top down, sent with each run: the tile's first dimension becomes the
// level below the last value, kept to those values. A drill narrows the whole dashboard: every
// other dataset tile whose dataset has the levels drilled through is kept to the same values (as
// Power BI's "drilling filters other visuals"). Every level is the dataset query again, so a total
// at each level is computed from the rows, never added up from the level below.
//
// A matrix does not drill itself (its rows already open down their groups), but other tiles'
// drills narrow it.
type Drill struct {
	Levels []string `json:"levels"` // the tile's own level, then those below it
	At     int      `json:"at"`     // how far down it is: Levels[At] is shown
}

// levels are the hierarchy levels t drills down, from its own; nil when it does not drill.
func (h *Handler) levels(t Tile) []string {
	q := t.Dataset
	if q == nil || t.Matrix != nil || len(q.Dimensions) == 0 || h.Datasets == nil || h.Datasets.ByName[q.Dataset] == nil {
		return nil
	}
	hs := h.Datasets.ByName[q.Dataset].Hierarchies
	for _, name := range sortedNames(hs) {
		l := hs[name]
		// its own level, not the last; no lower level is already in the tile
		if i := slices.Index(l, q.Dimensions[0]); i >= 0 && i < len(l)-1 && !slices.ContainsFunc(q.Dimensions[1:], func(d string) bool { return slices.Contains(l[i:], d) }) {
			return l[i:]
		}
	}
	return nil
}

// drilled is tile i drilled down through its own values, narrowed by the other tiles' drills that
// apply to it (returned, by tile), and where it is in its hierarchy (nil when it does not drill).
func (h *Handler) drilled(tiles []Tile, i int, drill map[int][]string) (Tile, *Drill, []int, error) {
	t := tiles[i]
	levels, values := h.levels(t), drill[i]
	if len(values) > 0 && levels == nil {
		return t, nil, nil, fmt.Errorf("%w: this tile does not drill down", ErrDataset)
	}
	if len(values) >= max(len(levels), 1) {
		return t, nil, nil, fmt.Errorf("%w: %s is as far down as it goes", ErrDataset, levels[len(levels)-1])
	}
	if t.Dataset == nil {
		return t, nil, nil, nil
	}
	d := h.Datasets.ByName[t.Dataset.Dataset]
	down := *t.Dataset
	down.Where = slices.Clone(down.Where)
	if levels != nil {
		down.Dimensions = append([]string{levels[len(values)]}, down.Dimensions[1:]...)
	}
	// keep adds the conditions that rows show values at levels; ok is false if d lacks a level
	keep := func(levels, values []string) (bool, error) {
		var conds []string
		for k, v := range values {
			if _, has := d.Dimensions[levels[k]]; !has {
				return false, nil
			}
			cond, err := d.Is(levels[k], v)
			if err != nil {
				return false, fmt.Errorf("%w: %v", ErrDataset, err)
			}
			conds = append(conds, cond)
		}
		down.Where = append(down.Where, conds...)
		return true, nil
	}
	if _, err := keep(levels, values); err != nil {
		return t, nil, nil, err
	}
	var by []int
	for _, j := range slices.Sorted(maps.Keys(drill)) {
		if j == i || j < 0 || j >= len(tiles) || len(drill[j]) == 0 {
			continue
		}
		// a drill too deep for its tile is that tile's error, not this one's
		if l := h.levels(tiles[j]); len(l) > len(drill[j]) {
			if ok, err := keep(l, drill[j]); err != nil {
				return t, nil, nil, err
			} else if ok {
				by = append(by, j)
			}
		}
	}
	t.Dataset = &down
	if levels == nil {
		return t, nil, by, nil
	}
	return t, &Drill{Levels: levels, At: len(values)}, by, nil
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
