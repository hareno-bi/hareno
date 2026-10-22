package sqlapi

import (
	"time"
)

// Dates is a dashboard's date range: a span counted back from today, or from one day to another
// (both included). It narrows each dataset tile on its dataset's date (model: date:), read in the
// dataset's business time zone, before the measures are computed, so a tile without a date column
// (revenue by region) narrows too. A SQL tile has no declared date: it is left as it is, and says so.
type Dates struct {
	Last string `json:"last,omitempty"` // one of lasts
	From string `json:"from,omitempty"` // YYYY-MM-DD, with To
	To   string `json:"to,omitempty"`
}

// lasts are the spans counted back from today, today included; "year" is this year so far.
var lasts = map[string]func(today time.Time) (from, to time.Time){
	"7d":       func(t time.Time) (time.Time, time.Time) { return t.AddDate(0, 0, -6), t.AddDate(0, 0, 1) },
	"30d":      func(t time.Time) (time.Time, time.Time) { return t.AddDate(0, 0, -29), t.AddDate(0, 0, 1) },
	"3m":       func(t time.Time) (time.Time, time.Time) { return t.AddDate(0, -3, 1), t.AddDate(0, 0, 1) },
	"12m":      func(t time.Time) (time.Time, time.Time) { return t.AddDate(-1, 0, 1), t.AddDate(0, 0, 1) },
	"year":     func(t time.Time) (time.Time, time.Time) { return startOfYear(t, 0), t.AddDate(0, 0, 1) },
	"lastyear": func(t time.Time) (time.Time, time.Time) { return startOfYear(t, -1), startOfYear(t, 0) },
}

func startOfYear(t time.Time, add int) time.Time {
	return time.Date(t.Year()+add, 1, 1, 0, 0, 0, 0, t.Location())
}

func (d *Dates) valid() bool {
	if d == nil {
		return true
	}
	if d.Last != "" {
		_, ok := lasts[d.Last]
		return ok && d.From == "" && d.To == ""
	}
	from, err1 := time.Parse(time.DateOnly, d.From)
	to, err2 := time.Parse(time.DateOnly, d.To)
	return err1 == nil && err2 == nil && !to.Before(from)
}

// span is the range as days on today's calendar: from included, to not.
func (d *Dates) span(today time.Time) (from, to time.Time) {
	if f, ok := lasts[d.Last]; ok {
		return f(today)
	}
	from, _ = time.ParseInLocation(time.DateOnly, d.From, today.Location())
	to, _ = time.ParseInLocation(time.DateOnly, d.To, today.Location())
	return from, to.AddDate(0, 0, 1)
}
